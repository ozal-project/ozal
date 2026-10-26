package auth

import (
	"encoding/base64"
	"strings"
)

// OIDCSubject is an unambiguous, issuer-qualified identity. An upstream sub
// cannot impersonate a native sb: subject or a subject from another issuer.
func OIDCSubject(issuer, subject string) string {
	return "oidc:" + base64.RawURLEncoding.EncodeToString([]byte(issuer)) + "." + base64.RawURLEncoding.EncodeToString([]byte(subject))
}

// IsOIDCSubject accepts only canonical subjects of this configured issuer.
// Pre-namespace sessions deliberately require another login; no email linking.
func IsOIDCSubject(issuer, subject string) bool {
	prefix := "oidc:" + base64.RawURLEncoding.EncodeToString([]byte(issuer)) + "."
	encoded, ok := strings.CutPrefix(subject, prefix)
	if !ok || issuer == "" {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(raw) > 0 && base64.RawURLEncoding.EncodeToString(raw) == encoded
}
