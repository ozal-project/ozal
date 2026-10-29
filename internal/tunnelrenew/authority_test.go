package tunnelrenew

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthorityFixedRolesLocalKeyAndNoOverwrite(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	ca := filepath.Join(parent, "ca")
	if err := CreateAuthority(ctx, ca, 366, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := CreateAuthority(ctx, ca, 366, time.Now()); err == nil {
		t.Fatal("overwrote authority")
	}
	for _, role := range []string{"edge", "connector"} {
		dir := filepath.Join(parent, role)
		if err := PrepareRole(ctx, role, dir); err != nil {
			t.Fatal(err)
		}
		csr := filepath.Join(dir, role+".csr")
		out := filepath.Join(dir, role+".crt")
		// A CSR's requested identity cannot choose the certificate's authority scope.
		_, key, _ := ed25519.GenerateKey(rand.Reader)
		raw, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "attacker"}, DNSNames: []string{"other.example.com"}}, key)
		attacker := filepath.Join(parent, role+"-attacker.csr")
		os.WriteFile(attacker, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: raw}), 0600)
		other := filepath.Join(parent, role+"-attacker.crt")
		if err := SignRole(ctx, role, ca, attacker, other); err != nil {
			t.Fatal(err)
		}
		bytes, _ := os.ReadFile(other)
		block, _ := pem.Decode(bytes)
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || len(cert.DNSNames) != 1 || cert.DNSNames[0] != role+".homelab.internal" {
			t.Fatal(cert, err)
		}
		if err := SignRole(ctx, role, ca, csr, out); err != nil {
			t.Fatal(err)
		}
		if err := CheckRole(ctx, role, dir, filepath.Join(ca, "ca.crt")); err != nil {
			t.Fatal(err)
		}
		if err := SignRole(ctx, role, ca, csr, out); err == nil {
			t.Fatal("overwrote certificate")
		}
		info, _ := os.Stat(filepath.Join(dir, role+".key"))
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
	}
	if err := PrepareRole(ctx, "../admin", filepath.Join(parent, "bad")); err == nil {
		t.Fatal("accepted unknown role")
	}
	oldCA := filepath.Join(parent, "expired")
	if err := CreateAuthority(ctx, oldCA, 38, time.Now().Add(-2*day)); err != nil {
		t.Fatal(err)
	}
	if err := SignRole(ctx, "edge", oldCA, filepath.Join(parent, "edge/edge.csr"), filepath.Join(parent, "bad.crt")); err == nil {
		t.Fatal("signed beyond safe CA margin")
	}
}

func TestSignerRefusesLeafAsCertificateAuthority(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	ca := filepath.Join(parent, "ca")
	identity := filepath.Join(parent, "edge")
	if err := CreateAuthority(ctx, ca, 366, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRole(ctx, "edge", identity); err != nil {
		t.Fatal(err)
	}
	// Use the same key for the deliberately misconfigured non-CA certificate.
	keyData, err := os.ReadFile(filepath.Join(ca, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(keyData)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	signer := key.(ed25519.PrivateKey)
	template := &x509.Certificate{SerialNumber: big.NewInt(5), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * day), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ca, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SignRole(ctx, "edge", ca, filepath.Join(identity, "edge.csr"), filepath.Join(identity, "edge.crt")); err == nil {
		t.Fatal("signed with a non-authority certificate")
	}
}
