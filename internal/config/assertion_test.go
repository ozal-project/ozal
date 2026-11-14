package config

import (
	"strings"
	"testing"
)

func assertionConfig() Config {
	return Config{
		Listen:            ":8888",
		Auth:              Auth{CookieDomain: "example.com", Passkeys: &Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com", DisplayName: "Ozal"}},
		IdentityAssertion: &IdentityAssertion{KeyDir: "/var/lib/ozal/assertion"},
		Routes: []Route{{Host: "custodia.example.com", Target: "http://127.0.0.1:8090",
			Access: &AccessPolicy{RequiredGroups: []string{"members"}}, IdentityAssertion: &RouteAssertion{Audience: "custodia"}}},
	}
}

func TestIdentityAssertionConfig(t *testing.T) {
	cfg, err := Validate(assertionConfig())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AssertionIssuer() != "https://auth.example.com" || cfg.Routes[0].IdentityAssertion.Audience != "custodia" {
		t.Fatalf("issuer %q, route %+v", cfg.AssertionIssuer(), cfg.Routes[0].IdentityAssertion)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"public route", func(c *Config) { c.Routes[0].Access = nil }, "public route has no person"},
		{"static route", func(c *Config) { c.Routes[0].Target = ""; c.Routes[0].Root = "/srv/www" }, "needs target or upstreams"},
		{"bad audience", func(c *Config) { c.Routes[0].IdentityAssertion.Audience = "Custodia App" }, "audience must be"},
		{"empty audience", func(c *Config) { c.Routes[0].IdentityAssertion.Audience = "" }, "audience must be"},
		{"no key dir", func(c *Config) { c.IdentityAssertion = nil }, "key_dir"},
		{"relative key dir", func(c *Config) { c.IdentityAssertion.KeyDir = "keys" }, "absolute, clean path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := assertionConfig()
			tc.mutate(&c)
			if _, err := Validate(c); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCloudflareTunnelWithIdentityAssertion(t *testing.T) {
	cfg := tunnelConfig()
	cfg.IdentityAssertion = &IdentityAssertion{KeyDir: "/var/lib/ozal/assertion"}
	cfg.Routes[0].IdentityAssertion = &RouteAssertion{Audience: "custodia"}
	got, err := Validate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.IngressMode() != IngressCloudflareTunnel || got.AssertionIssuer() != "https://auth.example.com" {
		t.Fatalf("mode %q, issuer %q", got.IngressMode(), got.AssertionIssuer())
	}
	// Reloads clone both settings; changing one snapshot must not alter another.
	clone := Clone(*got)
	clone.Ingress.CloudflareTunnel.Name = "another-edge"
	clone.IdentityAssertion.KeyDir = "/var/lib/another-edge/assertion"
	clone.Routes[0].IdentityAssertion.Audience = "another-app"
	if got.Ingress.CloudflareTunnel.Name != "ozal" || got.IdentityAssertion.KeyDir != cfg.IdentityAssertion.KeyDir || got.Routes[0].IdentityAssertion.Audience != "custodia" {
		t.Fatal("cloning shared tunnel or assertion settings")
	}
	// Tunnel mode must still enforce HTTPS for the assertion's login origin.
	cfg.Auth.Passkeys.LoginURL = "http://auth.example.com"
	if _, err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "login_url must be https") {
		t.Fatalf("insecure tunnel login accepted: %v", err)
	}
}
