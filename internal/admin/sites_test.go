package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
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
	if site == "cut" {
		return pages.Deployment{}, fmt.Errorf("%w: %w", pages.ErrUploadInterrupted, os.ErrDeadlineExceeded)
	}
	if site == "hostile" {
		return pages.Deployment{}, &pages.RefusedError{Code: pages.UnsafePath, Reason: "entry name contains a parent-directory component"}
	}
	if site == "broken" {
		return pages.Deployment{}, fmt.Errorf("write /private/ozal/sites/blog/index.html: %w", os.ErrPermission)
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

type metadataDeployments struct{ fakeDeployments }

func (*metadataDeployments) Sites(context.Context) ([]pages.Site, error) {
	return []pages.Site{{
		Name: "blog", Live: strings.Repeat("a", 64),
		Deployments: []pages.Deployment{{
			Project: "private-project", CommitSHA: "private-commit",
			RunURL: "https://github.com/private/repository/actions/runs/42",
		}},
	}}, nil
}

func sitesHandler(store *fakeDeployments, sink events.Sink, extra ...Option) http.Handler {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	options := append([]Option{WithPages(store)}, extra...)
	return New(manager, sink, options...)
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

func TestSiteListingRequiresSitesAuthority(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret", SitesToken: "publisher"}}}
	h := New(manager, nil, WithPages(&fakeDeployments{}))
	peopleToken, _, err := h.operators.issue("People operator", false, "people")
	if err != nil {
		t.Fatal(err)
	}
	sitesToken, _, err := h.operators.issue("Sites operator", false, "sites")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"wrong scope", peopleToken, http.StatusForbidden},
		{"sites scope", sitesToken, http.StatusOK},
		{"publisher", "publisher", http.StatusOK},
		{"admin", "secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := request(h, http.MethodGet, "/_/api/sites", tc.token, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestDeployAndRollbackEmitEvents(t *testing.T) {
	store := &fakeDeployments{}
	sink := &events.Recorder{}
	audit := &recordingMutationAudit{}
	handler := sitesHandler(store, sink, WithAdminMutationAudit(audit, true))

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

	rec = request(handler, http.MethodGet, "/_/api/sites", "secret", "")
	var listed struct {
		Sites []pages.Site `json:"sites"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Sites) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	for _, code := range []string{"site.deploy", "site.rollback"} {
		if !hasMutationCode(audit.events, code) {
			t.Errorf("durable admin intents missing %q: %+v", code, audit.events)
		}
	}
	for _, event := range audit.events {
		if event.Type != events.AdminMutationRequested || event.Host != "blog" {
			t.Errorf("site mutation intent = %+v, want safe blog target", event)
		}
	}
}

func TestSiteListDoesNotExposeDeploymentSourceMetadata(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	handler := New(manager, nil, WithPages(&metadataDeployments{}))
	rec := request(handler, http.MethodGet, "/_/api/sites", "secret", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("site list = %d %s, want 200", rec.Code, rec.Body)
	}
	for _, value := range []string{"private-project", "private-commit", "private/repository", "actions/runs/42"} {
		if strings.Contains(rec.Body.String(), value) {
			t.Errorf("unauthenticated site list exposed deployment metadata %q: %s", value, rec.Body)
		}
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

func TestStorageFailureDoesNotExposeFilesystemError(t *testing.T) {
	sink := &events.Recorder{}
	rec := request(sitesHandler(&fakeDeployments{}, sink), http.MethodPost, "/_/api/sites/broken/deployments", "secret", "archive")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/private/ozal") {
		t.Fatalf("storage failure response = %d %s, want generic 500", rec.Code, rec.Body)
	}
	event, ok := sink.Find(events.PagesFailed)
	if !ok {
		t.Fatal("storage failure did not emit pages.failed")
	}
	if event.Code != "deployment.failed" || event.Reason != "deployment could not be completed" || event.Error != "" {
		t.Fatalf("failure event exposed unexpected details: %+v", event)
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

// slowBody hands out one byte per tick, like an archive over a slow link.
type slowBody struct {
	data []byte
	tick time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.tick)
	p[0], b.data = b.data[0], b.data[1:]
	return 1, nil
}

// The admin server cuts requests at its ReadTimeout; an authenticated site
// upload gets its own, longer deadline (2026-10-21: deploys over the homelab
// tunnel were cut at 10 s and reported as broken archives).
func TestASlowSiteUploadOutlivesTheServerReadTimeout(t *testing.T) {
	store := &fakeDeployments{}
	server := httptest.NewUnstartedServer(sitesHandler(store, nil))
	server.Config.ReadTimeout = 200 * time.Millisecond
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Start()
	defer server.Close()

	body := &slowBody{data: []byte("archive"), tick: 100 * time.Millisecond} // ~700 ms
	req, err := http.NewRequest(http.MethodPost, server.URL+"/_/api/sites/blog/deployments", body)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len("archive"))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201", resp.StatusCode)
	}
	if len(store.received) != 1 || store.received[0] != "blog:archive" {
		t.Fatalf("store received %v", store.received)
	}
}

func TestAnInterruptedUploadSaysSo(t *testing.T) {
	sink := &events.Recorder{}
	rec := request(sitesHandler(&fakeDeployments{}, sink), http.MethodPost, "/_/api/sites/cut/deployments", "secret", "archive")
	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("status %d, want 408", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != "upload.timeout" {
		t.Fatalf("body = %s, want code upload.timeout", rec.Body.String())
	}
	got := sink.Events()
	if len(got) != 1 || got[0].Type != events.PagesRefused || got[0].Code != "upload.timeout" {
		t.Fatalf("events = %+v, want one pages.refused with code upload.timeout", got)
	}
}

func scopedHandler(sitesToken string) (http.Handler, *fakeDeployments) {
	store := &fakeDeployments{}
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret", SitesToken: sitesToken}}}
	return New(manager, nil, WithPages(store)), store
}

// The sites token publishes and rolls back sites; it opens nothing else.
func TestSitesTokenDeploysAndNothingElse(t *testing.T) {
	handler, store := scopedHandler("sites-secret")
	if rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", "sites-secret", "archive"); rec.Code != http.StatusCreated {
		t.Fatalf("deploy with the sites token = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/sites/blog/rollback", "sites-secret", `{"id":"`+strings.Repeat("a", 64)+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("rollback with the sites token = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", "secret", "archive"); rec.Code != http.StatusCreated {
		t.Fatalf("the admin token must still deploy: %d", rec.Code)
	}
	for _, target := range []string{"/_/reload", "/_/api/policy/explain", "/routes"} {
		if rec := request(handler, http.MethodPost, target, "sites-secret", "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s with the sites token = %d, want 401", target, rec.Code)
		}
	}
	if len(store.received) != 2 {
		t.Fatalf("store received %v", store.received)
	}
}

// A sites token whose file is missing only takes itself out.
func TestAnUnreadableSitesTokenLeavesTheAdminTokenWorking(t *testing.T) {
	handler, _ := scopedHandler("file:/nonexistent/sites.token")
	if rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", "secret", "archive"); rec.Code != http.StatusCreated {
		t.Fatalf("admin token deploy = %d %s", rec.Code, rec.Body)
	}
	if rec := request(handler, http.MethodPost, "/_/api/sites/blog/deployments", "", "archive"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", rec.Code)
	}
}
