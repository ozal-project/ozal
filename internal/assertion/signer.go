package assertion

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// KeyFile is the edge's signing key inside the configured key directory.
const KeyFile = "signing.key"

// Signer signs assertions with one Ed25519 key. It is safe for concurrent use.
type Signer struct {
	key    ed25519.PrivateKey
	kid    string
	signer jose.Signer
	random io.Reader
	now    func() time.Time
}

// NewSigner wraps key. The key ID is the first 16 bytes of the SHA-256 of
// the public key, in hex, so an application can tell keys apart in rotation.
func NewSigner(key ed25519.PrivateKey) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("assertion: invalid Ed25519 key")
	}
	kid := KeyID(key.Public().(ed25519.PublicKey))
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, opts)
	if err != nil {
		return nil, fmt.Errorf("assertion: %w", err)
	}
	return &Signer{key: key, kid: kid, signer: signer, random: rand.Reader, now: time.Now}, nil
}

// KeyID names a public key.
func KeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:16])
}

// KeyID is this signer's key ID.
func (s *Signer) KeyID() string { return s.kid }

// Sign issues an assertion for p to audience, valid for Lifetime.
func (s *Signer) Sign(issuer, audience string, p Person) (string, error) {
	id := make([]byte, 16)
	if _, err := io.ReadFull(s.random, id); err != nil {
		return "", errors.New("assertion: no randomness for the assertion ID")
	}
	claims, err := Build(issuer, audience, p, s.now(), base64.RawURLEncoding.EncodeToString(id))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signed, err := s.signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("assertion: sign: %w", err)
	}
	return signed.CompactSerialize()
}

// PublicKeyPEM is the PKIX public key an application pins.
func (s *Signer) PublicKeyPEM() []byte {
	der, _ := x509.MarshalPKIXPublicKey(s.key.Public())
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// Verify parses and checks an assertion against pinned keys, by key ID. It is
// the reference an application can follow; Ozal uses it in its own tests.
func Verify(token string, keys []ed25519.PublicKey, issuer, audience string, now time.Time, skew time.Duration) (Claims, string, error) {
	signed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(signed.Signatures) != 1 {
		return Claims{}, "", errors.New("assertion: not a single-signature EdDSA JWS")
	}
	kid := signed.Signatures[0].Header.KeyID
	for _, key := range keys {
		if KeyID(key) != kid {
			continue
		}
		payload, err := signed.Verify(key)
		if err != nil {
			return Claims{}, "", errors.New("assertion: signature does not verify")
		}
		var claims Claims
		if err := json.Unmarshal(payload, &claims); err != nil {
			return Claims{}, "", errors.New("assertion: malformed claims")
		}
		return claims, Check(claims, issuer, audience, now, skew), nil
	}
	return Claims{}, "", errors.New("assertion: signed with a key that is not pinned")
}

// LoadOrCreateKey reads the signing key from dir, creating the directory
// (0700) and a new key (0600) when there is none. An existing key that is
// not a private regular file is refused rather than silently replaced.
func LoadOrCreateKey(dir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dir, KeyFile)
	if data, err := os.ReadFile(path); err == nil {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("assertion: %s must be a regular file readable only by its owner", path)
		}
		return parseKey(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("assertion: read %s: %w", path, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("assertion: create %s: %w", dir, err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".signing.key.*")
	if err != nil {
		return nil, fmt.Errorf("assertion: write key in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := pem.Encode(tmp, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	// Link, not rename: a key another process created first wins and both
	// end up with the same one.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreateKey(dir)
		}
		return nil, fmt.Errorf("assertion: save key: %w", err)
	}
	return key, nil
}

func parseKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("assertion: signing key is not a PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("assertion: signing key cannot be parsed")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("assertion: signing key is not Ed25519")
	}
	return key, nil
}

// ParsePublicKeyPEM reads a pinned public key.
func ParsePublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("assertion: not a PEM public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("assertion: public key cannot be parsed")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("assertion: public key is not Ed25519")
	}
	return key, nil
}
