package config

import (
	"strings"
	"testing"
)

func remoteConsoleConfig() Config {
	cfg := passkeyConfig()
	cfg.TLS = TLS{Enabled: true, Domains: []string{"app.example.com", "auth.example.com", "console.example.com"}, CacheDir: "/var/lib/ozal/certs"}
	cfg.Admin.LoginHost = true
	cfg.Admin.Remote = &AdminRemote{Host: "Console.Example.com."}
	return cfg
}

func TestAdminRemoteValidatesAndNormalizes(t *testing.T) {
	source := remoteConsoleConfig()
	cfg, err := Validate(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RemoteConsoleHost(); got != "console.example.com" {
		t.Fatalf("host = %q", got)
	}
	if source.Admin.Remote.Host != "Console.Example.com." {
		t.Fatal("Validate rewrote the caller's admin.remote")
	}
	if (Config{}).RemoteConsoleHost() != "" {
		t.Fatal("no admin.remote must mean no remote console")
	}
}

// The remote console is refused in every shape that would make it weaker
// than the loopback console: plain HTTP to the visitor, a sign-in that is not
// an administrator passkey on the HTTPS login host, or a shared hostname.
func TestAdminRemoteRefusesUnsafeSettings(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"no login host":     {func(c *Config) { c.Admin.LoginHost = false }, "admin.login_host"},
		"no passkeys":       {func(c *Config) { c.Auth.Passkeys = nil; c.Routes[0].Access = nil; c.Admin.LoginHost = false }, "admin.login_host"},
		"no https":          {func(c *Config) { c.TLS = TLS{} }, "HTTPS"},
		"empty host":        {func(c *Config) { c.Admin.Remote.Host = "" }, "dedicated hostname"},
		"ip address":        {func(c *Config) { c.Admin.Remote.Host = "192.0.2.10" }, "dedicated hostname"},
		"port":              {func(c *Config) { c.Admin.Remote.Host = "console.example.com:8443" }, "dedicated hostname"},
		"single label":      {func(c *Config) { c.Admin.Remote.Host = "console" }, "dedicated hostname"},
		"login host":        {func(c *Config) { c.Admin.Remote.Host = "auth.example.com" }, "login host"},
		"application route": {func(c *Config) { c.Admin.Remote.Host = "app.example.com" }, "application route"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := remoteConsoleConfig()
			tc.mutate(&cfg)
			_, err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Behind a Cloudflare Tunnel the visitor's HTTPS is Cloudflare's, decided by
// the trust stage; the remote console is allowed there.
func TestAdminRemoteAllowsTunnelIngress(t *testing.T) {
	cfg := remoteConsoleConfig()
	cfg.TLS = TLS{}
	cfg.Ingress = &Ingress{Mode: IngressCloudflareTunnel, CloudflareTunnel: &CloudflareTunnelIngress{Token: "file:/etc/ozal/cloudflare.token"}}
	cfg.TrustedProxies = []string{"127.0.0.1/32", "::1/128"}
	if _, err := Validate(cfg); err != nil {
		t.Fatalf("tunnel ingress refused: %v", err)
	}
}
