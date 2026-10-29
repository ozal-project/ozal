package main

import (
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
)

func rfc2136Config(secretRef string) *config.DNS01 {
	return &config.DNS01{
		DNSProvider: "rfc2136",
		RFC2136: &config.DNS01RFC2136{
			Server: "127.0.0.1:53", Zone: "example.test", KeyName: "ozal-key", Algorithm: "hmac-sha256", Secret: secretRef,
		},
	}
}

func TestNewDNSProviderSelectsTheConfiguredAdapter(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "tsig")
	if err := os.WriteFile(secretFile, []byte(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := newDNSProvider(rfc2136Config("file:" + secretFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*certs.RFC2136Client); !ok {
		t.Fatalf("provider = %T", provider)
	}

	t.Setenv("OZAL_TEST_CF_TOKEN", "token-value")
	for _, name := range []string{"", "cloudflare"} {
		provider, err = newDNSProvider(&config.DNS01{DNSProvider: name, DNSToken: "env:OZAL_TEST_CF_TOKEN"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := provider.(*certs.CloudflareClient); !ok {
			t.Fatalf("%q: provider = %T", name, provider)
		}
	}
}

func TestNewDNSProviderRefusesBadSecretsWithoutLeakingThem(t *testing.T) {
	dir := t.TempDir()
	notBase64 := filepath.Join(dir, "plain")
	short := filepath.Join(dir, "short")
	if err := os.WriteFile(notBase64, []byte("this is not base64 ###"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(short, []byte(base64.StdEncoding.EncodeToString([]byte("tiny"))), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, ref := range map[string]string{
		"missing file":     "file:" + filepath.Join(dir, "absent"),
		"not base64":       "file:" + notBase64,
		"short secret":     "file:" + short,
		"unset env secret": "env:OZAL_TEST_UNSET_TSIG",
	} {
		_, err := newDNSProvider(rfc2136Config(ref))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "this is not base64") {
			t.Errorf("%s: the error leaks the secret: %v", name, err)
		}
	}
	if _, err := newDNSProvider(&config.DNS01{DNSProvider: "route53"}); err == nil {
		t.Error("unknown provider accepted")
	}
}

func TestNewDNSProviderHetzner(t *testing.T) {
	t.Setenv("OZAL_TEST_HZ_TOKEN", "token-value")
	provider, err := newDNSProvider(&config.DNS01{DNSProvider: "hetzner", Hetzner: &config.DNS01Hetzner{Zone: "example.test", Token: "env:OZAL_TEST_HZ_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*certs.HetznerClient); !ok {
		t.Fatalf("provider = %T", provider)
	}
	if _, err := newDNSProvider(&config.DNS01{DNSProvider: "hetzner", Hetzner: &config.DNS01Hetzner{Zone: "example.test", Token: "env:OZAL_TEST_HZ_UNSET"}}); err == nil {
		t.Fatal("an unset token was accepted")
	}
}

func TestStartBuiltinDNSServesPublishedChallengesAndStops(t *testing.T) {
	dns01 := &config.DNS01{DNSProvider: "builtin", Builtin: &config.DNS01Builtin{
		Zone: "acme.example.test", Listen: "127.0.0.1:0", Nameservers: []string{"ns1.example.test"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider, stop, err := startBuiltinDNS(ctx, dns01)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := provider.CreateTXT(ctx, "x.acme.example.test", "challenge-value")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID == "" || rec.Name != "x.acme.example.test" {
		t.Fatalf("record = %+v", rec)
	}
	if err := provider.DeleteTXT(ctx, rec); err != nil {
		t.Fatal(err)
	}
	stop()
	stop() // closing twice must be harmless: error paths call it too
}

func TestStartBuiltinDNSFailsAtStartupWhenTheAddressIsTaken(t *testing.T) {
	taken, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	dns01 := &config.DNS01{DNSProvider: "builtin", Builtin: &config.DNS01Builtin{
		Zone: "acme.example.test", Listen: taken.LocalAddr().String(), Nameservers: []string{"ns1.example.test"},
	}}
	if _, _, err := startBuiltinDNS(context.Background(), dns01); err == nil {
		t.Fatal("a certificate that can never validate must fail at startup, not later")
	}
}
