package auth

import (
	"strings"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// DecisionCode identifies why an authorization decision came out the way it
// did. It's the stable part of a Decision — safe to compare in tests, log,
// or key a dashboard metric on. Reason is the human-facing sibling and may
// change wording freely.
type DecisionCode string

const (
	CodeAuthenticationRequired DecisionCode = "authentication_required"
	CodeEmailNotVerified       DecisionCode = "email_not_verified"
	CodeEmailNotAllowed        DecisionCode = "email_not_allowed"
	CodeDomainNotAllowed       DecisionCode = "domain_not_allowed"
	CodeMissingRequiredGroup   DecisionCode = "missing_required_group"
	CodeAllowed                DecisionCode = "allowed"
)

// Decision is the outcome of evaluating a Principal against an
// AccessPolicy. Code is what code should branch on; Reason is what a human
// reads in a log line or an error page.
type Decision struct {
	Allowed bool
	Code    DecisionCode
	Reason  string
}

// Authorizer decides whether an already-authenticated Principal may reach a
// route governed by policy. It has no side effects and no knowledge of
// HTTP, sessions, or OIDC — CodeAuthenticationRequired is deliberately not
// producible by an Authorizer: the caller only reaches this decision once
// Authenticate has already confirmed a Principal exists.
type Authorizer interface {
	Authorize(p Principal, policy config.AccessPolicy) Decision
}

// PolicyAuthorizer is the default, pure Authorizer implementation.
type PolicyAuthorizer struct{}

func (PolicyAuthorizer) Authorize(p Principal, policy config.AccessPolicy) Decision {
	return Authorize(p, policy)
}

// Authorize evaluates:
//
//	(email allowed OR domain allowed OR no identity allowlist configured)
//	AND
//	(principal carries every required group)
//
// If an identity allowlist (AllowedEmails or AllowedDomains) is configured,
// the principal's email must also be verified — an unverified email is an
// unauthenticated claim by the provider, not something Switchboard should
// authorize on.
func Authorize(p Principal, policy config.AccessPolicy) Decision {
	hasIdentityAllowlist := len(policy.AllowedEmails) > 0 || len(policy.AllowedDomains) > 0

	if hasIdentityAllowlist {
		if !p.EmailVerified {
			return Decision{
				Code:   CodeEmailNotVerified,
				Reason: "provider did not verify this account's email address",
			}
		}

		switch {
		case emailAllowed(p.Email, policy.AllowedEmails):
			// identity check passed, fall through to group check
		case domainAllowed(p.Email, policy.AllowedDomains):
			// identity check passed, fall through to group check
		case len(policy.AllowedDomains) > 0:
			return Decision{
				Code:   CodeDomainNotAllowed,
				Reason: "account's email domain is not on the allowed list",
			}
		default:
			return Decision{
				Code:   CodeEmailNotAllowed,
				Reason: "account's email is not on the allowed list",
			}
		}
	}

	for _, required := range policy.RequiredGroups {
		if !hasGroup(p.Groups, required) {
			return Decision{
				Code:   CodeMissingRequiredGroup,
				Reason: "account is missing required group " + required,
			}
		}
	}

	return Decision{Allowed: true, Code: CodeAllowed, Reason: "allowed"}
}

func emailAllowed(email string, allowed []string) bool {
	email = strings.ToLower(email)
	for _, a := range allowed {
		if email == strings.ToLower(a) {
			return true
		}
	}
	return false
}

// domainAllowed extracts the domain from the part of email after the last
// "@" and compares it exactly against allowed — never with strings.HasSuffix,
// which would let "evilexample.com" pass an allowlist for "example.com".
func domainAllowed(email string, allowed []string) bool {
	at := strings.LastIndex(email, "@")
	if at == -1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, a := range allowed {
		if domain == strings.ToLower(a) {
			return true
		}
	}
	return false
}

// hasGroup is case-sensitive: group names are provider-defined identifiers,
// not human text, so "Engineering" and "engineering" are different groups.
func hasGroup(groups []string, want string) bool {
	for _, g := range groups {
		if g == want {
			return true
		}
	}
	return false
}
