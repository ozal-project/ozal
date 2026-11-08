// Package assertion issues the identity assertion an application can verify
// on every request: a short-lived JWS, signed by this edge with Ed25519, that
// names the person the edge just authorized for that application.
//
// The identity headers (X-Auth-Request-*) are only as trustworthy as the hop
// between the edge and the application. An assertion is not: an application
// that pins this edge's public key accepts it from any hop, and rejects a
// forged, replayed or expired one. Routes opt in with identity_assertion.
//
// Claims and Check are the pure core; Signer and the key file are the shell.
package assertion

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// Header carries the assertion to the application. The trust stage strips
// any client-supplied copy, in every spelling.
const Header = "X-Ozal-Assertion"

// Lifetime bounds how long an assertion is accepted. It covers one request,
// with room for clock skew, and nothing more.
const Lifetime = 60 * time.Second

// Claims is what the edge asserts. JSON names follow RFC 7519.
type Claims struct {
	Issuer   string   `json:"iss"`
	Audience string   `json:"aud"`
	Subject  string   `json:"sub"`
	Email    string   `json:"email,omitempty"`
	Name     string   `json:"name,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	IssuedAt int64    `json:"iat"`
	Expiry   int64    `json:"exp"`
	ID       string   `json:"jti"`
}

// Person is the authorized identity the claims describe.
type Person struct {
	Subject, Email, Name string
	Groups               []string
}

// Build returns the claims for one request. It never asserts an anonymous
// visitor: an empty subject is an error.
func Build(issuer, audience string, p Person, now time.Time, id string) (Claims, error) {
	if issuer == "" || audience == "" || id == "" {
		return Claims{}, errors.New("assertion: issuer, audience and id are required")
	}
	if p.Subject == "" {
		return Claims{}, errors.New("assertion: no authenticated person to assert")
	}
	groups := slices.Clone(p.Groups)
	slices.Sort(groups)
	groups = slices.Compact(groups)
	return Claims{
		Issuer: issuer, Audience: audience, Subject: p.Subject, Email: p.Email, Name: p.Name, Groups: groups,
		IssuedAt: now.Unix(), Expiry: now.Add(Lifetime).Unix(), ID: id,
	}, nil
}

// Rejection codes are stable for tests, logs and metrics.
const (
	RejectIssuer   = "issuer"
	RejectAudience = "audience"
	RejectExpired  = "expired"
	RejectNotYet   = "not_yet_valid"
	RejectLifetime = "lifetime"
	RejectSubject  = "subject"
	RejectID       = "id"
)

// Check applies the rules a verifier must apply after the signature: the
// expected issuer and audience, a subject and an ID, the validity window with
// at most skew of clock difference, and a lifetime no longer than Lifetime.
// It returns "" when the claims are acceptable, or a rejection code.
func Check(c Claims, issuer, audience string, now time.Time, skew time.Duration) string {
	switch {
	case c.Issuer == "" || c.Issuer != issuer:
		return RejectIssuer
	case c.Audience == "" || c.Audience != audience:
		return RejectAudience
	case strings.TrimSpace(c.Subject) == "":
		return RejectSubject
	case strings.TrimSpace(c.ID) == "":
		return RejectID
	case c.Expiry-c.IssuedAt <= 0 || time.Duration(c.Expiry-c.IssuedAt)*time.Second > Lifetime:
		return RejectLifetime
	case now.Add(skew).Unix() < c.IssuedAt:
		return RejectNotYet
	case now.Add(-skew).Unix() >= c.Expiry:
		return RejectExpired
	}
	return ""
}
