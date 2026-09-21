package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8888" {
		t.Errorf("Listen = %q, want :8888", cfg.Listen)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("len(Routes) = %d, want 1", len(cfg.Routes))
	}
}

func TestLoadRejectsMissingListen(t *testing.T) {
	path := writeConfig(t, `{"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for missing listen address")
	}
}

func TestLoadRejectsNoRoutes(t *testing.T) {
	path := writeConfig(t, `{"listen": ":8888", "routes": []}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for empty routes")
	}
}

func TestLoadRejectsRouteMissingTarget(t *testing.T) {
	path := writeConfig(t, `{"listen": ":8888", "routes": [{"host": "a.localhost"}]}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for a route missing target")
	}
}

func TestLoadRejectsTLSWithoutDomains(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": {"enabled": true, "cache_dir": "/tmp/cache"},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for tls.enabled without domains")
	}
}

func TestLoadRejectsTLSWithoutCacheDir(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": {"enabled": true, "domains": ["example.com"]},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for tls.enabled without cache_dir")
	}
}

func TestLoadRejectsMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}
