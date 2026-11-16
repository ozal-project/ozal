package cloudflare

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func edge(t *testing.T, routes string) config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(`{"listen": ":443", "tls": {"enabled": true, "cache_dir": "/var/lib/ozal/acme", "email": "ops@example.com", "domains": ["auth.lab.example.org"]},
		"auth": {"passkeys": {"rp_id": "lab.example.org", "login_url": "https://auth.lab.example.org"}},
		"routes": ` + routes + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return *cfg
}

const token = "file:/var/lib/ozal/cloudflare.token"

// Every address served today gets a zone the token reaches: an apex is its
// own zone, anything else its parent, so one wildcard covers siblings.
func TestEnableCoversEveryServedAddress(t *testing.T) {
	cfg := edge(t, `[{"host": "app.lab.example.org", "target": "http://127.0.0.1:3000"},
		{"host": "example.org", "target": "http://127.0.0.1:3001"},
		{"host": "localhost:8888", "target": "http://127.0.0.1:3002"}]`)
	plan, err := PlanEnable(cfg, cfg, token, "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Zones, []string{"example.org", "lab.example.org"}) {
		t.Fatalf("zones = %v", plan.Zones)
	}
	if plan.Covers["auth.lab.example.org"] != "lab.example.org" || plan.Covers["app.lab.example.org"] != "lab.example.org" || plan.Covers["example.org"] != "example.org" {
		t.Fatalf("covers = %v", plan.Covers)
	}
	if !slices.Equal(plan.Local, []string{"localhost"}) {
		t.Fatalf("local = %v", plan.Local)
	}
	got := plan.Host
	if got.TLS.DNS01 == nil || got.TLS.DNS01.DNSToken != token || got.TLS.DNS01.DirectoryURL != LetsEncrypt || got.TLS.DNS01.StateDir != "/var/lib/ozal/dns01" {
		t.Fatalf("dns01 = %+v", got.TLS.DNS01)
	}
	if got.DNS == nil || got.DNS.Cloudflare == nil || got.DNS.Cloudflare.Token != token || got.DNS.Target != "203.0.113.10" {
		t.Fatalf("dns = %+v", got.DNS)
	}
	if strings.Contains(strings.Join(got.TLS.Domains, ","), "auth.") {
		t.Fatalf("hostnames stayed in tls.domains: %v", got.TLS.Domains)
	}
}

// An address whose zone the token cannot reach would lose its certificate:
// the plan is refused and names it.
func TestEnableRefusesToStrandAnAddress(t *testing.T) {
	cfg := edge(t, `[{"host": "app.lab.example.org", "target": "http://127.0.0.1:3000"},
		{"host": "shop.example.com", "target": "http://127.0.0.1:3001"}]`)
	_, err := PlanEnable(cfg, cfg, token, "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org"})
	if err == nil || !strings.Contains(err.Error(), "shop.example.com") || !strings.Contains(err.Error(), "lose their certificate") {
		t.Fatalf("err = %v", err)
	}
}

// The runtime's routes count: a host file without routes still has to cover
// what the runtime serves.
func TestEnableChecksTheRoutesInEffect(t *testing.T) {
	merged := edge(t, `[{"host": "shop.example.com", "target": "http://127.0.0.1:3001"}]`)
	host := edge(t, `[{"host": "localhost:8888", "target": "http://127.0.0.1:3002"}]`)
	if _, err := PlanEnable(host, merged, token, "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org"}); err == nil || !strings.Contains(err.Error(), "shop.example.com") {
		t.Fatalf("a runtime route was not checked: %v", err)
	}
	plan, err := PlanEnable(host, merged, token, "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org", "example.com"})
	if err != nil || len(plan.Host.Routes) != 1 {
		t.Fatalf("the host file gained routes or failed: %v %v", plan.Host.Routes, err)
	}
}

func TestEnableRefusals(t *testing.T) {
	plain, err := config.Parse([]byte(`{"listen": ":8888", "routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanEnable(*plain, *plain, token, "/d", "203.0.113.10", []string{"example.org"}); err == nil || !strings.Contains(err.Error(), "plain HTTP") {
		t.Fatalf("plain HTTP: %v", err)
	}
	cfg := edge(t, `[{"host": "app.lab.example.org", "target": "http://127.0.0.1:3000"}]`)
	plan, err := PlanEnable(cfg, cfg, token, "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanEnable(plan.Host, plan.Host, token, "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org"}); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("second enable: %v", err)
	}
	if _, err := PlanEnable(plan.Host, plan.Host, "file:/other", "/var/lib/ozal/dns01", "203.0.113.10", []string{"example.org"}); err == nil || errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("another DNS-01 setup was overwritten: %v", err)
	}
	if _, err := PlanEnable(cfg, cfg, token, "/d", "", []string{"example.org"}); err == nil || !strings.Contains(err.Error(), "dns.target") {
		t.Fatalf("no target: %v", err)
	}
	noEmail := cfg
	noEmail.TLS.Email = ""
	if _, err := PlanEnable(noEmail, noEmail, token, "/d", "203.0.113.10", []string{"example.org"}); err == nil || !strings.Contains(err.Error(), "tls.email") {
		t.Fatalf("no email: %v", err)
	}
}
