package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/healthcheck"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
)

type fakeManager struct {
	mu        sync.Mutex
	cfg       config.Config
	statuses  []routemanager.RouteStatus
	reloadErr error
	addErr    error
	deleteErr error
	reloads   int
	added     []config.Route
	deleted   []string
}

func (m *fakeManager) Config() config.Config                     { return m.cfg }
func (m *fakeManager) RouteStatuses() []routemanager.RouteStatus { return m.statuses }
func (m *fakeManager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reloads++
	return m.reloadErr
}
func (m *fakeManager) AddRoute(route config.Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, route)
	return m.addErr
}
func (m *fakeManager) DeleteRoute(host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, host)
	return m.deleteErr
}

func request(handler http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	return requestWithHost(handler, "127.0.0.1:9090", method, target, token, body)
}

func requestWithHost(handler http.Handler, host, method, target, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestStatusDashboardShowsOperationalDetailsWithoutSecrets(t *testing.T) {
	checkedAt := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	manager := &fakeManager{
		cfg: config.Config{Admin: config.Admin{Token: "do-not-render"}},
		statuses: []routemanager.RouteStatus{{
			Route: config.Route{Host: "app.localhost", Target: "http://localhost:9000"},
			Health: healthcheck.Status{
				Checked:        true,
				Healthy:        false,
				Latency:        12 * time.Millisecond,
				LastCheck:      checkedAt,
				LastTransition: checkedAt,
				LastError:      "HTTP 503 <private>",
			},
		}},
	}

	rec := request(New(manager, nil), http.MethodGet, "/_/status", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"app.localhost", "Unhealthy", "12ms", "HTTP 503 &lt;private&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, "do-not-render") {
		t.Fatal("dashboard rendered admin token")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestAdminListenerRejectsNonLoopbackHost(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	handler := New(manager, nil)

	cases := []struct {
		name   string
		method string
		target string
		token  string
	}{
		{"status read", http.MethodGet, "/_/status", ""},
		{"reload mutation", http.MethodPost, "/_/reload", "secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := requestWithHost(handler, "evil.example", tc.method, tc.target, tc.token, "")
			if rec.Code != http.StatusMisdirectedRequest {
				t.Fatalf("status = %d, want 421", rec.Code)
			}
		})
	}
	if manager.reloads != 0 {
		t.Fatal("a rebound request must not reach the route manager")
	}
}

func TestLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.1:9090", true},
		{"127.1.2.3:9090", true},
		{"[::1]:9090", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST:9090", true},
		{"localhost.", true},
		{"", false},
		{"evil.example", false},
		{"evil.example:9090", false},
		{"127.0.0.1.evil.example", false},
		{"localhost.evil.example", false},
		{"192.168.100.147:9090", false},
		{"10.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			if got := loopbackHost(tc.host); got != tc.want {
				t.Fatalf("loopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestMutationsRequireBearerScheme(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	handler := New(manager, nil)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"bare token without scheme", "secret", http.StatusUnauthorized},
		{"wrong scheme", "Basic secret", http.StatusUnauthorized},
		{"scheme without token", "Bearer ", http.StatusUnauthorized},
		{"no header", "", http.StatusUnauthorized},
		{"bearer", "Bearer secret", http.StatusOK},
		{"scheme is case-insensitive", "bearer secret", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_/reload", nil)
			req.Host = "127.0.0.1:9090"
			req.RemoteAddr = "127.0.0.1:43210"
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestMutationsRequireConfiguredBearerToken(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	handler := New(manager, nil)

	if rec := request(handler, http.MethodPost, "/_/reload", "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", rec.Code)
	}
	manager.cfg.Admin.Token = ""
	if rec := request(handler, http.MethodPost, "/_/reload", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing configured token status = %d, want 503", rec.Code)
	}
	if manager.reloads != 0 {
		t.Fatalf("reloads = %d, want 0", manager.reloads)
	}
}

func TestAdminTokenReferenceIsResolvedPerRequest(t *testing.T) {
	t.Setenv("SB_ADMIN_TOKEN", "secret")
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "env:SB_ADMIN_TOKEN"}}}
	handler := New(manager, nil)

	if rec := request(handler, http.MethodPost, "/_/reload", "secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("resolved token status = %d, want 200", rec.Code)
	}
	t.Setenv("SB_ADMIN_TOKEN", "")
	if rec := request(handler, http.MethodPost, "/_/reload", "secret", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unresolved reference status = %d, want 503", rec.Code)
	}
}

func TestRouteMutationsAndReloadAreAudited(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}
	recorder := &events.Recorder{}
	handler := New(manager, recorder)

	body := `{"host":"new.localhost","target":"http://localhost:9001"}`
	if rec := request(handler, http.MethodPost, "/routes", "secret", body); rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := request(handler, http.MethodDelete, "/routes/new.localhost", "secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := request(handler, http.MethodPost, "/_/reload", "secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("reload status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if len(manager.added) != 1 || manager.added[0].Host != "new.localhost" {
		t.Fatalf("added routes = %+v", manager.added)
	}
	if len(manager.deleted) != 1 || manager.deleted[0] != "new.localhost" {
		t.Fatalf("deleted hosts = %v", manager.deleted)
	}
	if manager.reloads != 1 {
		t.Fatalf("reloads = %d, want 1", manager.reloads)
	}
	created, ok := recorder.Find(events.RouteCreated)
	if !ok || created.Host != "new.localhost" || created.Actor == "" {
		t.Errorf("route.created event = %+v, %v", created, ok)
	}
	deleted, ok := recorder.Find(events.RouteDeleted)
	if !ok || deleted.Host != "new.localhost" || deleted.Actor == "" {
		t.Errorf("route.deleted event = %+v, %v", deleted, ok)
	}
	reloaded, ok := recorder.Find(events.ConfigReloaded)
	if !ok || reloaded.Actor == "" || reloaded.Error != "" {
		t.Errorf("config.reloaded event = %+v, %v", reloaded, ok)
	}
}

func TestRouteMutationErrorsMapToHTTPStatuses(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		addErr     error
		deleteErr  error
		wantStatus int
	}{
		{name: "duplicate", method: http.MethodPost, target: "/routes", body: `{"host":"app.localhost","target":"http://localhost:9000"}`, addErr: routemanager.ErrRouteExists, wantStatus: http.StatusConflict},
		{name: "unknown JSON", method: http.MethodPost, target: "/routes", body: `{"host":"app.localhost","target":"http://localhost:9000","typo":true}`, wantStatus: http.StatusBadRequest},
		{name: "missing", method: http.MethodDelete, target: "/routes/missing.localhost", deleteErr: routemanager.ErrRouteNotFound, wantStatus: http.StatusNotFound},
		{name: "persistence", method: http.MethodPost, target: "/routes", body: `{"host":"app.localhost","target":"http://localhost:9000"}`, addErr: routemanager.ErrPersistence, wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &fakeManager{
				cfg:       config.Config{Admin: config.Admin{Token: "secret"}},
				addErr:    tt.addErr,
				deleteErr: tt.deleteErr,
			}
			rec := request(New(manager, nil), tt.method, tt.target, "secret", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestReloadFailureReturnsUnprocessableEntity(t *testing.T) {
	manager := &fakeManager{
		cfg:       config.Config{Admin: config.Admin{Token: "secret"}},
		reloadErr: errors.New("invalid config"),
	}
	rec := request(New(manager, nil), http.MethodPost, "/_/reload", "secret", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}
