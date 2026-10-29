package auth

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

// FuzzSafeReturnTo checks the open-redirect guard: anything returned must be
// an absolute HTTP(S) URL without userinfo on an allowed host, and feeding it
// back must be a fixed point.
func FuzzSafeReturnTo(f *testing.F) {
	gate := &Gate{allowedHosts: map[string]bool{"app.example.com": true}}
	f.Add("https://app.example.com/dashboard?x=1")
	f.Add("https://evil.example.com/")
	f.Add("//app.example.com/path")
	f.Add("javascript://app.example.com/%0aalert(1)")
	f.Add("")

	f.Fuzz(func(t *testing.T, raw string) {
		got := gate.safeReturnTo(raw)
		if got == "/" {
			return
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("safeReturnTo(%q) = %q, unparsable: %v", raw, got, err)
		}
		if u.User != nil {
			t.Fatalf("safeReturnTo(%q) returned userinfo: %q", raw, got)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("safeReturnTo(%q) returned scheme %q", raw, u.Scheme)
		}
		host, err := config.NormalizeHost(u.Host)
		if err != nil || !gate.allowedHosts[host] {
			t.Fatalf("safeReturnTo(%q) returned disallowed host %q", raw, u.Host)
		}
		if again := gate.safeReturnTo(got); again != got {
			t.Fatalf("safeReturnTo is not idempotent: %q -> %q", got, again)
		}
	})
}

// FuzzAuthorize compares the decision with the access-policy contract: an
// identity allowlist requires a verified email and an email or exact domain
// match; every required group must match exactly. Empty policy allows any
// already-authenticated principal.
func FuzzAuthorize(f *testing.F) {
	f.Add("alice@example.com", "alice@example.com", "", "eng", "eng", true)
	f.Add("eve@evilexample.com", "", "example.com", "eng", "eng", true)
	f.Add("alice@example.com", "alice@example.com", "", "", "", false)
	f.Add("alice@example.com", "", "", "", "eng", true)
	f.Add("not-an-email", "", "example.com", "", "", true)

	f.Fuzz(func(t *testing.T, email, allowedEmails, allowedDomains, groups, requiredGroups string, verified bool) {
		policy := config.AccessPolicy{
			AllowedEmails:  fuzzPolicyList(allowedEmails),
			AllowedDomains: fuzzPolicyList(allowedDomains),
			RequiredGroups: fuzzPolicyList(requiredGroups),
		}
		principal := Principal{Email: email, EmailVerified: verified, Groups: fuzzPolicyList(groups)}
		got := Authorize(principal, policy)

		want := expectedAuthorization(principal, policy)
		if got.Allowed != want.Allowed || got.Code != want.Code {
			t.Fatalf("Authorize(%+v, %+v) = {allowed:%v code:%q}, want {allowed:%v code:%q}",
				principal, policy, got.Allowed, got.Code, want.Allowed, want.Code)
		}
		if got.Reason == "" {
			t.Fatal("Authorize returned an empty reason")
		}
	})
}

func fuzzPolicyList(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\x00")
}

// expectedAuthorization is an intentionally small model of the documented
// policy semantics. It does not call Authorize's matching helpers.
func expectedAuthorization(p Principal, policy config.AccessPolicy) Decision {
	hasIdentityAllowlist := len(policy.AllowedEmails) > 0 || len(policy.AllowedDomains) > 0
	if hasIdentityAllowlist {
		if !p.EmailVerified {
			return Decision{Code: CodeEmailNotVerified}
		}
		matched := false
		for _, allowed := range policy.AllowedEmails {
			matched = matched || strings.EqualFold(p.Email, allowed)
		}
		if !matched {
			if at := strings.LastIndex(p.Email, "@"); at >= 0 {
				for _, allowed := range policy.AllowedDomains {
					matched = matched || strings.EqualFold(p.Email[at+1:], allowed)
				}
			}
		}
		if !matched {
			if len(policy.AllowedDomains) > 0 {
				return Decision{Code: CodeDomainNotAllowed}
			}
			return Decision{Code: CodeEmailNotAllowed}
		}
	}
	for _, required := range policy.RequiredGroups {
		found := false
		for _, group := range p.Groups {
			if group == required {
				found = true
				break
			}
		}
		if !found {
			return Decision{Code: CodeMissingRequiredGroup}
		}
	}
	return Decision{Allowed: true, Code: CodeAllowed}
}
