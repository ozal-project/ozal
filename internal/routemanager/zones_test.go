package routemanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

func zoneConfig(t *testing.T) config.Config {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX absolute-path fixtures")
	}
	cfg := routeConfig("http://127.0.0.1:3000")
	cfg.TLS = config.TLS{Enabled: true, Domains: []string{"*.example.com", "example.com"}, Email: "ops@example.com",
		DNS01: &config.DNS01{StateDir: "/var/lib/ozal/certs", DirectoryURL: "https://acme-v02.api.letsencrypt.org/directory",
			DNSProvider: "builtin", Builtin: &config.DNS01Builtin{Zone: "acme.example.net", Listen: "203.0.113.7:53", Nameservers: []string{"ns1.example.net"}},
			ChallengeDelegation: map[string]string{"example.com": "example-com.acme.example.net"}}}
	cfg.RuntimeFile = filepath.Join(t.TempDir(), "runtime.json")
	operational, err := config.OperatingJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RuntimeFile, operational, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A zone is a runtime edit: it needs the reviewed digest, leaves the host
// configuration untouched and survives a restart.
func TestZonesAreReviewedRuntimeEdits(t *testing.T) {
	path := writeConfig(t, zoneConfig(t))
	root, _ := os.ReadFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := manager.ConfigDigest()
	if err := manager.AddZone("other.dev", ""); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("an unreviewed zone was added: %v", err)
	}
	if err := manager.AddZone("other.dev", reviewed); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddZone("third.dev", reviewed); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("a stale review added a zone: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(root) {
		t.Fatal("the host configuration was changed")
	}
	restarted, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.ServedZoneNames(restarted.Config()); len(got) != 2 || got[1] != "other.dev" {
		t.Fatalf("zones after restart = %v", got)
	}
	if err := restarted.RemoveZone("example.com", restarted.ConfigDigest()); err == nil {
		t.Fatal("a tls.domains zone was removed through the runtime")
	}
	if err := restarted.RemoveZone("other.dev", restarted.ConfigDigest()); err != nil {
		t.Fatal(err)
	}
	if got := config.ServedZoneNames(restarted.Config()); len(got) != 1 {
		t.Fatalf("zones after removal = %v", got)
	}
}

// The install hook sees the first configuration and every edit after it,
// so the certificate set follows zones without a restart.
func TestInstallHookSeesEveryInstalledConfiguration(t *testing.T) {
	path := writeConfig(t, zoneConfig(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen [][]string
	manager, err := New(ctx, path, time.Hour, WithInstallHook(func(cfg config.Config) {
		seen = append(seen, config.ServedZoneNames(cfg))
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddZone("other.dev", manager.ConfigDigest()); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || len(seen[0]) != 1 || len(seen[1]) != 2 || seen[1][1] != "other.dev" {
		t.Fatalf("hook saw %v", seen)
	}
}
