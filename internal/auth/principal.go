package auth

// Principal is the identity Switchboard trusts for a request, regardless of
// how it was established — a verified OIDC session today, potentially a
// simulated identity later. Authorize only ever sees this type, so adding a
// second authentication strategy never touches authorization logic.
type Principal struct {
	Subject       string
	Email         string
	EmailVerified bool
	Groups        []string
}
