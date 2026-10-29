package routemanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

func TestTrustedProxiesSurviveMutationsAndReloadAtomically(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Header.Get("X-Forwarded-For")) }))
	defer upstream.Close()
	cfg := routeConfig(upstream.URL)
	cfg.TrustedProxies = []string{"127.0.0.1"}
	path := writeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://app.localhost/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("CF-Connecting-IP", "198.51.100.23")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("response %d %q, want IP %s", w.Code, w.Body.String(), want)
		}
	}
	check("198.51.100.23")
	if err := m.AddRoute(config.Route{Host: "other.localhost", Target: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.TrustedProxies) != 1 || stored.TrustedProxies[0] != "127.0.0.1/32" {
		t.Fatalf("route mutation lost proxy trust: %v", stored.TrustedProxies)
	}
	clone := m.Config()
	clone.TrustedProxies[0] = "203.0.113.0/24"
	check("198.51.100.23")
	cfg.TrustedProxies = nil
	overwriteConfig(t, path, cfg)
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	check("127.0.0.1")
	cfg.TrustedProxies = []string{"not-a-network"}
	overwriteConfig(t, path, cfg)
	if err := m.Reload(); err == nil {
		t.Fatal("invalid trust list accepted")
	}
	check("127.0.0.1")
}
