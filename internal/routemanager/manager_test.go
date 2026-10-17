package routemanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

func writeConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	overwriteConfig(t, path, cfg)
	return path
}

func overwriteConfig(t *testing.T, path string, cfg config.Config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func routeConfig(target string) config.Config {
	return config.Config{
		Listen: ":8888",
		Routes: []config.Route{{
			Host:   "app.localhost",
			Target: target,
		}},
	}
}

func serve(manager *Manager) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.Host = "app.localhost"
	rec := httptest.NewRecorder()
	manager.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestReloadSwapsSnapshotAndPreservesLastGood(t *testing.T) {
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "old")
	}))
	defer oldUpstream.Close()
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "new")
	}))
	defer newUpstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configPath := writeConfig(t, routeConfig(oldUpstream.URL))
	manager, err := New(ctx, configPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if status, body := serve(manager); status != http.StatusOK || body != "old" {
		t.Fatalf("initial response = (%d, %q), want (200, old)", status, body)
	}

	overwriteConfig(t, configPath, routeConfig(newUpstream.URL))
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload valid config: %v", err)
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("reloaded response = (%d, %q), want (200, new)", status, body)
	}

	invalid := routeConfig("ftp://invalid.example")
	overwriteConfig(t, configPath, invalid)
	if err := manager.Reload(); err == nil {
		t.Fatal("Reload invalid config returned nil error")
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("response after failed reload = (%d, %q), want last-good (200, new)", status, body)
	}

	changedListener := routeConfig(oldUpstream.URL)
	changedListener.Listen = ":9999"
	overwriteConfig(t, configPath, changedListener)
	if err := manager.Reload(); err == nil {
		t.Fatal("Reload with changed listener returned nil error")
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("response after rejected listener change = (%d, %q), want (200, new)", status, body)
	}

	changedAdmin := routeConfig(oldUpstream.URL)
	changedAdmin.Admin.Listen = "127.0.0.1:9191"
	overwriteConfig(t, configPath, changedAdmin)
	if err := manager.Reload(); err == nil {
		t.Fatal("Reload with changed admin listener returned nil error")
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("response after rejected admin listener change = (%d, %q), want (200, new)", status, body)
	}
}

func TestConcurrentRequestsSeeWholeSnapshots(t *testing.T) {
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "old")
	}))
	defer oldUpstream.Close()
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "new")
	}))
	defer newUpstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configPath := writeConfig(t, routeConfig(oldUpstream.URL))
	manager, err := New(ctx, configPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const requests = 100
	start := make(chan struct{})
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, body := serve(manager)
			if status != http.StatusOK || (body != "old" && body != "new") {
				errs <- fmt.Errorf("response = (%d, %q), want complete old or new snapshot", status, body)
			}
		}()
	}

	close(start)
	overwriteConfig(t, configPath, routeConfig(newUpstream.URL))
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("final response = (%d, %q), want (200, new)", status, body)
	}
}

func TestConfigReturnsDeepCopy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := routeConfig(upstream.URL)
	cfg.TLS.Domains = []string{"app.localhost"}
	cfg.Routes[0].Access = &config.AccessPolicy{
		AllowedEmails: []string{"user@example.com"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, writeConfig(t, cfg), time.Hour)
	if err == nil {
		t.Fatal("expected auth configuration to be required for protected route")
	}

	cfg.Routes[0].Access = nil
	manager, err = New(ctx, writeConfig(t, cfg), time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	copy := manager.Config()
	copy.Routes[0].Host = "mutated.localhost"
	copy.TLS.Domains[0] = "mutated.localhost"

	fresh := manager.Config()
	if fresh.Routes[0].Host != "app.localhost" {
		t.Fatalf("active route host mutated through Config copy: %q", fresh.Routes[0].Host)
	}
	if fresh.TLS.Domains[0] != "app.localhost" {
		t.Fatalf("active TLS domains mutated through Config copy: %v", fresh.TLS.Domains)
	}
}

func TestCloneConfigDeepCopiesAccessPolicy(t *testing.T) {
	source := routeConfig("http://localhost:9000")
	source.Routes[0].Access = &config.AccessPolicy{
		AllowedEmails:  []string{"user@example.com"},
		AllowedDomains: []string{"example.com"},
		RequiredGroups: []string{"operators"},
	}

	clone := cloneConfig(source)
	clone.Routes[0].Access.AllowedEmails[0] = "changed@example.com"
	clone.Routes[0].Access.AllowedDomains[0] = "changed.example"
	clone.Routes[0].Access.RequiredGroups[0] = "changed"

	policy := source.Routes[0].Access
	if policy.AllowedEmails[0] != "user@example.com" {
		t.Fatalf("source allowed emails mutated: %v", policy.AllowedEmails)
	}
	if policy.AllowedDomains[0] != "example.com" {
		t.Fatalf("source allowed domains mutated: %v", policy.AllowedDomains)
	}
	if policy.RequiredGroups[0] != "operators" {
		t.Fatalf("source required groups mutated: %v", policy.RequiredGroups)
	}
}

func TestAddAndDeleteRoutePersistAndInstallTogether(t *testing.T) {
	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "first")
	}))
	defer firstUpstream.Close()
	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "second")
	}))
	defer secondUpstream.Close()

	cfg := routeConfig(firstUpstream.URL)
	cfg.Admin.Token = "secret"
	configPath := writeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, configPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	added := config.Route{Host: "NEW.Localhost.", Target: secondUpstream.URL, HealthPath: "/"}
	if err := manager.AddRoute(added); err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := manager.AddRoute(added); !errors.Is(err, ErrRouteExists) {
		t.Fatalf("duplicate AddRoute error = %v, want ErrRouteExists", err)
	}

	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load persisted config: %v", err)
	}
	if len(persisted.Routes) != 2 || persisted.Routes[1].Host != "new.localhost" {
		t.Fatalf("persisted routes = %+v", persisted.Routes)
	}

	req := httptest.NewRequest(http.MethodGet, "http://new.localhost/", nil)
	req.Host = "new.localhost"
	rec := httptest.NewRecorder()
	manager.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "second" {
		t.Fatalf("new route response = (%d, %q), want (200, second)", rec.Code, rec.Body.String())
	}

	if err := manager.DeleteRoute("APP.localhost."); err != nil {
		t.Fatalf("DeleteRoute: %v", err)
	}
	persisted, err = config.Load(configPath)
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if len(persisted.Routes) != 1 || persisted.Routes[0].Host != "new.localhost" {
		t.Fatalf("routes after delete = %+v", persisted.Routes)
	}
	if status, _ := serve(manager); status != http.StatusNotFound {
		t.Fatalf("deleted route status = %d, want 404", status)
	}

	if err := manager.DeleteRoute("new.localhost"); err == nil {
		t.Fatal("deleting the final route returned nil error")
	}
	if len(manager.Config().Routes) != 1 {
		t.Fatal("failed deletion changed active snapshot")
	}
	persisted, err = config.Load(configPath)
	if err != nil || len(persisted.Routes) != 1 {
		t.Fatalf("failed deletion changed persisted config: routes=%v err=%v", persisted.Routes, err)
	}
}

