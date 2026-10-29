package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
)

const stagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

type fakeCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newFakeCA(t *testing.T) fakeCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fake Staging Root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return fakeCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf for key carrying exactly names.
func (ca fakeCA) issue(t *testing.T, names []string, key crypto.Signer) certs.StoredGeneration {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return certs.StoredGeneration{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}
}

type fakeIssuer struct {
	calls  int
	got    certs.ACMEConfig
	obtain func(context.Context, []string, crypto.Signer) (certs.StoredGeneration, error)
}

func (f *fakeIssuer) Obtain(ctx context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error) {
	f.calls++
	return f.obtain(ctx, names, key)
}

func (f *fakeIssuer) factory(cfg certs.ACMEConfig, _ *certs.FileStore) (obtainer, error) {
	f.got = cfg
	return f, nil
}

func tryConfig(t *testing.T, mutate func(*config.DNS01)) *config.Config {
	t.Helper()
	t.Setenv("OZAL_TEST_TRY_TOKEN", "token-value")
	dns01 := &config.DNS01{
		StateDir: filepath.Join(t.TempDir(), "production-state"), DirectoryURL: stagingDirectory,
		DNSProvider: "cloudflare", DNSToken: "env:OZAL_TEST_TRY_TOKEN",
	}
	if mutate != nil {
		mutate(dns01)
	}
	return &config.Config{TLS: config.TLS{Enabled: true, Domains: []string{"preview.example.com"}, Email: "ops@example.com", DNS01: dns01}}
}

func runTry(t *testing.T, cfg *config.Config, allowProduction bool, issuer *fakeIssuer) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := tryDNS01(context.Background(), cfg, allowProduction, time.Minute, &out, issuer.factory)
	return out.String(), err
}

func TestDNS01TryIssuesAndDiscardsTheCertificate(t *testing.T) {
	ca := newFakeCA(t)
	rootsFile := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(rootsFile, ca.pem, 0o600); err != nil {
		t.Fatal(err)
	}
	issuer := &fakeIssuer{obtain: func(_ context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error) {
		return ca.issue(t, names, key), nil
	}}
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg := tryConfig(t, func(d *config.DNS01) { d.CertificateRootsFile = rootsFile })

	out, err := runTry(t, cfg, false, issuer)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"provider=cloudflare", "names=preview.example.com", "OK in", "Fake Staging Root", "chain verified against certificate_roots_file", "discarded"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "BEGIN") || strings.Contains(out, "PRIVATE") {
		t.Fatalf("the rehearsal printed key or certificate material:\n%s", out)
	}
	if left, _ := os.ReadDir(scratch); len(left) != 0 {
		t.Fatalf("the throwaway state directory was not removed: %v", left)
	}
	if _, err := os.Stat(cfg.TLS.DNS01.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the production state directory was touched: %v", err)
	}
	if issuer.got.DirectoryURL != stagingDirectory || issuer.got.Email != "ops@example.com" {
		t.Fatalf("issuer config = %+v", issuer.got)
	}
}

func TestDNS01TryWithoutRootsSaysTheChainWasNotVerified(t *testing.T) {
	ca := newFakeCA(t)
	issuer := &fakeIssuer{obtain: func(_ context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error) {
		return ca.issue(t, names, key), nil
	}}
	out, err := runTry(t, tryConfig(t, nil), false, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chain NOT verified") {
		t.Fatalf("a rehearsal must not claim a verification it did not make:\n%s", out)
	}
}

