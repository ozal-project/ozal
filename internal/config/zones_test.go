package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeZoneName(t *testing.T) {
	for raw, want := range map[string]string{
		"Example.COM.":    "example.com",
		" lab.example.io": "lab.example.io",
		"example.com":     "example.com",
		"*.example.com":   "",
		"example.com:443": "",
		"com":             "",
		"localhost":       "",
		"app.localhost":   "",
		"192.0.2.1":       "",
		"[2001:db8::1]":   "",
		"exa mple.com":    "",
		"":                "",
	} {
		got, err := NormalizeZoneName(raw)
		if (err == nil) != (want != "") || got != want {
			t.Errorf("NormalizeZoneName(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestServedZonesComeFromDomainsAndRuntime(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, builtinTLS(nil))
	if got := ServedZones(*cfg); len(got) != 1 || got[0] != (ServedZone{"example.com", ZoneFromConfig}) {
		t.Fatalf("zones from tls.domains = %v, want one example.com (wildcard and apex share it)", got)
	}
	next, err := WithZone(*cfg, "Other.DEV")
	if err != nil {
		t.Fatal(err)
	}
	want := []ServedZone{{"example.com", ZoneFromConfig}, {"other.dev", ZoneFromRuntime}}
	if got := ServedZones(next); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("zones = %v, want %v", got, want)
	}
	if len(cfg.Zones) != 0 {
		t.Fatal("WithZone changed its input")
	}
	if _, err := WithZone(next, "other.dev"); err == nil {
		t.Fatal("a served zone was added twice")
	}
	if _, err := WithZone(next, "example.com"); err == nil {
		t.Fatal("a tls.domains zone was added to the runtime")
	}
}

func TestChallengeForZones(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	builtin := dns01Config(t, builtinTLS(nil)).TLS
	// The explicit delegation wins; a new zone gets the builtin default.
	if got := ChallengeFor(builtin, "example.com"); got != (ZoneChallenge{"_acme-challenge.example.com", "example-com.acme.example.net", true}) {
		t.Fatalf("explicit delegation = %+v", got)
	}
	if got := ChallengeFor(builtin, "other.dev"); got != (ZoneChallenge{"_acme-challenge.other.dev", "other.dev.acme.example.net", true}) {
		t.Fatalf("builtin default = %+v", got)
	}
	cloudflare := dns01Config(t, `{"enabled": true, "domains": ["example.com"], "email": "ops@example.com",
		"dns01": {"state_dir": "/var/lib/ozal/certs", "directory_url": "https://acme-v02.api.letsencrypt.org/directory", "dns_token": "env:T"}}`).TLS
	if got := ChallengeFor(cloudflare, "other.dev"); got.Delegated || got.Target != "_acme-challenge.other.dev" {
		t.Fatalf("provider-written challenge = %+v", got)
	}
}

func TestZonesNeedAnEdgeThatOwnsItsCertificates(t *testing.T) {
	cfg := Config{Listen: ":8888", Zones: []Zone{{Name: "example.com"}},
		Routes: []Route{{Host: "a.localhost", Target: "http://localhost:9000"}}}
	if _, err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "tls.dns01") {
		t.Fatalf("zones without tls.dns01: %v", err)
	}
}

func TestZoneOutsideASingleZoneProviderIsRefused(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{"enabled": true, "domains": ["example.com"], "email": "ops@example.com",
		"dns01": {"state_dir": "/var/lib/ozal/certs", "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
		"dns_provider": "hetzner", "hetzner": {"zone": "example.com", "token": "env:H"}}}`)
	if _, err := WithZone(*cfg, "lab.example.com"); err != nil {
		t.Fatalf("a zone inside the provider's zone: %v", err)
	}
	if _, err := WithZone(*cfg, "other.dev"); err == nil || !strings.Contains(err.Error(), "challenge_delegation") {
		t.Fatalf("a zone the provider cannot write: %v", err)
	}
}

func TestWithoutZoneProtectsLiveApplications(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, builtinTLS(nil))
	next, err := WithZone(*cfg, "other.dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithoutZone(next, "example.com"); err == nil || !strings.Contains(err.Error(), "tls.domains") {
		t.Fatalf("a host-config zone was removed through the runtime: %v", err)
	}
	withApp, err := WithApplication(next, Route{Host: "www.other.dev", Target: "http://127.0.0.1:9000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithoutZone(withApp, "other.dev"); err == nil || !strings.Contains(err.Error(), "www.other.dev") {
		t.Fatalf("a zone serving an application was removed: %v", err)
	}
	removed, err := WithoutZone(next, "other.dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Zones) != 0 {
		t.Fatalf("zones = %v", removed.Zones)
	}
}

// Zones live in the runtime file beside routes; a runtime file without
// zones keeps its old shape so a paired rollback can still read it.
func TestZonesRoundTripThroughTheRuntimeFile(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, builtinTLS(nil))
	plain, err := OperatingJSON(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "zones") {
		t.Fatalf("empty zones were written: %s", plain)
	}
	next, err := WithZone(*cfg, "other.dev")
	if err != nil {
		t.Fatal(err)
	}
	data, err := OperatingJSON(next)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(runtime, data, 0o600); err != nil {
		t.Fatal(err)
	}
	base := Clone(*cfg)
	base.RuntimeFile = runtime
	loaded, _, err := loadOperating(&base, []byte("root"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Zones) != 1 || loaded.Zones[0].Name != "other.dev" {
		t.Fatalf("zones after reload = %v", loaded.Zones)
	}
}
