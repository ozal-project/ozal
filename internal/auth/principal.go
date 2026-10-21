package auth

import "time"

// Principal is the identity Switchboard trusts for a request, regardless of
// how it was established — a verified OIDC session today, potentially a
// simulated identity later. Authorize only ever sees this type, so adding a
// second authentication strategy never touches authorization logic.
type Principal struct {
	Subject       string
	Email         string
	EmailVerified bool
	Groups        []string
	// AuthTime is when this identity was last proven by a login, and Method
	// how (MethodPasskey, MethodOIDC). Step-up policies read them; they are
	// never sent upstream.
	AuthTime time.Time
	Method   string
}

// Login methods recorded in a session (its amr).
const (
	MethodPasskey = "passkey"
	MethodOIDC    = "oidc"
)
