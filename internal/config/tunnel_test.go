package config

import (
	"path/filepath"
	"testing"
)

func TestTunnelRouteValidationFailsClosed(t *testing.T) {
	directory := t.TempDir()
	baseline := Config{Listen: "127.0.0.1:0", Tunnel: &Tunnel{Listen: "127.0.0.1:0", CertFile: filepath.Join(directory, "edge.crt"), KeyFile: filepath.Join(directory, "edge.key"), CAFile: filepath.Join(directory, "ca.crt")}, Routes: []Route{{Host: "APP.localhost.", Connector: "portfolio"}}}
	baseline.Auth = Auth{Issuer: "https://idp.example", ClientID: "client", ClientSecret: "env:SB_TEST_SECRET", RedirectURL: "http://app.localhost/_/auth/callback"}
	cfg, err := Validate(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routes[0].Host != "app.localhost" || cfg.Routes[0].Destination() != "connector:portfolio" {
		t.Fatalf("normalization: %+v", cfg.Routes[0])
	}
	for name, mutate := range map[string]func(*Config){
		"no adapter":                     func(c *Config) { c.Tunnel = nil },
		"two destinations":               func(c *Config) { c.Routes[0].Target = "http://localhost:8000" },
		"bad connector":                  func(c *Config) { c.Routes[0].Connector = "../other" },
		"protected":                      func(c *Config) { c.Routes[0].Access = &AccessPolicy{} },
		"http probe":                     func(c *Config) { c.Routes[0].HealthPath = "/health" },
		"SPA":                            func(c *Config) { c.Routes[0].SPA = true },
		"relative key":                   func(c *Config) { c.Tunnel.KeyFile = "edge.key" },
		"public bind":                    func(c *Config) { c.Tunnel.Listen = "0.0.0.0:9443" },
		"private bind without allowlist": func(c *Config) { c.Tunnel.Listen = "10.8.0.1:9443" },
		"public subnet":                  func(c *Config) { c.Tunnel.Network = "0.0.0.0/0" },
		"partly public subnet":           func(c *Config) { c.Tunnel.Network = "10.0.0.0/7" },
		"outside allowlist":              func(c *Config) { c.Tunnel.Network = "10.8.0.0/24"; c.Tunnel.Listen = "10.9.0.1:9443" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := Clone(baseline)
			mutate(&candidate)
			if _, err := Validate(candidate); err == nil {
				t.Fatal("unsafe tunnel config accepted")
			}
		})
	}
	allowed := Clone(baseline)
	allowed.Tunnel.Network = "10.8.0.0/24"
	allowed.Tunnel.Listen = "10.8.0.1:9443"
	if _, err := Validate(allowed); err != nil {
		t.Fatal(err)
	}
	clone := Clone(baseline)
	clone.Tunnel.Listen = "127.0.0.1:1234"
	if baseline.Tunnel.Listen != "127.0.0.1:0" {
		t.Fatal("clone mutated source")
	}
}
