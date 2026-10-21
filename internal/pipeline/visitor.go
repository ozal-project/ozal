package pipeline

import (
	"context"
	"net/http"
	"strings"
)

// The visitor's identity travels from the tunnel edge to the connector in
// these headers, inside the mutually authenticated tunnel, the same way
// VisitorIPHeader carries the address. Only TrustAuthenticatedTunnel believes
// them; every stage strips them as client input, and they never reach an
// application: the connector turns them back into the identity headers.
const (
	VisitorUserHeader  = "X-Switchboard-Visitor-User"
	VisitorEmailHeader = "X-Switchboard-Visitor-Email"
)

// maxVisitorField bounds each asserted value. OIDC subjects are at most 255
// ASCII characters and email addresses at most 254; anything longer did not
// come from the edge.
const maxVisitorField = 255

// Visitor is the authenticated identity an access decision allowed: the
// edge records it after Authorize, the connector after reading the edge's
// assertion. A request without one is anonymous.
type Visitor struct {
	Subject string
	Email   string
}

type visitorKey struct{}

// WithVisitor records the authenticated visitor for the rest of the request.
func WithVisitor(ctx context.Context, v Visitor) context.Context {
	return context.WithValue(ctx, visitorKey{}, v)
}

// VisitorFrom returns the visitor recorded by WithVisitor.
func VisitorFrom(ctx context.Context) (Visitor, bool) {
	v, ok := ctx.Value(visitorKey{}).(Visitor)
	return v, ok && v.Subject != ""
}

// SetIdentityHeaders writes the identity an application trusts. The caller
// has already removed any client-supplied copies (the trust stage does).
func SetIdentityHeaders(h http.Header, v Visitor) {
	if v.Email != "" {
		h.Set(IdentityEmailHeader, v.Email)
	}
	if v.Subject != "" {
		h.Set(IdentityUserHeader, v.Subject)
	}
}

// parseVisitorIdentity validates the edge's assertion. users and emails are
// every value of VisitorUserHeader and VisitorEmailHeader, as headerValues
// returns them (nil when a "_" spelling was present).
//
// The contract, which the tests in visitor_test.go pin down:
//   - no user values at all: anonymous, (Visitor{}, false), even if an email
//     is present (an email without a subject identifies nobody);
//   - exactly one user value and at most one email value, otherwise refuse;
//   - every value non-empty, at most maxVisitorField bytes, with no
//     surrounding spaces and no control characters (bytes < 0x20 and 0x7f),
//     otherwise refuse.
//
// Refusing means (Visitor{}, false): the application then sees an anonymous
// request, which can only deny more, never allow more.
func parseVisitorIdentity(users, emails []string) (Visitor, bool) {
	if len(users) != 1 || len(emails) > 1 {
		return Visitor{}, false
	}
	v := Visitor{Subject: users[0]}
	if !validVisitorField(v.Subject) {
		return Visitor{}, false
	}
	if len(emails) == 1 {
		if !validVisitorField(emails[0]) {
			return Visitor{}, false
		}
		v.Email = emails[0]
	}
	return v, true
}

// validVisitorField checks one asserted value byte by byte: control
// characters are ASCII, so there is no need to decode UTF-8 runes.
func validVisitorField(s string) bool {
	if s == "" || len(s) > maxVisitorField || strings.TrimSpace(s) != s {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}
