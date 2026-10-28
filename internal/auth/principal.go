package auth

import (
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
)

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
	// DeviceBound reports that the login used a passkey that cannot be
	// synced to other devices.
	DeviceBound bool
	// UserVerified is the authenticator's WebAuthn user-verification flag.
	// Nil means the authentication method or a legacy session did not record it.
	UserVerified *bool
	// DeviceFirstSeen reports that this passkey was newly observed for the
	// account during registration or its first successful login. Nil means
	// the method or an older credential did not record this history.
	DeviceFirstSeen *bool
}

// Login methods recorded in a session (its amr); the same names as
// access.methods.
const (
	MethodPasskey = config.MethodPasskey
	MethodOIDC    = config.MethodOIDC
)
