package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ParseGeneration turns a stored certificate/key pair into a usable
// tls.Certificate and holds it to every check that must pass before the
// manager serves it: the private key matches the leaf (tls.X509KeyPair
// proves that), the leaf is a server certificate for every one of names, and
// now falls inside its validity window. A leaf missing a name is refused, so
// adding a name to the configuration makes the stored certificate due for
// replacement instead of serving it short. The chain must verify against explicit roots;
// an embedded issuer never establishes trust on its own. A pair that fails
// any check is never activated, no matter who wrote it.
func ParseGeneration(certPEM, keyPEM []byte, names []string, now time.Time, roots *x509.CertPool) (tls.Certificate, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("%w: certificate and key do not form a pair: %v", ErrInvalidGeneration, err)
	}
	if len(pair.Certificate) == 0 {
		return tls.Certificate{}, fmt.Errorf("%w: certificate chain is empty", ErrInvalidGeneration)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("%w: parse leaf: %v", ErrInvalidGeneration, err)
	}
	if err := validateLeaf(leaf, names, now); err != nil {
		return tls.Certificate{}, err
	}
	if roots == nil {
		return tls.Certificate{}, fmt.Errorf("%w: certificate trust roots are required", ErrInvalidGeneration)
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		issuer, err := x509.ParseCertificate(der)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("%w: parse issuer: %v", ErrInvalidGeneration, err)
		}
		intermediates.AddCert(issuer)
	}
	for _, name := range names {
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots: roots, Intermediates: intermediates, DNSName: ProbeName(name),
			CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			return tls.Certificate{}, fmt.Errorf("%w: verify certificate chain: %v", ErrInvalidGeneration, err)
		}
	}
	pair.Leaf = leaf
	return pair, nil
}

// validateLeaf is the pure half of ParseGeneration. It states every refusal
// as its own case so the log line and the event code say what was wrong.
func validateLeaf(leaf *x509.Certificate, names []string, now time.Time) error {
	if leaf.IsCA {
		return fmt.Errorf("%w: leaf is a CA certificate", ErrInvalidGeneration)
	}
	if len(leaf.DNSNames) == 0 {
		return fmt.Errorf("%w: leaf carries no DNS names", ErrInvalidGeneration)
	}
	if len(names) == 0 {
		return fmt.Errorf("%w: no certificate name to check the leaf against", ErrInvalidGeneration)
	}
	for _, want := range names {
		if !slices.ContainsFunc(leaf.DNSNames, func(name string) bool { return strings.EqualFold(name, want) }) {
			return fmt.Errorf("%w: leaf is not valid for %q", ErrInvalidGeneration, want)
		}
	}
	if len(leaf.ExtKeyUsage) > 0 && !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		return fmt.Errorf("%w: leaf is not valid for TLS server authentication", ErrInvalidGeneration)
	}
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("%w: leaf is not valid until %s", ErrInvalidGeneration, leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if !now.Before(leaf.NotAfter) {
		return fmt.Errorf("%w: leaf expired at %s", ErrInvalidGeneration, leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

// errNotConfigured covers "nothing to serve yet" without pretending it is a
// certificate problem: the listener answers a handshake error and the
// console shows the state.
var errNotConfigured = errors.New("certs: no usable origin certificate yet")
