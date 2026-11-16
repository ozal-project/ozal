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