func TestPersistenceFailureKeepsActiveSnapshot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "active")
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, writeConfig(t, routeConfig(upstream.URL)), time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	manager.configPath = filepath.Join(t.TempDir(), "missing", "config.json")

	err = manager.AddRoute(config.Route{Host: "new.localhost", Target: upstream.URL})
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("AddRoute error = %v, want ErrPersistence", err)
	}
	if routes := manager.Config().Routes; len(routes) != 1 || routes[0].Host != "app.localhost" {
		t.Fatalf("active routes changed after persistence failure: %+v", routes)
	}
}

func TestRouteStatusesExposeLatestHealthObservation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, writeConfig(t, routeConfig(upstream.URL)), time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	statuses := manager.RouteStatuses()
	if len(statuses) != 1 {
		t.Fatalf("len(RouteStatuses) = %d, want 1", len(statuses))
	}
	status := statuses[0]
	if !status.Health.Checked || status.Health.Healthy {
		t.Fatalf("health = %+v, want checked and unhealthy", status.Health)
	}
	if status.Health.LastCheck.IsZero() || status.Health.LastTransition.IsZero() {
		t.Fatalf("health timestamps not populated: %+v", status.Health)
	}
	if status.Health.LastError != "HTTP 502" {
		t.Fatalf("LastError = %q, want HTTP 502", status.Health.LastError)
	}
}

func TestReloadReusesOIDCGateWhenAuthBoundaryIsUnchanged(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 provider.URL,
			"authorization_endpoint": provider.URL + "/authorize",
			"token_endpoint":         provider.URL + "/token",
			"jwks_uri":               provider.URL + "/jwks",
		})
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := routeConfig(upstream.URL)
	cfg.Auth = config.Auth{
		Issuer:       provider.URL,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURL:  "http://app.localhost/_/auth/callback",
	}
	cfg.Routes[0].Access = &config.AccessPolicy{}
	configPath := writeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, configPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	initialGate := manager.gate
	if initialGate == nil {
		t.Fatal("initial OIDC gate is nil")
	}

	overwriteConfig(t, configPath, cfg)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if manager.gate != initialGate {
		t.Fatal("identical reload replaced OIDC gate and would invalidate sessions")
	}

	if err := manager.AddRoute(config.Route{Host: "public.localhost", Target: upstream.URL}); err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if manager.gate != initialGate {
		t.Fatal("adding an unprotected route replaced OIDC gate")
	}
}

