package admin

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/healthcheck"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
)

func TestOverviewProjectsAccessRulesWithoutConfigSecrets(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: "hidden-admin-token"}, Auth: config.Auth{ClientSecret: "hidden-client-secret"},
		Routes: []config.Route{
			{Host: "public.localhost", Root: "/private/site-path"},
			{Host: "protected.localhost", Target: "http://private-upstream:1234", Access: &config.AccessPolicy{AllowedEmails: []string{"private@example.test"}, RequiredGroups: []string{"private-group"}}},
			{Host: "legacy.localhost", AuthRequired: true},
		},
	}}
	broker := events.NewBroker()
	handler := New(manager, broker, WithEvents(broker))
	rec := request(handler, http.MethodGet, "/_/overview", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var view struct {
		Routes []accessRoute `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Routes) != 3 || view.Routes[0].Protected || !view.Routes[1].Protected || !view.Routes[2].Protected {
		t.Fatalf("wrong access rules: %+v", view)
	}
	if view.Routes[0].Kind != "Static files" || view.Routes[1].Policy != "1 emails / 0 domains / 1 required groups" {
		t.Fatalf("wrong projection: %+v", view)
	}
	for _, private := range []string{"hidden-admin-token", "hidden-client-secret", "/private/site-path", "private-upstream", "private@example.test", "private-group"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("overview exposed %q", private)
		}
	}
	for _, path := range []string{"/_/overview", "/_/console.css", "/_/api/status", "/activity", "/"} {
		if got := requestWithHost(handler, "evil.example", http.MethodGet, path, "", "").Code; got != http.StatusMisdirectedRequest {
			t.Errorf("%s accepted non-loopback host: %d", path, got)
		}
	}
}

func TestAuditStatusAppearsInHTMLAndJSONWithoutRawIOError(t *testing.T) {
	status := AuditStatus{Available: false, FailureCount: 3, LastFailureAt: "2026-10-25T12:00:00Z"}
	handler := New(&fakeManager{}, nil, WithAuditStatus(func() AuditStatus { return status }))
	api := request(handler, http.MethodGet, "/_/api/status", "", "")
	if api.Code != http.StatusOK {
		t.Fatalf("api status = %d", api.Code)
	}
	var body struct {
		Audit *AuditStatus `json:"audit"`
	}
	if err := json.Unmarshal(api.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Audit == nil || body.Audit.Available || body.Audit.FailureCount != 3 || body.Audit.LastFailureAt != status.LastFailureAt {
		t.Fatalf("audit status = %+v", body.Audit)
	}
	page := request(handler, http.MethodGet, "/_/status", "", "")
	for _, want := range []string{"Durable audit", "Selected event families", "Unavailable", "3", status.LastFailureAt, "Required operations fail closed"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("status page did not show %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "permission denied") || strings.Contains(api.Body.String(), "disk full") {
		t.Fatal("admin status exposed a raw storage error")
	}
}

func TestConsoleHomeAndAssets(t *testing.T) {
	broker := events.NewBroker()
	handler := New(&fakeManager{}, broker, WithEvents(broker))

	// Every client route gets the app shell with the strict CSP.
	for _, path := range []string{"/", "/overview", "/activity", "/routes", "/simulator"} {
		rec := request(handler, http.MethodGet, path, "", "")
		csp := rec.Header().Get("Content-Security-Policy")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
			!strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") {
			t.Fatalf("console %s: %d %v", path, rec.Code, rec.Header())
		}
		if strings.Contains(rec.Body.String(), "<script>") {
			t.Fatalf("console %s has an inline script", path)
		}
	}

	// The CSP allows fonts from this origin only. A font that Vite inlined as a
	// data: URI is blocked by the browser, so none may be embedded in the CSS.
	for _, css := range mustGlob(t, "console/assets/*.css") {
		body, err := fs.ReadFile(consoleApp, css)
		if err != nil || bytes.Contains(body, []byte("data:font")) {
			t.Fatalf("%s inlines a font as a data: URI, which font-src 'self' refuses (%v)", css, err)
		}
	}

	// Built assets are served with their type; hashed names are immutable.
	assets, err := fs.Glob(consoleApp, "console/assets/*.js")
	if err != nil || len(assets) == 0 {
		t.Fatalf("no built console script embedded: %v", err)
	}
	asset := "/" + strings.TrimPrefix(assets[0], "console/")
	index := request(handler, http.MethodGet, "/activity", "", "")
	if !strings.Contains(index.Body.String(), asset) {
		t.Fatalf("console index does not reference its built script %s", asset)
	}
	rec := request(handler, http.MethodGet, asset, "", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("asset %s: %d %v", asset, rec.Code, rec.Header())
	}
	// The Overview (UI-005) is the console's first page and says what needs the
	// operator, what changed, who got in and where each site came from.
	for _, want := range []string{"Nothing needs you right now.", "Recent changes", "Who got in", "What is live, and where it came from", "uploaded by hand"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the embedded overview console lacks %q", want)
		}
	}
	for _, want := range []string{
		"pages.failed", "Deployment failed",
		"route.create_failed", "Route creation failed",
		"route.delete_failed", "Route removal failed",
		"config.reload_failed", "Configuration reload failed",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the embedded activity console lacks %q", want)
		}
	}
	if strings.Contains(rec.Body.String(), "localStorage") {
		t.Fatal("the console must not persist anything in localStorage (the admin token included)")
	}

	// Unknown paths are 404, and control-plane paths never fall through to the app.
	for _, path := range []string{"/nope", "/assets/missing.js", "/_/unknown", "/console.go", "/console/index.html"} {
		if got := request(handler, http.MethodGet, path, "", "").Code; got != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, got)
		}
	}

	// Traversal is cleaned by the mux before the console sees it; never 200.
	if got := request(handler, http.MethodGet, "/../console.go", "", "").Code; got == http.StatusOK {
		t.Error("path traversal served a file")
	}

	// The route debug page still works without JavaScript.
	status := request(handler, http.MethodGet, "/_/status", "", "")
	if !strings.Contains(status.Body.String(), "/_/console.css") || !strings.Contains(status.Header().Get("Content-Security-Policy"), "style-src 'self'") {
		t.Fatal("route debug page lost its stylesheet or CSP")
	}

	// Without an event broker there is no app; home falls back to the debug view.
	rec = request(New(&fakeManager{}, nil), http.MethodGet, "/", "", "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/_/status" {
		t.Fatalf("home without events: %d %v", rec.Code, rec.Header())
	}
}

func TestAPIStatusMatchesDebugViewWithoutSecrets(t *testing.T) {
	manager := &fakeManager{
		cfg: config.Config{Admin: config.Admin{Token: "hidden-admin-token"}},
		statuses: []routemanager.RouteStatus{{Route: config.Route{Host: "app.localhost", Target: "http://localhost:9000",
			Access: &config.AccessPolicy{AllowedEmails: []string{"private@example.test"}, RequiredGroups: []string{"private-group"}}}}},
	}
	rec := request(New(manager, nil), http.MethodGet, "/_/api/status", "", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("api status: %d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	for _, private := range []string{"hidden-admin-token", "private@example.test", "private-group"} {
		if strings.Contains(body, private) {
			t.Fatalf("api status exposed %q", private)
		}
	}
	for _, want := range []string{`"host":"app.localhost"`, `"access":"Protected"`, `"state":"Unchecked"`, `"policy":"1 emails / 0 domains / 1 required groups"`, `"config_sha256":"digest-of-"`} {
		if !strings.Contains(body, want) {
			t.Errorf("api status missing %s: %s", want, body)
		}
	}
}

func TestStatusShowsHealthForEachPoolTarget(t *testing.T) {
	checkedAt := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	manager := &fakeManager{statuses: []routemanager.RouteStatus{{
		Route: config.Route{
			Host:      "app.localhost",
			Upstreams: []string{"http://one.local", "http://two.local"},
		},
		Health: healthcheck.Status{Checked: true, Healthy: true, LastCheck: checkedAt, LastTransition: checkedAt},
		Upstreams: []healthcheck.TargetStatus{
			{Target: "http://one.local", Status: healthcheck.Status{Checked: true, Healthy: true, LastCheck: checkedAt}},
			{Target: "http://two.local", Status: healthcheck.Status{Checked: true, Healthy: false, LastCheck: checkedAt, LastError: "HTTP 503"}},
		},
	}}}
	handler := New(manager, nil)
	page := request(handler, http.MethodGet, "/_/status", "", "")
	if page.Code != http.StatusOK {
		t.Fatalf("status page = %d, want 200", page.Code)
	}
	for _, want := range []string{"Pool targets", "http://one.local", "http://two.local", "Unhealthy", "HTTP 503"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("status page missing %q", want)
		}
	}

	api := request(handler, http.MethodGet, "/_/api/status", "", "")
	if api.Code != http.StatusOK || !strings.Contains(api.Body.String(), `"state":"Unhealthy"`) || !strings.Contains(api.Body.String(), `"target":"http://two.local"`) {
		t.Fatalf("API status does not include per-target health: %d %s", api.Code, api.Body.String())
	}
}

func TestStaticRouteShowsAccessAndNoProbe(t *testing.T) {
	manager := &fakeManager{statuses: []routemanager.RouteStatus{{Route: config.Route{Host: "site.localhost", Root: "/tmp/site"}}}}
	rec := request(New(manager, nil), http.MethodGet, "/_/status", "", "")
	for _, want := range []string{"Public", "No login required", "Static files", "Not probed"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(rec.Body.String(), "Unchecked") {
		t.Fatal("static routes must not imply a pending HTTP probe")
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	files, err := fs.Glob(consoleApp, pattern)
	if err != nil || len(files) == 0 {
		t.Fatalf("no embedded files match %s: %v", pattern, err)
	}
	return files
}
