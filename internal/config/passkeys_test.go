package config

import (
	"slices"
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
		"partial OIDC beside":  {func(c *Config) { c.Auth.Issuer = "https://accounts.google.com" }, "needs issuer"},
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

// Step-up needs a login Switchboard itself performs: an upstream provider's
// single sign-on would answer without a fresh proof.
func TestMaxAuthAgeValidation(t *testing.T) {
	ok := passkeyConfig()
	ok.Routes[0].Access.MaxAuthAgeMinutes = 15
	if _, err := Validate(ok); err != nil {
		t.Fatalf("passkeys + max_auth_age refused: %v", err)
	}
	negative := passkeyConfig()
	negative.Routes[0].Access.MaxAuthAgeMinutes = -1
	if _, err := Validate(negative); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative max_auth_age: %v", err)
	}
	oidc := Config{
		Listen: "127.0.0.1:0",
		Auth:   Auth{Issuer: "https://accounts.google.com", ClientID: "id", ClientSecret: "secret", RedirectURL: "http://app.localhost/_/auth/callback"},
		Routes: []Route{{Host: "app.localhost", Target: "http://127.0.0.1:9000", Access: &AccessPolicy{MaxAuthAgeMinutes: 15}}},
	}
	if _, err := Validate(oidc); err == nil || !strings.Contains(err.Error(), "requires auth.passkeys") {
		t.Fatalf("OIDC + max_auth_age: %v", err)
	}
	oidc.Routes[0].Access = &AccessPolicy{RequireDeviceBound: true}
	if _, err := Validate(oidc); err == nil || !strings.Contains(err.Error(), "require_device_bound requires auth.passkeys") {
		t.Fatalf("OIDC + require_device_bound: %v", err)
	}
}

// ID-007: an upstream provider next to passkeys is all or nothing, calls back
// on the login host, and access.methods names only configured methods.
func TestPasskeysWithOIDCAndMethods(t *testing.T) {
	combined := func() Config {
		c := passkeyConfig()
		c.Auth.Issuer = "https://accounts.google.com"
		c.Auth.ClientID = "id"
		c.Auth.ClientSecret = "secret"
		c.Auth.RedirectURL = "https://auth.example.com/_/auth/callback"
		return c
	}
	cfg, err := Validate(combined())
	if err != nil {
		t.Fatalf("passkeys + OIDC refused: %v", err)
	}
	if cfg.Auth.ProviderName != "single sign-on" || !slices.Equal(cfg.Auth.LoginMethods(), []string{MethodPasskey, MethodOIDC}) {
		t.Fatalf("provider %q, methods %v", cfg.Auth.ProviderName, cfg.Auth.LoginMethods())
	}
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"callback elsewhere":      {func(c *Config) { c.Auth.RedirectURL = "https://app.example.com/_/auth/callback" }, "must be"},
		"provider name, no OIDC":  {func(c *Config) { *c = passkeyConfig(); c.Auth.ProviderName = "Google" }, "none is configured"},
		"method not configured":   {func(c *Config) { *c = passkeyConfig(); c.Routes[0].Access.Methods = []string{"oidc"} }, "not a configured login method"},
		"unknown method":          {func(c *Config) { c.Routes[0].Access.Methods = []string{"sms"} }, "not a configured login method"},
		"secret as literal value": {func(c *Config) { c.Auth.ClientSecret = "" }, "needs issuer"},
	} {
		t.Run(name, func(t *testing.T) {
			c := combined()
			tc.mutate(&c)
			if _, err := Validate(c); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	stepUpWithOIDC := combined()
	stepUpWithOIDC.Routes[0].Access = &AccessPolicy{MaxAuthAgeMinutes: 15, Methods: []string{"passkey", "oidc"}}
	if _, err := Validate(stepUpWithOIDC); err == nil || !strings.Contains(err.Error(), "need a passkey login") {
		t.Fatalf("max_auth_age + methods with oidc: %v", err)
	}
	ok := combined()
	ok.Routes[0].Access.Methods = []string{"passkey"}
	if _, err := Validate(ok); err != nil {
		t.Fatalf("methods [passkey] refused: %v", err)
	}
}
