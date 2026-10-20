package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/pages"
)

// fakeDeployments records what reached the store and answers per site.
type fakeDeployments struct {
	received []string
}

func (f *fakeDeployments) Deploy(_ context.Context, site string, archive io.Reader) (pages.Deployment, error) {
	body, err := io.ReadAll(archive)
	if err != nil {
		return pages.Deployment{}, err // MaxBytesReader refusing the body
	}
	f.received = append(f.received, site+":"+string(body))
	if site == "hostile" {
		return pages.Deployment{}, &pages.RefusedError{Code: pages.UnsafePath, Reason: "entry name contains a parent-directory component"}
	}
	return pages.Deployment{Site: site, ID: strings.Repeat("a", 64), Live: true}, nil
}

func (f *fakeDeployments) Rollback(_ context.Context, site, id string) (pages.Deployment, error) {
	if id != strings.Repeat("a", 64) {
		return pages.Deployment{}, pages.ErrNotFound
	}
	return pages.Deployment{Site: site, ID: id, Live: true}, nil
}

func (f *fakeDeployments) Sites(context.Context) ([]pages.Site, error) {
	return []pages.Site{{Name: "blog", Live: strings.Repeat("a", 64)}}, nil
}

func sitesHandler(store *fakeDeployments, sink events.Sink) http.Handler {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	return New(manager, sink, WithPages(store))
}

func TestDeployRequiresTheAdminToken(t *testing.T) {
	store := &fakeDeployments{}
	handler := sitesHandler(store, nil)
	for _, token := range []string{"", "wrong"} {
		if rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", token, "archive"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d, want 401", token, rec.Code)
		}
	}
	if len(store.received) != 0 {
		t.Fatalf("an unauthenticated upload reached the store: %v", store.received)
	}
}

func TestDeployAndRollbackEmitEvents(t *testing.T) {
	store := &fakeDeployments{}
	sink := &events.Recorder{}
	handler := sitesHandler(store, sink)

	rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", "secret", "archive")
	if rec.Code != http.StatusCreated || len(store.received) != 1 || store.received[0] != "blog:archive" {
		t.Fatalf("deploy: %d %s, store got %v", rec.Code, rec.Body, store.received)
	}
	if e, ok := sink.Find(events.PagesDeployed); !ok || e.Host != "blog" || e.Reason != strings.Repeat("a", 64) {
		t.Fatalf("deploy event = %+v, %v", e, ok)
	}

	rec = request(handler, http.MethodPost, "/_/api/sites/blog/rollback", "secret", `{"id":"`+strings.Repeat("a", 64)+`"}`)
	if rec.Code != http.StatusOK || !sink.Has(events.PagesRolledBack) {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/sites/blog/rollback", "secret", `{"id":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("rollback to an unknown deployment: %d, want 404", rec.Code)
	}

	rec = request(handler, http.MethodGet, "/_/api/sites", "", "")
	var listed struct {
		Sites []pages.Site `json:"sites"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Sites) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

func TestRefusedDeployReturnsItsCode(t *testing.T) {
	sink := &events.Recorder{}
	rec := request(sitesHandler(&fakeDeployments{}, sink), http.MethodPost, "/_/api/sites/hostile/deployments", "secret", "archive")
	var body map[string]string
	if rec.Code != http.StatusUnprocessableEntity || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != string(pages.UnsafePath) {
		t.Fatalf("hostile deploy: %d %s", rec.Code, rec.Body)
	}
	if e, ok := sink.Find(events.PagesRefused); !ok || e.Code != string(pages.UnsafePath) {
		t.Fatalf("refusal event = %+v, %v", e, ok)
	}
}

func TestSitesConsoleRouteServesTheApp(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	broker := events.NewBroker()
	handler := New(manager, broker, WithEvents(broker), WithPages(&fakeDeployments{}))
	if rec := request(handler, http.MethodGet, "/sites", "", ""); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/sites: %d %v", rec.Code, rec.Header())
	}
}
