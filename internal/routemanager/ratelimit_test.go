package routemanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

func TestRateLimitKeysOnResolvedClientIP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()
	cfg := routeConfig(upstream.URL)
	cfg.TrustedProxies = []string{"127.0.0.1"}
	cfg.RateLimit = &config.RateLimit{
		General: config.Limit{Rate: 0.001, Burst: 2},
		Auth:    config.Limit{Rate: 0.001, Burst: 1},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := New(ctx, writeConfig(t, cfg), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	do := func(path, client string) int {
		r := httptest.NewRequest("GET", "http://app.localhost"+path, nil)
		r.RemoteAddr = "127.0.0.1:1234" // the trusted tunnel peer, shared by every client
		r.Header.Set("CF-Connecting-IP", client)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w.Code
	}

	for i := 0; i < 2; i++ {
		if code := do("/", "198.51.100.1"); code != 200 {
			t.Fatalf("request %d within burst: got %d", i, code)
		}
	}
	if code := do("/", "198.51.100.1"); code != http.StatusTooManyRequests {
		t.Fatalf("over burst: got %d, want 429", code)
	}
	if code := do("/", "198.51.100.2"); code != 200 {
		t.Fatalf("a different client behind the same peer was throttled: got %d", code)
	}
	// The auth bucket is separate and stricter (burst 1), even for /_/auth/
	// paths that no route serves.
	first := do("/_/auth/login", "198.51.100.3")
	if first == http.StatusTooManyRequests {
		t.Fatal("first auth request must not be limited")
	}
	if code := do("/_/auth/login", "198.51.100.3"); code != http.StatusTooManyRequests {
		t.Fatalf("second auth request: got %d, want 429", code)
	}
}

func TestRateLimitConfigRejectsNonPositive(t *testing.T) {
	cfg := routeConfig("http://127.0.0.1:1")
	cfg.RateLimit = &config.RateLimit{General: config.Limit{Rate: 1, Burst: 1}}
	if _, err := config.Validate(cfg); err == nil {
		t.Fatal("zero auth limit accepted")
	}
}