func TestAuthEndpointsOnlyExistOnAuthHosts(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 provider.URL,
			"authorization_endpoint": provider.URL + "/authorize",
			"token_endpoint":         provider.URL + "/token",
			"jwks_uri":               provider.URL + "/jwks",
		})
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := routeConfig(upstream.URL)
	cfg.Auth = config.Auth{
		Issuer:       provider.URL,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURL:  "http://app.localhost/_/auth/callback",
	}
	cfg.Routes[0].Access = &config.AccessPolicy{}
	cfg.Routes = append(cfg.Routes, config.Route{Host: "public.localhost", Target: upstream.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, writeConfig(t, cfg), time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	do := func(method, host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://"+host+path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		manager.ServeHTTP(rec, req)
		return rec
	}

	// On a public host the path belongs to the upstream, not to Switchboard.
	rec := do(http.MethodGet, "public.localhost", "/_/auth/login")
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-Upstream-Path") != "/_/auth/login" {
		t.Fatalf("public /_/auth/login = %d path=%q, want the upstream 204", rec.Code, rec.Header().Get("X-Upstream-Path"))
	}
	if rec := do(http.MethodPost, "public.localhost", "/_/auth/logout"); rec.Code != http.StatusNoContent {
		t.Fatalf("public POST /_/auth/logout = %d, want the upstream 204", rec.Code)
	}

	// On a protected host the login endpoint answers with the provider redirect.
	if rec := do(http.MethodGet, "app.localhost", "/_/auth/login"); rec.Code != http.StatusFound {
		t.Fatalf("protected /_/auth/login = %d, want 302 to the provider", rec.Code)
	}

	// Logout is a POST; a GET is rejected before touching the gate.
	rec = do(http.MethodGet, "app.localhost", "/_/auth/logout")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("protected GET /_/auth/logout = %d Allow=%q, want 405 with Allow: POST", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := do(http.MethodPost, "app.localhost", "/_/auth/logout"); rec.Code != http.StatusSeeOther {
		t.Fatalf("protected POST /_/auth/logout = %d, want 303", rec.Code)
	}
}

func TestControlPlaneWritesPersistSecretReferences(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 provider.URL,
			"authorization_endpoint": provider.URL + "/authorize",
			"token_endpoint":         provider.URL + "/token",
			"jwks_uri":               provider.URL + "/jwks",
		})
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	t.Setenv("SB_TEST_CLIENT_SECRET", "resolved-secret-value")
	cfg := routeConfig(upstream.URL)
	cfg.Auth = config.Auth{
		Issuer:       provider.URL,
		ClientID:     "client",
		ClientSecret: "env:SB_TEST_CLIENT_SECRET",
		RedirectURL:  "http://app.localhost/_/auth/callback",
	}
	cfg.Routes[0].Access = &config.AccessPolicy{}
	path := writeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := manager.AddRoute(config.Route{Host: "public.localhost", Target: upstream.URL}); err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "env:SB_TEST_CLIENT_SECRET") {
		t.Fatalf("control-plane write lost the secret reference:\n%s", data)
	}
	if strings.Contains(string(data), "resolved-secret-value") {
		t.Fatalf("control-plane write persisted the resolved secret:\n%s", data)
	}
}

func TestUnresolvedSecretReferenceFailsStartup(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": provider.URL})
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	t.Setenv("SB_TEST_CLIENT_SECRET", "")
	cfg := routeConfig(upstream.URL)
	cfg.Auth = config.Auth{
		Issuer:       provider.URL,
		ClientID:     "client",
		ClientSecret: "env:SB_TEST_CLIENT_SECRET",
		RedirectURL:  "http://app.localhost/_/auth/callback",
	}
	cfg.Routes[0].Access = &config.AccessPolicy{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := New(ctx, writeConfig(t, cfg), time.Hour)
	if err == nil || !strings.Contains(err.Error(), "resolve auth.client_secret") {
		t.Fatalf("New error = %v, want a resolve auth.client_secret failure", err)
	}
}

func TestSessionsSurviveGateRebuildWhenProtectedHostsChange(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 provider.URL,
			"authorization_endpoint": provider.URL + "/authorize",
			"token_endpoint":         provider.URL + "/token",
			"jwks_uri":               provider.URL + "/jwks",
		})
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := routeConfig(upstream.URL)
	cfg.Auth = config.Auth{
		Issuer:       provider.URL,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURL:  "http://app.localhost/_/auth/callback",
		// The shared cookie domain is what lets a second protected host exist;
		// changing that boundary is exactly what forces a gate rebuild.
		CookieDomain: "localhost",
	}
	cfg.Routes[0].Access = &config.AccessPolicy{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, writeConfig(t, cfg), time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if manager.sessions == nil {
		t.Fatal("expected a session store after building the gate")
	}
	id, err := manager.sessions.Create(auth.Principal{Subject: "user-1", Email: "ana@example.com", EmailVerified: true})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	initialGate := manager.gate

	// A second protected route changes the gate's host boundary, so the gate
	// is rebuilt — the session store must outlive that rebuild.
	if err := manager.AddRoute(config.Route{Host: "app2.localhost", Target: upstream.URL, Access: &config.AccessPolicy{}}); err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if manager.gate == initialGate {
		t.Fatal("expected a new gate after the protected boundary changed")
	}
	if manager.sessions == nil {
		t.Fatal("session store disappeared after the rebuild")
	}

	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: id})
	principal, ok := manager.gate.Authenticate(req)
	if !ok || principal.Email != "ana@example.com" {
		t.Fatalf("Authenticate after rebuild = (%+v, %v), want the session created before the rebuild", principal, ok)
	}
}
