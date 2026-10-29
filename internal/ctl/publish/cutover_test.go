package publish

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
)

// fakeZone holds one name's records; the public server answers like Pages
// (home page for any path) until the records point at the edge, and then
// like the edge (404 for unknown paths) unless pagesClaimsName is set.
type fakeZone struct {
	mu              sync.Mutex
	records         []certs.SiteRecord
	batches         int
	pagesClaimsName bool
}

func (z *fakeZone) SiteRecords(context.Context, string) ([]certs.SiteRecord, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]certs.SiteRecord{}, z.records...), nil
}

func (z *fakeZone) ReplaceSiteRecords(_ context.Context, name string, remove []certs.SiteRecord, add certs.SiteRecord) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.batches++
	add.ID, add.Name = "r"+string(rune('0'+z.batches)), name
	z.records = []certs.SiteRecord{add}
	return nil
}

func (z *fakeZone) public() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		z.mu.Lock()
		edge := len(z.records) == 1 && z.records[0].Type == "A" && !z.pagesClaimsName
		z.mu.Unlock()
		if !edge {
			_, _ = io.WriteString(w, "pages home")
			return
		}
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, "<h1>portfolio</h1>")
	}))
}

func cutoverRequest(t *testing.T, z *fakeZone) (CutoverRequest, func()) {
	server := z.public()
	return CutoverRequest{Host: "example.com", To: certs.SiteRecord{Type: "A", Content: "203.0.113.10", Proxied: true}, Apply: true,
		Evidence: t.TempDir(), Stamp: "s", Index: []byte("<h1>portfolio</h1>"), Public: server.Client(), PublicURL: server.URL,
		Every: time.Millisecond, Tries: 8}, server.Close
}

func pagesRecord() certs.SiteRecord {
	return certs.SiteRecord{ID: "old", Type: "CNAME", Name: "example.com", Content: "site.pages.dev", Proxied: true}
}

func TestCutoverMovesTheNameAndKeepsTheOldRecord(t *testing.T) {
	z := &fakeZone{records: []certs.SiteRecord{pagesRecord()}}
	req, done := cutoverRequest(t, z)
	defer done()
	if err := Cutover(context.Background(), io.Discard, z, req); err != nil {
		t.Fatal(err)
	}
	if z.records[0].Type != "A" || z.batches != 1 {
		t.Fatalf("records = %+v after %d batches", z.records, z.batches)
	}
	entries, _ := os.ReadDir(req.Evidence)
	if len(entries) != 1 {
		t.Fatal("the replaced record was not kept")
	}
	saved, err := SavedRecord(req.Evidence + "/" + entries[0].Name())
	if err != nil || saved.Content != "site.pages.dev" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	// Running it again is a no-op.
	if err := Cutover(context.Background(), io.Discard, z, req); err != nil || z.batches != 1 {
		t.Fatalf("second run: %v, %d batches", err, z.batches)
	}
}

func TestCutoverGoesBackWhenTheEdgeNeverAnswers(t *testing.T) {
	z := &fakeZone{records: []certs.SiteRecord{pagesRecord()}, pagesClaimsName: true}
	req, done := cutoverRequest(t, z)
	defer done()
	err := Cutover(context.Background(), io.Discard, z, req)
	if err == nil || !strings.Contains(err.Error(), "previous host again") {
		t.Fatalf("err = %v", err)
	}
	if z.records[0].Type != "CNAME" || z.records[0].Content != "site.pages.dev" || z.batches != 2 {
		t.Fatalf("not restored: %+v after %d batches", z.records, z.batches)
	}
}

func TestCutoverGoesBackWhenTheBuildIsWrongAndPlanChangesNothing(t *testing.T) {
	z := &fakeZone{records: []certs.SiteRecord{pagesRecord()}}
	req, done := cutoverRequest(t, z)
	defer done()
	req.Apply = false
	if err := Cutover(context.Background(), io.Discard, z, req); err != nil || z.batches != 0 {
		t.Fatalf("plan: %v, %d batches", err, z.batches)
	}
	req.Apply, req.Index = true, []byte("another build")
	if err := Cutover(context.Background(), io.Discard, z, req); err == nil || z.records[0].Type != "CNAME" {
		t.Fatalf("wrong build kept the cutover: %v %+v", err, z.records)
	}
}
