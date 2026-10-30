package originauth

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

// startTrustServer is startServer reading its authorities from trust, as the
// DNS-01 listener does (no ACME exemption).
func startTrustServer(t *testing.T, trust *Trust) string {
	t.Helper()
	base := &tls.Config{Certificates: []tls.Certificate{serverCert(t)}}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", ServerTLSTrust(base, trust, events.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				if err := conn.(*tls.Conn).Handshake(); err != nil {
					return
				}
				_, _ = conn.Write([]byte("ok"))
			}(conn)
		}
	}()
	return listener.Addr().String()
}

func identityPair(t *testing.T, certPEM, keyPEM []byte) tls.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

// EDGE-009: the edge's own proxy certificate is accepted from the first
// connection after it is created, without a restart, while every other
// certificate stays refused.
func TestIdentityIsTrustedLiveOnlyOnceCreated(t *testing.T) {
	configured := newAuthority(t, "configured-origin-ca")
	trust := NewTrust(poolOf(t, configured))
	addr := startTrustServer(t, trust)

	// A certificate shaped like ours but from an identity this edge never
	// created is refused.
	stranger := NewIdentity(t.TempDir(), nil)
	strangerCert, strangerKey, err := stranger.Ensure(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	strangerPair := identityPair(t, strangerCert, strangerKey)
	if err := pull(t, addr, &strangerPair); err == nil {
		t.Fatal("another edge's identity was accepted")
	}

	identity := NewIdentity(t.TempDir(), trust)
	if _, ok := identity.Certificate(); ok {
		t.Fatal("an identity exists before Ensure")
	}
	certPEM, keyPEM, err := identity.Ensure(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ours := identityPair(t, certPEM, keyPEM)
	if err := pull(t, addr, &ours); err != nil {
		t.Fatalf("the edge's own identity was refused after Ensure: %v", err)
	}
	// The configured authority still works, and a stranger is still refused.
	good := configured.issue(t, clientOptions())
	if err := pull(t, addr, &good); err != nil {
		t.Fatalf("the configured authority stopped working: %v", err)
	}
	if err := pull(t, addr, &strangerPair); err == nil {
		t.Fatal("adding our identity let another one in")
	}
	if err := pull(t, addr, nil); err == nil {
		t.Fatal("a connection without a certificate was accepted")
	}

	// Ensure is idempotent: the same certificate, no second authority.
	again, _, err := identity.Ensure(time.Now())
	if err != nil || string(again) != string(certPEM) {
		t.Fatalf("Ensure created a second identity: %v", err)
	}
}

// The authority's key is discarded: what is on disk cannot mint another
// certificate the listener accepts, and the client certificate cannot act
// as an authority either.
func TestIdentityCannotIssueFurtherCertificates(t *testing.T) {
	dir := t.TempDir()
	trust := NewTrust(x509.NewCertPool())
	identity := NewIdentity(dir, trust)
	certPEM, keyPEM, err := identity.Ensure(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want 0600", e.Name(), info.Mode().Perm())
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"ca.pem", "client.key", "client.pem"}) {
		t.Fatalf("identity files = %v; the authority key must never be written", names)
	}

	// A certificate signed with the client key, chaining to our authority
	// through the client certificate, is refused.
	pair := identityPair(t, certPEM, keyPEM)
	leaf := peers(t, pair)[0]
	if leaf.IsCA {
		t.Fatal("the client certificate is an authority")
	}
	key := newKey(t)
	template := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "minted"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, leaf, &key.PublicKey, pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	minted, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if trust.Trusts([]*x509.Certificate{minted, leaf}, time.Now()) {
		t.Fatal("a certificate signed by the client certificate was trusted")
	}
	if !trust.Trusts([]*x509.Certificate{leaf}, time.Now()) {
		t.Fatal("the identity itself is not trusted")
	}
}

// After a restart the identity is read back and trusted again; a damaged
// identity (certificate from another authority) is refused, never half
// trusted.
func TestIdentityLoadAfterRestart(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := NewIdentity(dir, nil).Ensure(time.Now()); err != nil {
		t.Fatal(err)
	}
	trust := NewTrust(x509.NewCertPool())
	restarted := NewIdentity(dir, trust)
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	leaf, ok := restarted.Certificate()
	if !ok || !trust.Trusts([]*x509.Certificate{leaf}, time.Now()) {
		t.Fatal("the identity is not trusted after a restart")
	}

	other := t.TempDir()
	if _, _, err := NewIdentity(other, nil).Ensure(time.Now()); err != nil {
		t.Fatal(err)
	}
	foreign, err := os.ReadFile(filepath.Join(other, "client.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client.pem"), foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	damagedTrust := NewTrust(x509.NewCertPool())
	if err := NewIdentity(dir, damagedTrust).Load(); err == nil {
		t.Fatal("a damaged identity loaded")
	}
	if block, _ := pem.Decode(foreign); block != nil {
		cert, _ := x509.ParseCertificate(block.Bytes)
		if damagedTrust.Trusts([]*x509.Certificate{cert}, time.Now()) {
			t.Fatal("a damaged identity was partly trusted")
		}
	}
	// A missing identity is not an error: nothing is trusted.
	if err := NewIdentity(t.TempDir(), damagedTrust).Load(); err != nil {
		t.Fatalf("a missing identity failed to load: %v", err)
	}
}
