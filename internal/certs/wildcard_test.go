package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"slices"
	"testing"
	"time"
)

const wildcardName = "*.example.com"

func TestParseGenerationAcceptsAWildcardLeaf(t *testing.T) {
	now := time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, wildcardName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	cert, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{wildcardName}, now, testRoots(t, gen))
	if err != nil {
		t.Fatalf("a wildcard leaf for the configured wildcard must be accepted: %v", err)
	}
	if cert.Leaf == nil || !slices.Contains(cert.Leaf.DNSNames, wildcardName) {
		t.Fatalf("leaf = %+v", cert.Leaf)
	}
}

func TestParseGenerationRejectsAWildcardOfAnotherDomain(t *testing.T) {
	now := time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, "*.evil.net", now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{wildcardName}, now, testRoots(t, gen)); err == nil {
		t.Fatal("a wildcard for another domain must never be activated")
	}
}

func TestParseGenerationRejectsAPlainLeafForAWildcardName(t *testing.T) {
	now := time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, "preview.example.com", now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{wildcardName}, now, testRoots(t, gen)); err == nil {
		t.Fatal("a single-host leaf does not satisfy a wildcard configuration")
	}
}

func wildcardManager(t *testing.T, store Store, now time.Time, trusted ...StoredGeneration) *Manager {
	t.Helper()
	m, err := NewManager(ManagerConfig{
		Host:           wildcardName,
		RootCAs:        testRoots(t, trusted...),
		Now:            func() time.Time { return now },
		Jitter:         func(d time.Duration) time.Duration { return d },
		AttemptTimeout: time.Minute,
		BackoffBase:    time.Minute,
		BackoffMax:     time.Hour,
	}, store, &stubIssuer{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestManagerServesEveryNameTheWildcardCovers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)
	store := testStore(t)
	gen, _ := testGeneration(t, wildcardName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if err := store.Save(ctx, wildcardName, gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := wildcardManager(t, store, now, gen)
	m.startup(ctx)

	for _, name := range []string{"preview.example.com", "sigea.example.com", "SIGEA.Example.COM"} {
		if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name}); err != nil {
			t.Errorf("GetCertificate(%q): %v", name, err)
		}
	}
	// The apex stays on Pages, and a wildcard never reaches two labels deep.
	for _, name := range []string{"example.com", "a.b.example.com", "attacker.net", ""} {
		if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name}); err == nil {
			t.Errorf("GetCertificate(%q) must be refused", name)
		}
	}
}

func TestFileStoreKeepsAWildcardUnderASafeDirectory(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	start := time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, wildcardName, start, start.Add(90*24*time.Hour))
	if err := store.Save(ctx, wildcardName, gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, ok, err := store.Load(ctx, wildcardName)
	if err != nil || !ok || string(loaded.CertPEM) != string(gen.CertPEM) {
		t.Fatalf("Load = ok %v, err %v", ok, err)
	}
	dir, err := store.hostDir(wildcardName)
	if err != nil {
		t.Fatalf("hostDir: %v", err)
	}
	if slices.Contains([]byte(dir), '*') {
		t.Errorf("wildcard directory %q contains a literal *", dir)
	}
	// A plain host can never alias the wildcard's directory.
	if _, err := store.hostDir("_wildcard.example.com"); err == nil {
		t.Error("a plain name must not be able to claim the wildcard's storage key")
	}
}

func TestACMEIssuerIssuesAWildcardWithTheBaseChallenge(t *testing.T) {
	fake, server := newFakeACME(t, wildcardName)
	dns := newFakeDNS()
	issuer, _ := testACMEIssuer(t, server, dns, &fakeResolver{dns: dns}, ACMEConfig{})

	gen, err := issuer.Obtain(context.Background(), []string{wildcardName}, testSigningKey(t))
	if err != nil {
		t.Fatalf("Obtain: %v", err)
	}
	if want := []string{"_acme-challenge.example.com"}; !slices.Equal(dns.createdNames, want) {
		t.Errorf("challenge records created at %v, want %v (never a literal *)", dns.createdNames, want)
	}
	if len(dns.snapshot()) != 0 {
		t.Errorf("challenge record not cleaned up: %+v", dns.snapshot())
	}
	roots := x509.NewCertPool()
	roots.AddCert(fake.caCert)
	if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{wildcardName}, time.Now(), roots); err != nil {
		t.Fatalf("issued wildcard generation is not usable: %v", err)
	}
}
