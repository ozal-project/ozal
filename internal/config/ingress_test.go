package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func tunnelConfig() Config {
	return Config{
		Listen:  "127.0.0.1:8888",
		Auth:    Auth{CookieDomain: "example.com", Passkeys: &Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com", DisplayName: "Ozal"}},
		Ingress: &Ingress{Mode: IngressCloudflareTunnel, CloudflareTunnel: &CloudflareTunnelIngress{Token: "file:/etc/ozal/cloudflare.token"}},
		DNS:     &DNS{Proxied: true, Cloudflare: &DNSCloudflare{Token: "file:/etc/ozal/cloudflare.token"}},
		Routes:  []Route{{Host: "notes.example.com", Target: "http://127.0.0.1:3000", Access: &AccessPolicy{RequiredGroups: []string{"members"}}}},
	}
}

func TestCloudflareTunnelIngressDefaultsAndTrustsOnlyLoopback(t *testing.T) {
	cfg := tunnelConfig()
	cfg.TrustedProxies = []string{"127.0.0.1"}
	got, err := Validate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.IngressMode() != IngressCloudflareTunnel {
		t.Fatalf("mode = %q", got.IngressMode())
	}
	tunnel := got.Ingress.CloudflareTunnel
	if tunnel.StateDir != DefaultCloudflareTunnelStateDir || tunnel.Name != "ozal" {
		t.Fatalf("defaults = %+v", tunnel)
	}
	if want := []string{"127.0.0.1/32", "::1/128"}; !slices.Equal(got.TrustedProxies, want) {
		t.Fatalf("trusted_proxies = %v, want %v", got.TrustedProxies, want)
	}
	if cfg.Ingress.CloudflareTunnel.Name != "" {
		t.Fatal("Validate changed its input")
	}
}

func TestCloudflareTunnelIngressRejectsUnsafeShapes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"public listener", func(c *Config) { c.Listen = ":8888" }, "listen must be a loopback"},
		{"LAN listener", func(c *Config) { c.Listen = "192.168.1.10:8888" }, "listen must be a loopback"},
		{"own TLS", func(c *Config) {
			c.TLS.Enabled = true
			c.TLS.Domains = []string{"auth.example.com"}
			c.TLS.CacheDir = "/var/lib/ozal/acme"
		}, "tls.enabled false"},
		{"remote trusted proxy", func(c *Config) { c.TrustedProxies = []string{"173.245.48.0/20"} }, "only loopback"},
		{"fixed dns target", func(c *Config) { c.DNS.Target = "203.0.113.10" }, "dns.target must be empty"},
		{"plain-HTTP login", func(c *Config) { c.Auth.Passkeys.LoginURL = "http://auth.example.com" }, "login_url must be https"},
		{"missing token", func(c *Config) { c.Ingress.CloudflareTunnel.Token = "" }, "token is required"},
		{"inline token", func(c *Config) { c.Ingress.CloudflareTunnel.Token = "abc123" }, "ingress.cloudflare_tunnel.token"},
		{"relative state dir", func(c *Config) { c.Ingress.CloudflareTunnel.StateDir = "state" }, "absolute, clean path"},
		{"bad name", func(c *Config) { c.Ingress.CloudflareTunnel.Name = "my tunnel" }, "name must be"},
		{"missing block", func(c *Config) { c.Ingress.CloudflareTunnel = nil }, "requires ingress.cloudflare_tunnel"},
		{"unknown mode", func(c *Config) { c.Ingress.Mode = "ngrok" }, "ingress.mode must be"},
		{"tunnel block in direct mode", func(c *Config) { c.Ingress.Mode = IngressDirect }, "requires ingress.mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tunnelConfig()
			tc.mutate(&cfg)
			_, err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestDirectIngressIsTheDefaultAndUnchanged(t *testing.T) {
	cfg := Config{Listen: ":8888", TrustedProxies: []string{"173.245.48.0/20"}, DNS: &DNS{Target: "203.0.113.10"}, Routes: []Route{{Host: "app.localhost", Target: "http://127.0.0.1:3000"}}}
	got, err := Validate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.IngressMode() != IngressDirect || got.Ingress != nil {
		t.Fatalf("mode = %q, ingress = %+v", got.IngressMode(), got.Ingress)
	}
	if !slices.Equal(got.TrustedProxies, []string{"173.245.48.0/20"}) {
		t.Fatalf("trusted_proxies changed: %v", got.TrustedProxies)
	}
	cfg.DNS = &DNS{Cloudflare: &DNSCloudflare{Token: "file:/etc/ozal/dns.token"}}
	if _, err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "requires dns.target") {
		t.Fatalf("direct mode accepted a provider without a target: %v", err)
	}
}

func TestCloudflareTunnelTarget(t *testing.T) {
	if got := CloudflareTunnelTarget("C0FFEE00-0000-4000-8000-000000000001"); got != "c0ffee00-0000-4000-8000-000000000001.cfargotunnel.com" {
		t.Fatalf("target = %q", got)
	}
}

func TestCloudflareTunnelExampleIsValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.cloudflare-tunnel.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IngressMode() != IngressCloudflareTunnel || cfg.TLS.Enabled || !slices.Equal(cfg.TrustedProxies, []string{"127.0.0.1/32", "::1/128"}) {
		t.Fatalf("example = mode %s, tls %v, proxies %v", cfg.IngressMode(), cfg.TLS.Enabled, cfg.TrustedProxies)
	}
}
