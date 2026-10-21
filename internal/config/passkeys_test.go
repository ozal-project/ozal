package config

import (
	"strings"
	"testing"
)

func passkeyConfig() Config {
	return Config{
		Listen: "127.0.0.1:0",
		Auth: Auth{
			CookieDomain: "example.com",
			Passkeys:     &Passkeys{RPID: "Example.com", LoginURL: "https://auth.example.com/"},
		},
		Routes: []Route{{Host: "app.example.com", Target: "http://127.0.0.1:9000", Access: &AccessPolicy{}}},
	}
}

func TestPasskeysValidateAndNormalize(t *testing.T) {
	source := passkeyConfig()
	cfg, err := Validate(source)
	if err != nil {
		t.Fatal(err)
	}
	pk := cfg.Auth.Passkeys
	if pk.RPID != "example.com" || pk.LoginURL != "https://auth.example.com" || pk.DisplayName != "Switchboard" {
		t.Fatalf("normalized = %+v", *pk)
	}
	if source.Auth.Passkeys.RPID != "Example.com" {
		t.Fatal("Validate rewrote the caller's passkeys settings")
	}

	local := passkeyConfig()
	local.Auth = Auth{Passkeys: &Passkeys{RPID: "localhost", LoginURL: "http://localhost:8888"}}
	local.Routes[0].Host = "localhost:8888"
	if _, err := Validate(local); err != nil {
		t.Fatalf("plain HTTP on localhost refused: %v", err)
	}
}

func TestPasskeysRefuseUnsafeSettings(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"plain HTTP":           {func(c *Config) { c.Auth.Passkeys.LoginURL = "http://auth.example.com" }, "https"},
		"login outside rp_id":  {func(c *Config) { c.Auth.Passkeys.LoginURL = "https://auth.example.net" }, "not rp_id"},
		"suffix trick":         {func(c *Config) { c.Auth.Passkeys.LoginURL = "https://authexample.com" }, "not rp_id"},
		"path in login_url":    {func(c *Config) { c.Auth.Passkeys.LoginURL = "https://auth.example.com/login" }, "origin"},
		"query in login_url":   {func(c *Config) { c.Auth.Passkeys.LoginURL = "https://auth.example.com/?x=1" }, "origin"},
		"IP as rp_id":          {func(c *Config) { c.Auth.Passkeys.RPID = "192.0.2.1" }, "DNS name"},
		"mixed with OIDC":      {func(c *Config) { c.Auth.Issuer = "https://accounts.google.com" }, "cannot be combined"},
		"cookie misses route":  {func(c *Config) { c.Routes[0].Host = "app.example.org" }, "does not cover"},
		"no cookie, two hosts": {func(c *Config) { c.Auth.CookieDomain = "" }, "different host"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := passkeyConfig()
			tc.mutate(&cfg)
			_, err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
