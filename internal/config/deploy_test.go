package config

import "testing"

func TestDeployEndpointRequiresDedicatedHostAudienceAndSecureListener(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*Config)
		wantErr bool
	}{
		{"local", func(*Config) {}, false},
		{"missing audience", func(c *Config) { c.Pages.Deploy.Audience = "" }, true},
		{"missing host", func(c *Config) { c.Pages.Deploy.Host = "" }, true},
		{"application hostname", func(c *Config) { c.Pages.Deploy.Host = "app.localhost" }, true},
		{"application hostname with port", func(c *Config) { c.Routes[0].Host = "deploy.localhost:8443" }, true},
		{"configured host with port", func(c *Config) { c.Pages.Deploy.Host = "deploy.localhost:8443" }, true},
		{"login hostname", func(c *Config) { c.Auth.RedirectURL = "https://deploy.localhost/_/auth/callback" }, true},
		{"public plaintext", func(c *Config) { c.Listen = ":8888" }, true},
		{"covered TLS", func(c *Config) {
			c.Listen = ":443"
			c.TLS = TLS{Enabled: true, Domains: []string{"deploy.localhost"}, CacheDir: c.Pages.Dir}
		}, false},
		{"TLS missing deploy certificate name", func(c *Config) {
			c.Listen = ":443"
			c.TLS = TLS{Enabled: true, Domains: []string{"app.localhost"}, CacheDir: c.Pages.Dir}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Listen: "127.0.0.1:8888", Pages: &Pages{Dir: t.TempDir(), Deploy: &DeployEndpoint{Host: "DEPLOY.LOCALHOST.", Audience: "https://deploy.localhost"}}, Routes: []Route{{Host: "app.localhost", Target: "http://127.0.0.1:8000"}}}
			tc.change(&cfg)
			got, err := Validate(cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate=%v,wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got.Pages.Deploy.Host != "deploy.localhost" {
				t.Fatalf("hostname not normalized: %q", got.Pages.Deploy.Host)
			}
		})
	}
}
