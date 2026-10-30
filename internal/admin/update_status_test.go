package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestUpdateStatusAPIRequiresTheUpdatesToolAndReturnsOnlyTheProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	if err := os.WriteFile(path, []byte(`{"checked_at":"2026-10-30T04:00:00Z","installed":"0.1.0","latest":"0.2.0","result":"available","reason":"private failure detail: token=DO_NOT_EXPOSE"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
	updates, _, err := h.operators.issue("Updater", false, "updates")
	if err != nil {
		t.Fatal(err)
	}
	people, _, err := h.operators.issue("People", false, "people")
	if err != nil {
		t.Fatal(err)
	}
	if rec := request(h, http.MethodGet, "/_/api/updates/status", people, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("people-only session = %d, want 403", rec.Code)
	}
	rec := request(h, http.MethodGet, "/_/api/updates/status", updates, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result":"available"`) || !strings.Contains(rec.Body.String(), `"latest":"0.2.0"`) {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("update status response can be cached")
	}
	if strings.Contains(rec.Body.String(), "DO_NOT_EXPOSE") {
		t.Fatal("private updater detail was returned by the console API")
	}
}

func TestUpdateStatusAPIReturnsUnknownBeforeFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created.json")
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
	rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result":"unknown"`) {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateStatusAPIRefusesMalformedOrSymlinkedFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"result":"root-command-accepted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(bad))
	if rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown result = %d, want 503", rec.Code)
	}
	link := filepath.Join(dir, "status-link.json")
	if err := os.Symlink(bad, link); err != nil {
		t.Fatal(err)
	}
	h = New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(link))
	if rec := request(h, http.MethodGet, "/_/api/updates/status", readToken, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("symlink status = %d, want 503", rec.Code)
	}
}

func TestUpdateStatusReportAcceptsOnlyTheHostSocketAndPersistsBoundedPairState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: readToken}}}, nil, WithUpdateStatusFile(path))
	body := `{"checked_at":"2026-10-30T04:00:00Z","installed":"0.1.0","latest":"0.2.0","result":"updating","job_id":"pair-123","phase":"installing","reason":"must be replaced","nodes":[{"role":"edge","name":"edge","installed":"0.1.0","latest":"0.2.0","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","result":"staged"},{"role":"connector","name":"home","installed":"0.1.0","latest":"0.2.0","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","result":"staged"}]}`
	remote := request(h, http.MethodPost, "/_/api/updates/status", readToken, body)
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote report = %d, want 403", remote.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/_/api/updates/status", strings.NewReader(body))
	req.Host = "127.0.0.1:9090"
	req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || strings.Contains(rec.Body.String(), "must be replaced") || !strings.Contains(rec.Body.String(), `"result":"updating"`) {
		t.Fatalf("local report = %d %s", rec.Code, rec.Body.String())
	}
	got := request(h, http.MethodGet, "/_/api/updates/status", readToken, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"role":"connector"`) || strings.Contains(got.Body.String(), "must be replaced") {
		t.Fatalf("read pair status = %d %s", got.Code, got.Body.String())
	}
}

func TestUpdateStatusReportRejectsUnknownFieldsAndInvalidRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	h := New(&fakeManager{}, nil, WithUpdateStatusFile(path))
	for _, body := range []string{
		`{"checked_at":"2026-10-30T04:00:00Z","result":"updating","command":"systemctl reboot"}`,
		`{"checked_at":"2026-10-30T04:00:00Z","result":"updating","nodes":[{"role":"host","name":"edge","result":"pending"}]}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/_/api/updates/status", strings.NewReader(body))
		req.Host = "127.0.0.1:9090"
		req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", body, rec.Code)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid status modified persisted state: %v", err)
	}
}
