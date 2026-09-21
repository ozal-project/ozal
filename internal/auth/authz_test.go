package auth

import (
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name     string
		p        Principal
		policy   config.AccessPolicy
		wantCode DecisionCode
		wantOK   bool
	}{
		{
			name:     "no policy at all allows any authenticated principal",
			p:        Principal{Email: "anyone@example.com", EmailVerified: false},
			policy:   config.AccessPolicy{},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name:     "email allowlist match",
			p:        Principal{Email: "alice@example.com", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name:     "email allowlist match is case-insensitive",
			p:        Principal{Email: "Alice@Example.com", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name:     "email not in allowlist",
			p:        Principal{Email: "mallory@example.com", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}},
			wantCode: CodeEmailNotAllowed,
			wantOK:   false,
		},
		{
			name:     "domain allowlist match",
			p:        Principal{Email: "bob@example.com", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name:     "domain allowlist match is case-insensitive",
			p:        Principal{Email: "bob@Example.COM", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name:     "domain allowlist rejects a suffix lookalike domain",
			p:        Principal{Email: "eve@evilexample.com", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			wantCode: CodeDomainNotAllowed,
			wantOK:   false,
		},
		{
			name:     "domain allowlist rejects a subdomain not listed exactly",
			p:        Principal{Email: "eve@sub.example.com", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			wantCode: CodeDomainNotAllowed,
			wantOK:   false,
		},
		{
			name: "email and domain allowlists are OR'd",
			p:    Principal{Email: "carol@other.com", EmailVerified: true},
			policy: config.AccessPolicy{
				AllowedEmails:  []string{"carol@other.com"},
				AllowedDomains: []string{"example.com"},
			},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name:     "unverified email is rejected even if it matches the allowlist",
			p:        Principal{Email: "alice@example.com", EmailVerified: false},
			policy:   config.AccessPolicy{AllowedEmails: []string{"alice@example.com"}},
			wantCode: CodeEmailNotVerified,
			wantOK:   false,
		},
		{
			name:     "unverified email is rejected even if domain matches the allowlist",
			p:        Principal{Email: "alice@example.com", EmailVerified: false},
			policy:   config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			wantCode: CodeEmailNotVerified,
			wantOK:   false,
		},
		{
			name:     "unverified email is fine when no identity allowlist is configured",
			p:        Principal{Email: "alice@example.com", EmailVerified: false, Groups: []string{"eng"}},
			policy:   config.AccessPolicy{RequiredGroups: []string{"eng"}},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name: "required group present",
			p:    Principal{Email: "alice@example.com", EmailVerified: true, Groups: []string{"eng", "oncall"}},
			policy: config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"eng"},
			},
			wantCode: CodeAllowed,
			wantOK:   true,
		},
		{
			name: "missing one of several required groups",
			p:    Principal{Email: "alice@example.com", EmailVerified: true, Groups: []string{"eng"}},
			policy: config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"eng", "oncall"},
			},
			wantCode: CodeMissingRequiredGroup,
			wantOK:   false,
		},
		{
			name: "group match is case-sensitive",
			p:    Principal{Email: "alice@example.com", EmailVerified: true, Groups: []string{"Engineering"}},
			policy: config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"engineering"},
			},
			wantCode: CodeMissingRequiredGroup,
			wantOK:   false,
		},
		{
			name: "identity allowed but groups required and principal has none",
			p:    Principal{Email: "alice@example.com", EmailVerified: true},
			policy: config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"eng"},
			},
			wantCode: CodeMissingRequiredGroup,
			wantOK:   false,
		},
		{
			name: "identity rejected takes precedence over an unrelated group failure",
			p:    Principal{Email: "mallory@other.com", EmailVerified: true},
			policy: config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"eng"},
			},
			wantCode: CodeDomainNotAllowed,
			wantOK:   false,
		},
		{
			name:     "email with no @ never matches a domain allowlist",
			p:        Principal{Email: "not-an-email", EmailVerified: true},
			policy:   config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			wantCode: CodeDomainNotAllowed,
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Authorize(tt.p, tt.policy)
			if got.Allowed != tt.wantOK {
				t.Errorf("Allowed = %v, want %v (code=%s)", got.Allowed, tt.wantOK, got.Code)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %s, want %s", got.Code, tt.wantCode)
			}
			if got.Reason == "" {
				t.Error("Reason should never be empty")
			}
		})
	}
}

func TestPolicyAuthorizerImplementsAuthorizer(t *testing.T) {
	var _ Authorizer = PolicyAuthorizer{}
}
