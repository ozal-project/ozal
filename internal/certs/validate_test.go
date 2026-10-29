package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"
)

// testGeneration builds an issuer and a leaf/signing pair for host, and
// returns the pem-encoded pair plus the parsed leaf. Every test that needs
// a certificate starts here, so no test ever depends on a real CA.
func testGeneration(t *testing.T, host string, notBefore, notAfter time.Time, altNames ...string) (StoredGeneration, *x509.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ozal test CA"},
		NotBefore:             notBefore.Add(-time.Hour),
		NotAfter:              notAfter.Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     append([]string{host}, altNames...),
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...,
	)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return StoredGeneration{CertPEM: certPEM, KeyPEM: keyPEM}, leaf
}

func TestParseGenerationAcceptsValidPair(t *testing.T) {
	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	gen, leaf := testGeneration(t, "preview.example.com", start, start.Add(90*24*time.Hour))
	cert, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{"preview.example.com"}, start.Add(time.Hour), testRoots(t, gen))
	if err != nil {
		t.Fatalf("ParseGeneration: %v", err)
	}
	if cert.Leaf == nil || !cert.Leaf.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("leaf not attached, got %+v", cert.Leaf)
	}
	if len(cert.Certificate) != 2 {
		t.Fatalf("chain length = %d, want 2", len(cert.Certificate))
	}
}

func TestParseGenerationRefusals(t *testing.T) {
	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	valid, _ := testGeneration(t, "preview.example.com", start, start.Add(24*time.Hour))

	t.Run("wrong host", func(t *testing.T) {
		_, err := ParseGeneration(valid.CertPEM, valid.KeyPEM, []string{"other.example.com"}, start, testRoots(t, valid))
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		_, err := ParseGeneration(valid.CertPEM, valid.KeyPEM, []string{"preview.example.com"}, start.Add(48*time.Hour), testRoots(t, valid))
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
	t.Run("not yet valid", func(t *testing.T) {
		_, err := ParseGeneration(valid.CertPEM, valid.KeyPEM, []string{"preview.example.com"}, start.Add(-time.Hour), testRoots(t, valid))
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
	t.Run("key from another generation", func(t *testing.T) {
		other, _ := testGeneration(t, "preview.example.com", start, start.Add(24*time.Hour))
		_, err := ParseGeneration(valid.CertPEM, other.KeyPEM, []string{"preview.example.com"}, start, testRoots(t, valid))
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		_, err := ParseGeneration([]byte("not pem"), []byte("not pem"), []string{"preview.example.com"}, start, nil)
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
	t.Run("CA leaf", func(t *testing.T) {
		caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber:          big.NewInt(9),
			Subject:               pkix.Name{CommonName: "preview.example.com"},
			DNSNames:              []string{"preview.example.com"},
			NotBefore:             start,
			NotAfter:              start.Add(time.Hour),
			IsCA:                  true,
			KeyUsage:              x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseGeneration(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
			[]string{"preview.example.com"}, start.Add(time.Minute), nil,
		)
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
	t.Run("client-auth only", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(10),
			Subject:      pkix.Name{CommonName: "preview.example.com"},
			DNSNames:     []string{"preview.example.com"},
			NotBefore:    start,
			NotAfter:     start.Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseGeneration(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
			[]string{"preview.example.com"}, start.Add(time.Minute), nil,
		)
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("err = %v, want ErrInvalidGeneration", err)
		}
	})
}

// testRoots trusts only the independently selected test fixture issuers.
func testRoots(t *testing.T, generations ...StoredGeneration) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	for _, gen := range generations {
		_, issuer := pem.Decode(gen.CertPEM)
		if !roots.AppendCertsFromPEM(issuer) {
			t.Fatal("test fixture has no issuer")
		}
	}
	return roots
}

func TestParseGenerationRejectsBrokenChain(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, "preview.example.com", now.Add(-time.Hour), now.Add(time.Hour))
	leaf, _ := pem.Decode(gen.CertPEM)
	other, _ := testGeneration(t, "preview.example.com", now.Add(-time.Hour), now.Add(time.Hour))
	_, otherCA := pem.Decode(other.CertPEM)
	for name, chain := range map[string][]byte{
		"malformed issuer": append(pem.EncodeToMemory(leaf), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("broken")})...),
		"unrelated issuer": append(pem.EncodeToMemory(leaf), otherCA...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseGeneration(chain, gen.KeyPEM, []string{"preview.example.com"}, now, testRoots(t, other)); !errors.Is(err, ErrInvalidGeneration) {
				t.Fatalf("broken chain accepted: %v", err)
			}
		})
	}
	t.Run("untrusted embedded root", func(t *testing.T) {
		if _, err := ParseGeneration(gen.CertPEM, gen.KeyPEM, []string{"preview.example.com"}, now, x509.NewCertPool()); !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("untrusted root accepted: %v", err)
		}
	})
}

func TestParseGenerationVerifiesIntermediateWithoutSendingRoot(t *testing.T) {
	now := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	makeKey := func() *ecdsa.PrivateKey {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	sign := func(template, parent *x509.Certificate, key, parentKey *ecdsa.PrivateKey) *x509.Certificate {
		t.Helper()
		der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	rootKey, intermediateKey, leafKey := makeKey(), makeKey(), makeKey()
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(100), Subject: pkix.Name{CommonName: "trusted test root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	root := sign(rootTemplate, rootTemplate, rootKey, rootKey)
	intermediateTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(101), Subject: pkix.Name{CommonName: "test intermediate"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	intermediate := sign(intermediateTemplate, root, intermediateKey, rootKey)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(102), DNSNames: []string{"preview.example.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf := sign(leafTemplate, intermediate, leafKey, intermediateKey)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	roots := x509.NewCertPool()
	roots.AddCert(root)
	chain := append(append([]byte(nil), leafPEM...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediate.Raw})...)
	if _, err := ParseGeneration(chain, keyPEM, []string{"preview.example.com"}, now, roots); err != nil {
		t.Fatalf("valid chain without root rejected: %v", err)
	}
	if _, err := ParseGeneration(leafPEM, keyPEM, []string{"preview.example.com"}, now, roots); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("missing intermediate accepted: %v", err)
	}
	broken := append([]byte(nil), intermediate.Raw...)
	broken[len(broken)-1] ^= 1
	chain = append(append([]byte(nil), leafPEM...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: broken})...)
	if _, err := ParseGeneration(chain, keyPEM, []string{"preview.example.com"}, now, roots); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("invalid issuer signature accepted: %v", err)
	}
}
