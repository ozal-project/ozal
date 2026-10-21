package routemanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

// ID-007 wiring: with auth.passkeys and an OIDC provider together, the
// snapshot serves the passkey login page with the provider's button and
// sends the provider's start endpoint to the provider.
func TestPasskeysAndOIDCServeTogether(t *testing.T) {
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

	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Listen: ":8888",
		Auth: config.Auth{
			Issuer:       provider.URL,
			ClientID:     "client",
			ClientSecret: "secret",
			RedirectURL:  "http://localhost:8888/_/auth/callback",
			ProviderName: "Okta",
			Passkeys:     &config.Passkeys{RPID: "localhost", LoginURL: "http://localhost:8888"},
		},
		Routes: []config.Route{{Host: "localhost:8888", Target: "http://127.0.0.1:9", Access: &config.AccessPolicy{}}},
	}
	manager, err := New(ctx, writeConfig(t, cfg), time.Hour, WithPasskeyDirectory(sqlite.NewIdentity(db)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:8888"+path, nil)
		rec := httptest.NewRecorder()
		manager.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/_/auth/login"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-oidc="Okta"`) {
		t.Fatalf("login page: %d, provider button missing", rec.Code)
	}
	if rec := get("/_/auth/oidc/start?return_to=http%3A%2F%2Flocalhost%3A8888%2F"); rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), provider.URL+"/authorize") {
		t.Fatalf("OIDC start: %d → %q", rec.Code, rec.Header().Get("Location"))
	}
}