func TestDNS01TryFailures(t *testing.T) {
	ca := newFakeCA(t)
	other := newFakeCA(t)
	rootsFile := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(rootsFile, ca.pem, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mutate func(*config.DNS01)
		obtain func(context.Context, []string, crypto.Signer) (certs.StoredGeneration, error)
		want   string
	}{
		"issuance error": {nil, func(context.Context, []string, crypto.Signer) (certs.StoredGeneration, error) {
			return certs.StoredGeneration{}, errors.New("validation failed")
		}, "issuance failed"},
		"certificate lacks a name": {nil, func(_ context.Context, _ []string, key crypto.Signer) (certs.StoredGeneration, error) {
			return ca.issue(t, []string{"another.example.com"}, key), nil
		}, "does not cover"},
		"chain from an untrusted root": {func(d *config.DNS01) { d.CertificateRootsFile = rootsFile }, func(_ context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error) {
			return other.issue(t, names, key), nil
		}, "does not verify"},
		"certificate for another key": {nil, func(_ context.Context, names []string, _ crypto.Signer) (certs.StoredGeneration, error) {
			stranger, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			good := ca.issue(t, names, stranger)
			wrong, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			return certs.StoredGeneration{CertPEM: good.CertPEM, KeyPEM: ca.issue(t, names, wrong).KeyPEM}, nil
		}, "does not match the key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeIssuer{obtain: tc.obtain}
			_, err := runTry(t, tryConfig(t, tc.mutate), false, issuer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDNS01TryRefusesBeforeDoingAnything(t *testing.T) {
	never := &fakeIssuer{obtain: func(context.Context, []string, crypto.Signer) (certs.StoredGeneration, error) {
		t.Fatal("the issuer must not be reached")
		return certs.StoredGeneration{}, nil
	}}
	production := tryConfig(t, func(d *config.DNS01) { d.DirectoryURL = "https://acme-v02.api.letsencrypt.org/directory" })
	if _, err := runTry(t, production, false, never); err == nil || !strings.Contains(err.Error(), "staging") {
		t.Fatalf("a production CA must be refused: %v", err)
	}
	notConfigured := tryConfig(t, nil)
	notConfigured.TLS.DNS01 = nil
	if _, err := runTry(t, notConfigured, false, never); err == nil {
		t.Fatal("no dns01 block: nothing to try")
	}
	disabled := tryConfig(t, nil)
	disabled.TLS.Enabled = false
	if _, err := runTry(t, disabled, false, never); err == nil {
		t.Fatal("tls disabled: nothing to try")
	}
	if never.calls != 0 {
		t.Fatalf("issuer reached %d times", never.calls)
	}
}

func TestDNS01TryAllowsProductionOnlyWhenAsked(t *testing.T) {
	ca := newFakeCA(t)
	issuer := &fakeIssuer{obtain: func(_ context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error) {
		return ca.issue(t, names, key), nil
	}}
	production := tryConfig(t, func(d *config.DNS01) { d.DirectoryURL = "https://acme-v02.api.letsencrypt.org/directory" })
	if _, err := runTry(t, production, true, issuer); err != nil {
		t.Fatalf("-allow-production must permit it: %v", err)
	}
}

func TestRefuseProductionDirectoryTable(t *testing.T) {
	for url, allow := range map[string]bool{
		stagingDirectory: false, "https://acme-staging.example.org/dir": false, "https://acme-v02.api.letsencrypt.org/directory": true,
	} {
		if err := refuseProductionDirectory(url, allow); err != nil {
			t.Errorf("%s (allow=%v): %v", url, allow, err)
		}
	}
	for _, url := range []string{"https://acme-v02.api.letsencrypt.org/directory", "https://acme.zerossl.com/v2/DV90", "not a url", ""} {
		if err := refuseProductionDirectory(url, false); err == nil {
			t.Errorf("%q accepted", url)
		}
	}
}

func TestDNS01TryWiresAndStopsTheBuiltinServer(t *testing.T) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.LocalAddr().String()
	probe.Close()
	cfg := tryConfig(t, func(d *config.DNS01) {
		d.DNSProvider, d.DNSToken = "builtin", ""
		d.Builtin = &config.DNS01Builtin{Zone: "acme.example.test", Listen: addr, Nameservers: []string{"ns1.example.test"}}
	})
	ca := newFakeCA(t)
	issuer := &fakeIssuer{}
	issuer.obtain = func(ctx context.Context, names []string, key crypto.Signer) (certs.StoredGeneration, error) {
		// The provider handed to the issuer publishes into the live server.
		rec, err := issuer.got.DNS.CreateTXT(ctx, "x.acme.example.test", "v")
		if err != nil {
			return certs.StoredGeneration{}, err
		}
		if err := issuer.got.DNS.DeleteTXT(ctx, rec); err != nil {
			return certs.StoredGeneration{}, err
		}
		return ca.issue(t, names, key), nil
	}
	if out, err := runTry(t, cfg, false, issuer); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Stopped: both sockets can be bound again.
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("the DNS server kept its UDP socket: %v", err)
	}
	udp.Close()
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the DNS server kept its TCP socket: %v", err)
	}
	tcp.Close()
}

func TestRunDNS01UsageAndMissingConfig(t *testing.T) {
	var out bytes.Buffer
	if err := runDNS01(context.Background(), nil, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no command: %v", err)
	}
	if err := runDNS01(context.Background(), []string{"issue"}, &out); err == nil {
		t.Fatal("an unknown command must fail")
	}
	if err := runDNS01(context.Background(), []string{"try", "-config", filepath.Join(t.TempDir(), "absent.json")}, &out); err == nil {
		t.Fatal("a missing config must fail")
	}
}
