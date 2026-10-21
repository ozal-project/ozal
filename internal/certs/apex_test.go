package certs

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"slices"
	"testing"
	"time"
)

// A wildcard does not cover its own base, so serving the apex takes a second
// name on the same certificate: "*.example.com" plus "example.com".
var apexNames = []string{wildcardName, "example.com"}

func TestCoversAny(t *testing.T) {
	for _, tt := range []struct {
		server string
		want   bool
	}{
		{"example.com", true},
		{"EXAMPLE.com", true},
		{"sigea.example.com", true},
		{"a.b.example.com", false},
		{"example.net", false},
		{"", false},
	} {
		if got := CoversAny(apexNames, tt.server); got != tt.want {
			t.Errorf("CoversAny(%v, %q) = %v, want %v", apexNames, tt.server, got, tt.want)
		}
	}
	if CoversAny(nil, "example.com") {
		t.Error("no names cover nothing")
	}
}

func TestACMEIssuerIssuesAWildcardAndItsApexOnOneCertificate(t *testing.T) {
	fake, server := newFakeACME(t, apexNames[0], apexNames[1:]...)
	dns := newFakeDNS()
	issuer, _ := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})

	gen, err := issuer.Obtain(context.Background(), apexNames, testSigningKey(t))
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	// Both authorizations validate the base domain, so both records go to
	// the same name; one at a time, each removed before the next.
	want := []string{"_acme-challenge.example.com", "_acme-challenge.example.com"}
	if !slices.Equal(dns.createdNames, want) {
		t.Errorf("challenge records created at %v, want %v", dns.createdNames, want)
	}
	if dns.peak != 1 {
		t.Errorf("%d challenge records were live at once, want 1", dns.peak)
	}
	if len(dns.snapshot()) != 0 {
		t.Errorf("challenge records not cleaned up: %+v", dns.snapshot())
	}
	roots := x509.NewCertPool()
	roots.AddCert(fake.caCert)
	cert, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, apexNames, time.Now(), roots)
	if err != nil {
		t.Fatalf("issued generation is not usable: %v", err)
	}
	if !slices.Equal(cert.Leaf.DNSNames, apexNames) {
		t.Errorf("leaf names = %v, want %v", cert.Leaf.DNSNames, apexNames)
	}
}

func TestParseGenerationWantsEveryConfiguredName(t *testing.T) {
	now := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC)
	// The certificate the edge holds today: the wildcard alone.
	gen, _ := testGeneration(t, wildcardName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, apexNames, now, testRoots(t, gen)); err == nil {
		t.Fatal("a leaf without the apex must not be served for a configuration that names it")
	}
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{wildcardName}, now, testRoots(t, gen)); err != nil {
		t.Fatalf("the same leaf still satisfies the wildcard alone: %v", err)
	}
}

func apexManager(t *testing.T, store Store, issuer Issuer, now time.Time, trusted ...StoredGeneration) *Manager {
	t.Helper()
	m, err := NewManager(ManagerConfig{
		Host:           apexNames[0],
		AltNames:       apexNames[1:],
		RootCAs:        testRoots(t, trusted...),
		Now:            func() time.Time { return now },
		Jitter:         func(d time.Duration) time.Duration { return d },
		AttemptTimeout: time.Minute,
		BackoffBase:    time.Minute,
		BackoffMax:     time.Hour,
	}, store, issuer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestManagerServesTheApexBesideTheWildcard(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC)
	store := testStore(t)
	gen, _ := testGeneration(t, wildcardName, now.Add(-time.Hour), now.Add(90*24*time.Hour), apexNames[1:]...)
	if err := store.Save(ctx, wildcardName, gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := apexManager(t, store, &stubIssuer{}, now, gen)
	m.startup(ctx)

	for _, name := range []string{"example.com", "sigea.example.com", "Example.COM"} {
		if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name}); err != nil {
			t.Errorf("GetCertificate(%q): %v", name, err)
		}
	}
	for _, name := range []string{"a.b.example.com", "example.net", ""} {
		if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name}); err == nil {
			t.Errorf("GetCertificate(%q) must be refused", name)
		}
	}
	if got := m.Status().Names; !slices.Equal(got, apexNames) {
		t.Errorf("Status().Names = %v, want %v", got, apexNames)
	}
}

func TestManagerReplacesAWildcardOnlyCertificateWhenTheApexIsAdded(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC)
	store := testStore(t)
	old, _ := testGeneration(t, wildcardName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if err := store.Save(ctx, wildcardName, old); err != nil {
		t.Fatalf("Save: %v", err)
	}
	next, _ := testGeneration(t, wildcardName, now.Add(-time.Minute), now.Add(90*24*time.Hour), apexNames[1:]...)
	var asked []string
	issuer := &stubIssuer{obtain: func(_ context.Context, names []string, _ crypto.Signer) (StoredGeneration, error) {
		asked = names
		return next, nil
	}}
	m := apexManager(t, store, issuer, now, old, next)
	m.startup(ctx)
	if wait := m.waitFor(now); wait != 0 {
		t.Fatalf("a stored certificate without the apex must make issuance due now, waits %s", wait)
	}
	// Until the replacement exists, the hosts the old certificate covers keep
	// working; only the apex waits.
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "sigea.example.com"}); err != nil {
		t.Errorf("the wildcard must keep serving its hosts during the replacement: %v", err)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"}); err == nil {
		t.Error("the apex must not be served a certificate that lacks it")
	}
	m.attempt(ctx)
	if !slices.Equal(asked, apexNames) {
		t.Errorf("issuer asked for %v, want %v", asked, apexNames)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"}); err != nil {
		t.Errorf("after issuance the apex must be served: %v", err)
	}
	if wait := m.waitFor(now); wait == 0 {
		t.Error("once complete, the next attempt waits for the renewal window")
	}
	// Same storage key as before: a rollback to the wildcard alone reads the
	// newest generation and still finds its name on it.
	loaded, ok, err := store.Load(ctx, wildcardName)
	if err != nil || !ok || string(loaded.CertPEM) != string(next.CertPEM) {
		t.Fatalf("Load = ok %v, err %v", ok, err)
	}
}

// A stored certificate for another name is never served, partially or not.
func TestManagerServesNothingFromACertificateForAnotherName(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC)
	store := testStore(t)
	other, _ := testGeneration(t, "*.evil.net", now.Add(-time.Hour), now.Add(90*24*time.Hour), "example.com")
	if err := store.Save(ctx, wildcardName, other); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := apexManager(t, store, &stubIssuer{}, now, other)
	m.startup(ctx)
	for _, name := range []string{"example.com", "sigea.example.com", "x.evil.net"} {
		if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name}); err == nil {
			t.Errorf("GetCertificate(%q) must be refused", name)
		}
	}
}

func TestNewManagerRefusesARepeatedName(t *testing.T) {
	for _, alt := range [][]string{{wildcardName}, {"example.com", "EXAMPLE.com"}, {""}} {
		_, err := NewManager(ManagerConfig{Host: wildcardName, AltNames: alt, RootCAs: x509.NewCertPool()}, testStore(t), &stubIssuer{})
		if err == nil {
			t.Errorf("AltNames %q must be refused", alt)
		}
	}
}
