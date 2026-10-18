package routemanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// A reload must never hand every client a fresh burst: the limiter buckets
// survive while their settings do not change.
func TestReloadKeepsRateLimiters(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := routeConfig(upstream.URL)
	cfg.RateLimit = &config.RateLimit{
		General: config.Limit{Rate: 0.001, Burst: 1},
		Auth:    config.Limit{Rate: 0.001, Burst: 1},
	}
	configPath := writeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, configPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	installed := manager.rateLimiters
	if installed == nil {
		t.Fatal("rate limiters were not installed")
	}

	if code, _ := serve(manager); code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", code)
	}
	if code, _ := serve(manager); code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", code)
	}

	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if manager.rateLimiters != installed {
		t.Fatal("an identical reload replaced the rate limiters")
	}
	if code, _ := serve(manager); code != http.StatusTooManyRequests {
		t.Fatal("the reload reset the client's spent budget")
	}

	// Changed settings build fresh limiters.
	cfg.RateLimit.General.Burst = 5
	overwriteConfig(t, configPath, cfg)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload after change: %v", err)
	}
	if manager.rateLimiters == installed {
		t.Fatal("changed rate_limit settings must build fresh limiters")
	}
	if code, _ := serve(manager); code != http.StatusOK {
		t.Fatalf("request after reconfiguration = %d, want 200 with the new burst", code)
	}

	// Disabling rate limiting drops the limiters entirely.
	cfg.RateLimit = nil
	overwriteConfig(t, configPath, cfg)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload after disabling: %v", err)
	}
	if manager.rateLimiters != nil {
		t.Fatal("disabling rate limiting must drop the installed limiters")
	}
}

func TestRateLimitIdentitySettingsSurviveUnrelatedReloads(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := routeConfig(upstream.URL)
	cfg.RateLimit = &config.RateLimit{
		General:  config.Limit{Rate: 1, Burst: 10},
		Auth:     config.Limit{Rate: 1, Burst: 3},
		Identity: &config.Limit{Rate: 2, Burst: 20},
	}
	configPath := writeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, configPath, time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	installed := manager.rateLimiters
	if installed == nil || installed.identity == nil {
		t.Fatal("the identity limiter was not installed")
	}

	cfg.Routes = append(cfg.Routes, config.Route{Host: "second.localhost", Target: upstream.URL})
	overwriteConfig(t, configPath, cfg)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if manager.rateLimiters != installed || manager.rateLimiters.identity != installed.identity {
		t.Fatal("adding a route replaced the rate limiters")
	}
}
