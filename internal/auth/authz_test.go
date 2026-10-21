package auth

import (
	"testing"
	"time"

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

func TestFreshStepUp(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	policy := config.AccessPolicy{MaxAuthAgeMinutes: 15}
	for name, tc := range map[string]struct {
		authTime time.Time
		policy   config.AccessPolicy
		allowed  bool
	}{
		"no limit, old login":    {now.Add(-48 * time.Hour), config.AccessPolicy{}, true},
		"recent login":           {now.Add(-5 * time.Minute), policy, true},
		"exactly at the limit":   {now.Add(-15 * time.Minute), policy, true},
		"just past the limit":    {now.Add(-15*time.Minute - time.Second), policy, false},
		"no recorded login time": {time.Time{}, policy, false},
	} {
		t.Run(name, func(t *testing.T) {
			d := Fresh(Principal{Subject: "s", AuthTime: tc.authTime}, tc.policy, now)
			if d.Allowed != tc.allowed {
				t.Fatalf("Fresh = %+v, want allowed=%v", d, tc.allowed)
			}
			if !d.Allowed && d.Code != CodeReauthRequired {
				t.Fatalf("code = %q", d.Code)
			}
		})
	}
}

func TestFreshDeviceBound(t *testing.T) {
	now := time.Now()
	policy := config.AccessPolicy{RequireDeviceBound: true}
	if d := Fresh(Principal{AuthTime: now}, policy, now); d.Allowed || d.Code != CodeDeviceBoundRequired {
		t.Fatalf("synced passkey = %+v", d)
	}
	if d := Fresh(Principal{AuthTime: now, DeviceBound: true}, policy, now); !d.Allowed {
		t.Fatalf("device-bound passkey = %+v", d)
	}
	both := config.AccessPolicy{RequireDeviceBound: true, MaxAuthAgeMinutes: 15}
	if d := Fresh(Principal{AuthTime: now.Add(-time.Hour), DeviceBound: true}, both, now); d.Code != CodeReauthRequired {
		t.Fatalf("device-bound but stale = %+v", d)
	}
}

func TestFreshMethods(t *testing.T) {
	now := time.Now()
	policy := config.AccessPolicy{Methods: []string{MethodPasskey}}
	if d := Fresh(Principal{AuthTime: now, Method: MethodOIDC}, policy, now); d.Allowed || d.Code != CodeMethodNotAllowed {
		t.Fatalf("OIDC session on a passkey route = %+v", d)
	}
	if d := Fresh(Principal{AuthTime: now, Method: ""}, policy, now); d.Allowed {
		t.Fatalf("session without a method = %+v", d)
	}
	if d := Fresh(Principal{AuthTime: now, Method: MethodPasskey}, policy, now); !d.Allowed {
		t.Fatalf("passkey session = %+v", d)
	}
	if d := Fresh(Principal{AuthTime: now, Method: MethodOIDC}, config.AccessPolicy{}, now); !d.Allowed {
		t.Fatalf("no methods restriction = %+v", d)
	}
}
