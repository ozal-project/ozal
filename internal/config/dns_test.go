package config

import (
	"strings"
	"testing"
)

func TestDNSSectionValidation(t *testing.T) {
	base := func(dns *DNS) Config {
		return Config{Listen: "127.0.0.1:8888", DNS: dns, Routes: []Route{{Host: "app.localhost", Target: "http://127.0.0.1:3000"}}}
	}
	for _, tc := range []struct {
		dns     *DNS
		want    string // normalized target, or "" when it must fail
		failure string
	}{
		{&DNS{Target: "203.0.113.10"}, "203.0.113.10", ""},
		{&DNS{Target: "Edge.Example.NET."}, "edge.example.net", ""},
		{&DNS{Target: "2001:DB8::1", Proxied: true}, "2001:db8::1", ""},
		{&DNS{Target: "edge.example.net:443"}, "", "dns.target"},
		{&DNS{Target: "0.0.0.0"}, "", "dns.target"},
		{&DNS{Target: "*.example.net"}, "", "dns.target"},
		{&DNS{Cloudflare: &DNSCloudflare{Token: "file:/run/cf"}}, "", "requires dns.target"},
		{&DNS{Target: "203.0.113.10", Cloudflare: &DNSCloudflare{}}, "", "token is required"},
		{&DNS{Target: "203.0.113.10", Cloudflare: &DNSCloudflare{Token: "file:"}}, "", "names no file"},
		{&DNS{Target: "203.0.113.10", Cloudflare: &DNSCloudflare{Token: "env:CF_DNS_TOKEN"}}, "203.0.113.10", ""},
	} {
		got, err := Validate(base(tc.dns))
		if tc.failure != "" {
			if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Errorf("%+v: err = %v, want %q", tc.dns, err, tc.failure)
			}
			continue
		}
		if err != nil || got.DNS.Target != tc.want {
			t.Errorf("%+v: %v, target %q", tc.dns, err, got.DNS.Target)
		}
	}
	cfg := base(&DNS{Target: "203.0.113.10", Cloudflare: &DNSCloudflare{Token: "file:/run/cf"}})
	clone := Clone(cfg)
	clone.DNS.Cloudflare.Token = "changed"
	if cfg.DNS.Cloudflare.Token != "file:/run/cf" {
		t.Fatal("Clone shared the dns section")
	}
}
