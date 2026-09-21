package routemanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func writeConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
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
	manager, err := New(ctx, writeConfig(t, routeConfig(oldUpstream.URL)), time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if status, body := serve(manager); status != http.StatusOK || body != "old" {
		t.Fatalf("initial response = (%d, %q), want (200, old)", status, body)
	}

	if err := manager.Reload(writeConfig(t, routeConfig(newUpstream.URL))); err != nil {
		t.Fatalf("Reload valid config: %v", err)
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("reloaded response = (%d, %q), want (200, new)", status, body)
	}

	invalid := routeConfig("ftp://invalid.example")
	if err := manager.Reload(writeConfig(t, invalid)); err == nil {
		t.Fatal("Reload invalid config returned nil error")
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("response after failed reload = (%d, %q), want last-good (200, new)", status, body)
	}

	changedListener := routeConfig(oldUpstream.URL)
	changedListener.Listen = ":9999"
	if err := manager.Reload(writeConfig(t, changedListener)); err == nil {
		t.Fatal("Reload with changed listener returned nil error")
	}
	if status, body := serve(manager); status != http.StatusOK || body != "new" {
		t.Fatalf("response after rejected listener change = (%d, %q), want (200, new)", status, body)
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
	manager, err := New(ctx, writeConfig(t, routeConfig(oldUpstream.URL)), time.Hour)
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
	if err := manager.Reload(writeConfig(t, routeConfig(newUpstream.URL))); err != nil {
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
