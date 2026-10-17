package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
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
	for _, path := range []string{"/_/overview", "/_/console.css", "/_/activity.js", "/"} {
		if got := requestWithHost(handler, "evil.example", http.MethodGet, path, "", "").Code; got != http.StatusMisdirectedRequest {
			t.Errorf("%s accepted non-loopback host: %d", path, got)
		}
	}
}

func TestConsoleHomeAndAssets(t *testing.T) {
	broker := events.NewBroker()
	for _, tc := range []struct {
		name        string
		h           *Handler
		destination string
	}{
		{"with events", New(&fakeManager{}, broker, WithEvents(broker)), "/_/activity"},
		{"without events", New(&fakeManager{}, nil), "/_/status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := request(tc.h, http.MethodGet, "/", "", "")
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tc.destination {
				t.Fatalf("home: %d %v", rec.Code, rec.Header())
			}
		})
	}
	handler := New(&fakeManager{}, broker, WithEvents(broker))
	for path, mime := range map[string]string{"/_/console.css": "text/css", "/_/activity.js": "text/javascript"} {
		rec := request(handler, http.MethodGet, path, "", "")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), mime) || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("asset %s: %d %v", path, rec.Code, rec.Header())
		}
	}
	for _, path := range []string{"/_/activity", "/_/status"} {
		rec := request(handler, http.MethodGet, path, "", "")
		if !strings.Contains(rec.Body.String(), "/_/console.css") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "style-src 'self'") {
			t.Fatalf("page asset/CSP: %s", path)
		}
	}
	script := request(handler, http.MethodGet, "/_/activity.js", "", "").Body.String()
	if strings.Contains(script, "innerHTML") {
		t.Fatal("event content must not be parsed as HTML")
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
