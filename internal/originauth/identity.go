package originauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// identityLifetime is how long the edge's proxy certificate is valid. It is
// replaced by setting the zone up again, never silently.
const identityLifetime = 3 * 365 * 24 * time.Hour

const (
	identityCA     = "ca.pem"
	identityCert   = "client.pem"
	identityKey    = "client.key"
	identityPrefix = "Ozal origin pull"
)

// Identity is the client certificate this edge hands its proxy (Cloudflare
// Authenticated Origin Pulls, EDGE-009), so a domain added later needs no
// certificate files from the operator's laptop. It is a fresh authority that
// signs exactly one client certificate and is then discarded: its key never
// touches disk, so nothing on this host can mint a second certificate the
// listener would accept. The authority's certificate joins Trust; the client
// certificate and its key are kept (0600) only to upload to the proxy.
type Identity struct {
	dir   string
	trust *Trust

	mu     sync.Mutex
	cert   []byte
	key    []byte
	leaf   *x509.Certificate
	loaded bool
}

// NewIdentity keeps its files in dir. trust may be nil when the listener
// does not check client certificates: the identity can still be created and
// uploaded, it just has nothing to unlock.
func NewIdentity(dir string, trust *Trust) *Identity {
	return &Identity{dir: dir, trust: trust}
}

// Load reads an identity created earlier and trusts its authority. A
// missing identity is not an error; a damaged one is, so the edge never
// starts trusting half of one.
func (i *Identity) Load() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.loadLocked()
}

func (i *Identity) loadLocked() error {
	if i.loaded {
		return nil
	}
	caPEM, err := os.ReadFile(filepath.Join(i.dir, identityCA))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("origin identity: %w", err)
	}
	certPEM, err := os.ReadFile(filepath.Join(i.dir, identityCert))
	if err != nil {
		return fmt.Errorf("origin identity: %w", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(i.dir, identityKey))
	if err != nil {
		return fmt.Errorf("origin identity: %w", err)
	}
	ca, err := parseCertificate(caPEM)
	if err != nil {
		return fmt.Errorf("origin identity authority: %w", err)
	}
	leaf, err := parseCertificate(certPEM)
	if err != nil {
		return fmt.Errorf("origin identity certificate: %w", err)
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		return errors.New("origin identity: the certificate was not issued by its authority")
	}
	i.adopt(ca, leaf, certPEM, keyPEM)
	return nil
}

func (i *Identity) adopt(ca, leaf *x509.Certificate, certPEM, keyPEM []byte) {
	i.cert, i.key, i.leaf, i.loaded = certPEM, keyPEM, leaf, true
	if i.trust != nil {
		i.trust.Add(ca)
	}
}

// Certificate returns the client certificate without creating one.
// ok is false until Ensure has run once on this host.
func (i *Identity) Certificate() (leaf *x509.Certificate, ok bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.loadLocked(); err != nil || !i.loaded {
		return nil, false
	}
	return i.leaf, true
}

// Ensure returns the client certificate and key, creating them on first use.
// The authority is trusted before Ensure returns, so a proxy that starts
// presenting the certificate is accepted on its first connection.
func (i *Identity) Ensure(now time.Time) (certPEM, keyPEM []byte, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.loadLocked(); err != nil {
		return nil, nil, err
	}
	if i.loaded {
		return i.cert, i.key, nil
	}
	ca, leaf, caPEM, certPEM, keyPEM, err := newIdentity(now)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(i.dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("origin identity: %w", err)
	}
	// The key and certificate go first and the authority last: Load treats
	// the authority as the marker of a complete identity.
	for _, f := range []struct {
		name string
		data []byte
	}{{identityKey, keyPEM}, {identityCert, certPEM}, {identityCA, caPEM}} {
		if err := writePrivate(filepath.Join(i.dir, f.name), f.data); err != nil {
			return nil, nil, fmt.Errorf("origin identity: %w", err)
		}
	}
	i.adopt(ca, leaf, certPEM, keyPEM)
	return certPEM, keyPEM, nil
}

func newIdentity(now time.Time) (ca, leaf *x509.Certificate, caPEM, certPEM, keyPEM []byte, err error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	notBefore, notAfter := now.Add(-time.Hour), now.Add(identityLifetime)
	caTemplate := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: identityPrefix + " authority"},
		NotBefore:             notBefore,
		NotAfter:              notAfter.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	// RSA for the certificate the proxy presents: the widest accepted kind
	// for uploaded client certificates.
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: identityPrefix + " client"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	leaf, err = x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return ca, leaf, caPEM, certPEM, keyPEM, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return n
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ParseCertificates reads every certificate in a PEM bundle, for checking a
// certificate a provider reports against Trust.
func ParseCertificates(data []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return out
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			out = append(out, cert)
		}
	}
}

func writePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
