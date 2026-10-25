package certs

import (
	"fmt"
	"strings"
)

// The manager serves one certificate. Each of its names is either a plain
// hostname or a single-level wildcard such as "*.example.com"; a wildcard
// does not cover its own base, so serving "example.com" too takes a second
// name on the same certificate. The first name is the certificate's identity
// (storage key, events). This file is the pure part of those rules: every
// decision that depends on a name being a wildcard lives here, so the shells
// (listener, ACME, storage) never parse a "*" themselves.

const wildcardPrefix = "*."

// wildcardDirPrefix is the storage key prefix of a wildcard; a plain name that
// starts with it is refused so it can never alias a wildcard's directory.
const wildcardDirPrefix = "_wildcard."

// IsWildcard reports whether name is a "*.base" certificate name.
func IsWildcard(name string) bool { return strings.HasPrefix(name, wildcardPrefix) }

// baseOf returns the name without its wildcard label, or name itself.
func baseOf(name string) string { return strings.TrimPrefix(name, wildcardPrefix) }

// ValidateName accepts a plain LDH hostname, or "*." followed by one with at
// least two labels. A wildcard over a bare suffix ("*.com") is refused: it is
// never what an operator means and a CA would reject it anyway.
func ValidateName(name string) error {
	base := baseOf(name)
	if base == "" {
		return fmt.Errorf("certs: empty certificate name")
	}
	if len(name) > 253 {
		return fmt.Errorf("certs: %q exceeds the DNS name length limit", name)
	}
	labels := strings.Split(base, ".")
	if IsWildcard(name) && len(labels) < 2 {
		return fmt.Errorf("certs: wildcard %q must sit above a registered domain", name)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("certs: %q is not a valid certificate name", name)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("certs: %q is not a valid certificate name", name)
			}
		}
	}
	return nil
}

// Covers reports whether a certificate for certName is valid for serverName.
// A wildcard covers exactly one additional label (RFC 6125 section 6.4.3):
// "*.example.com" covers "a.example.com" but neither "example.com" nor
// "a.b.example.com".
func Covers(certName, serverName string) bool {
	if serverName == "" || certName == "" {
		return false
	}
	if !IsWildcard(certName) {
		return strings.EqualFold(certName, serverName)
	}
	suffix := "." + strings.ToLower(baseOf(certName))
	server := strings.ToLower(serverName)
	label, ok := strings.CutSuffix(server, suffix)
	return ok && label != "" && !strings.Contains(label, ".")
}

// CoversAny reports whether any of a certificate's names covers serverName.
func CoversAny(certNames []string, serverName string) bool {
	for _, name := range certNames {
		if Covers(name, serverName) {
			return true
		}
	}
	return false
}

// ProbeName returns a concrete hostname the certificate must be valid for.
// Chain verification needs one: x509 refuses to verify a pattern as a name.
func ProbeName(certName string) string {
	if IsWildcard(certName) {
		return "wildcard-probe." + baseOf(certName)
	}
	return certName
}

// ChallengeFQDN is where the DNS-01 TXT record goes. A wildcard shares the
// challenge of its base domain: ACME has no "*" in the record name.
func ChallengeFQDN(certName string) string { return "_acme-challenge." + baseOf(certName) }

// StorageKey maps a certificate name to a safe single path segment: "*" is a
// legal file name on Linux but hostile to every shell.
func StorageKey(certName string) string {
	if IsWildcard(certName) {
		return wildcardDirPrefix + baseOf(certName)
	}
	return certName
}
