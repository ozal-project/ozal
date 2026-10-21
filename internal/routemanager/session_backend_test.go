package routemanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

// WithSessionBackend is the persistence seam: the factory receives the
// configured TTL, an identical reload keeps the installed backend, and a TTL
// change — a policy change — builds a new one.
func TestSessionBackendFactoryHonorsTTLChanges(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

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

	cfg := routeConfig(upstream.URL)
	cfg.Auth = config.Auth{
		Issuer:            provider.URL,
		ClientID:          "client",
		ClientSecret:      "secret",
		RedirectURL:       "http://app.localhost/_/auth/callback",
		SessionTTLMinutes: 60,
	}
	cfg.Routes[0].Access = &config.AccessPolicy{}
	configPath := writeConfig(t, cfg)

	var mu sync.Mutex
	var ttls []time.Duration
	factory := func(lifetime auth.Lifetime) auth.SessionBackend {
		mu.Lock()
		ttls = append(ttls, lifetime.Absolute)
		mu.Unlock()
		return auth.NewSessionStore(lifetime)
	}
	recordedTTLs := func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), ttls...)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, configPath, time.Hour, WithSessionBackend(factory))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	installed := manager.sessions
	if got := recordedTTLs(); len(got) != 1 || got[0] != time.Hour {
		t.Fatalf("factory TTLs = %v, want one call with the configured 60m", got)
	}

	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if manager.sessions != installed {
		t.Fatal("an identical reload replaced the session backend")
	}
	if got := recordedTTLs(); len(got) != 1 {
		t.Fatalf("factory calls = %d, want the backend reused", len(got))
	}

	cfg.Auth.SessionTTLMinutes = 30
	overwriteConfig(t, configPath, cfg)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload after TTL change: %v", err)
	}
	if manager.sessions == installed {
		t.Fatal("a session TTL change must build a new backend")
	}
	if got := recordedTTLs(); len(got) != 2 || got[1] != 30*time.Minute {
		t.Fatalf("factory TTLs = %v, want a second call with 30m", got)
	}
}
