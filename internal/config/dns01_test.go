package config

import (
	"strings"
	"testing"
)

func dns01Config(t *testing.T, tlsJSON string) *Config {
	t.Helper()
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": `+tlsJSON+`,
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestDNS01ConfigAcceptsTheBoundedScope(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["Preview.Example.COM"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/switchboard/certs/",
			"directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
			"certificate_roots_file": "/etc/switchboard/./staging-roots.crt",
			"dns_token": "env:CLOUDFLARE_DNS_TOKEN"
		}
	}`)
	if cfg.TLS.DNS01 == nil {
		t.Fatal("tls.dns01 was dropped")
	}
	if cfg.TLS.Domains[0] != "preview.example.com" {
		t.Fatalf("domain = %q, want normalized", cfg.TLS.Domains[0])
	}
	if cfg.TLS.DNS01.StateDir != "/var/lib/switchboard/certs" {
		t.Fatalf("state dir = %q, want cleaned absolute path", cfg.TLS.DNS01.StateDir)
	}
	if cfg.TLS.DNS01.DNSToken != "env:CLOUDFLARE_DNS_TOKEN" {
		t.Fatalf("token reference = %q", cfg.TLS.DNS01.DNSToken)
	}
	if cfg.TLS.DNS01.CertificateRootsFile != "/etc/switchboard/staging-roots.crt" {
		t.Fatal("certificate roots path was not normalized")
	}
	// Clone must not share the pointer.
	clone := Clone(*cfg)
	clone.TLS.DNS01.StateDir = "/elsewhere"
	if cfg.TLS.DNS01.StateDir == "/elsewhere" {
		t.Fatal("Clone shares the dns01 pointer")
	}
}

func TestDNS01ConfigRefusals(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	valid := func() string {
		return `{
			"enabled": true,
			"domains": ["preview.example.com"],
			"email": "ops@example.com",
			"dns01": {
				"state_dir": "/var/lib/switchboard/certs",
				"directory_url": "https://acme.example/directory",
				"dns_token": "env:TOKEN"
			}
		}`
	}
	tests := []struct {
		name   string
		mutate func(*TLS)
		want   string
	}{
		{"disabled tls", func(tls *TLS) { tls.Enabled = false }, "requires tls.enabled"},
		{"no domain", func(tls *TLS) { tls.Domains = nil }, "at least one domain"},
		{"five domains", func(tls *TLS) {
			tls.Domains = []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com", "e.example.com"}
		}, "1 to 4 domains"},
		{"repeated domain", func(tls *TLS) { tls.Domains = append(tls.Domains, "PREVIEW.example.com") }, "twice"},
		{"name the wildcard covers", func(tls *TLS) { tls.Domains = []string{"*.example.com", "preview.example.com"} }, "already covered"},
		{"bad second name", func(tls *TLS) { tls.Domains = append(tls.Domains, "*.com") }, "tls.domains[1]"},
		{"missing state dir", func(tls *TLS) { tls.DNS01.StateDir = "" }, "requires state_dir"},
		{"relative state dir", func(tls *TLS) { tls.DNS01.StateDir = "certs" }, "absolute path"},
		{"relative certificate roots", func(tls *TLS) { tls.DNS01.CertificateRootsFile = "roots.crt" }, "absolute path"},
		{"missing email", func(tls *TLS) { tls.Email = "" }, "requires tls.email"},
		{"missing directory", func(tls *TLS) { tls.DNS01.DirectoryURL = "" }, "requires directory_url"},
		{"http directory", func(tls *TLS) { tls.DNS01.DirectoryURL = "http://acme.example/directory" }, "https URL"},
		{"missing token", func(tls *TLS) { tls.DNS01.DNSToken = " " }, "requires dns_token"},
		{"empty file reference", func(tls *TLS) { tls.DNS01.DNSToken = "file: " }, "names no file"},
		{"hostname with port", func(tls *TLS) { tls.Domains[0] = "preview.example.com:443" }, "plain hostname"},
		{"wildcard over a public suffix", func(tls *TLS) { tls.Domains[0] = "*.com" }, "registered domain"},
		{"wildcard not at the left", func(tls *TLS) { tls.Domains[0] = "a.*.example.com" }, "plain hostname"},
		{"wildcard without a dot", func(tls *TLS) { tls.Domains[0] = "*example.com" }, "plain hostname"},
		{"double wildcard", func(tls *TLS) { tls.Domains[0] = "*.*.example.com" }, "plain hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `{
				"listen": ":8888",
				"tls": `+valid()+`,
				"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
			}`)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("baseline config must load: %v", err)
			}
			tt.mutate(&cfg.TLS)
			if _, err := Validate(*cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestDNS01ConfigDoesNotRequireAutocertCache(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["preview.example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/switchboard/certs",
			"directory_url": "https://acme.example/directory",
			"dns_token": "file:/etc/switchboard/dns-token"
		}
	}`)
	if cfg.TLS.CacheDir != "" {
		t.Fatalf("cache_dir = %q, want it unused in dns-01 mode", cfg.TLS.CacheDir)
	}
}

func TestDNS01ConfigAcceptsASingleLevelWildcard(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["*.Example.COM"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/switchboard/certs",
			"directory_url": "https://acme.example/directory",
			"dns_token": "env:TOKEN"
		}
	}`)
	if got := cfg.TLS.Domains[0]; got != "*.example.com" {
		t.Fatalf("domain = %q, want the wildcard normalized", got)
	}
}

// A wildcard does not cover its base, so the apex rides beside it as a second
// name on the same certificate; the wildcard stays first, its identity.
func TestDNS01ConfigAcceptsAWildcardAndItsApex(t *testing.T) {
	skipOnWindows(t, "POSIX absolute-path fixtures")
	cfg := dns01Config(t, `{
		"enabled": true,
		"domains": ["*.example.com", "Example.com"],
		"email": "ops@example.com",
		"dns01": {
			"state_dir": "/var/lib/switchboard/certs",
			"directory_url": "https://acme.example/directory",
			"dns_token": "env:TOKEN"
		}
	}`)
	if got, want := strings.Join(cfg.TLS.Domains, ","), "*.example.com,example.com"; got != want {
		t.Fatalf("domains = %q, want %q", got, want)
	}
}

// autocert cannot answer a wildcard: only the DNS-01 manager can, so a
// wildcard without tls.dns01 must fail at load time, not at the first handshake.
func TestWildcardRequiresDNS01(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"tls": {"enabled": true, "domains": ["*.example.com"], "email": "ops@example.com", "cache_dir": "/var/lib/switchboard/acme"},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("Load error = %v, want it to refuse a wildcard without tls.dns01", err)
	}
}

func TestSitesTokenMustDifferFromTheAdminToken(t *testing.T) {
	path := writeConfig(t, `{
		"listen": ":8888",
		"admin": {"token": "env:SB_ADMIN", "sites_token": "env:SB_ADMIN"},
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "sites_token") {
		t.Fatalf("Load error = %v, want sites_token refused", err)
	}
}
