package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The client secret is a literal here on purpose: the test proves the command
// never prints the auth section, whatever form the secret takes.
const policyTestSecret = "literal-secret-must-not-leak"

const policyTestConfig = `{
  "listen": "127.0.0.1:18888",
  "auth": {
    "issuer": "https://idp.example.com",
    "client_id": "switchboard-test",
    "client_secret": "` + policyTestSecret + `",
    "redirect_url": "https://auth.example.com/_/auth/callback",
    "cookie_domain": ".example.com"
  },
  "routes": [
    { "host": "www.example.com", "target": "http://127.0.0.1:18001" },
    { "host": "app.example.com", "target": "http://127.0.0.1:18002",
      "access": { "allowed_domains": ["example.com"], "required_groups": ["engineering", "oncall"] } },
    { "host": "ops.example.com", "target": "http://127.0.0.1:18003",
      "access": { "allowed_emails": ["Ana@Example.com"] } },
    { "host": "legacy.example.com", "target": "http://127.0.0.1:18004", "auth_required": true },
    { "host": "open.example.com", "target": "http://127.0.0.1:18005", "access": {} }
  ]
}`

func runExplain(t *testing.T, args ...string) (string, error) {
	t.Helper()
	path := writeValidateConfig(t, policyTestConfig)
	var out bytes.Buffer
	err := runPolicy(context.Background(), append([]string{"explain", "-config", path}, args...), &out)
	return out.String(), err
}

func TestPolicyExplainDecisions(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		decision string
		code     string
	}{
		{"public route without identity", []string{"-host", "www.example.com"}, "allowed", "allowed"},
		{"public route with -no-session", []string{"-host", "www.example.com", "-no-session"}, "allowed", "allowed"},
		{"all groups present", []string{"-host", "app.example.com", "-email", "ana@example.com", "-verified", "-groups", "engineering,oncall,support"}, "allowed", "allowed"},
		{"groups are AND, not OR", []string{"-host", "app.example.com", "-email", "ana@example.com", "-verified", "-groups", "engineering"}, "denied", "missing_required_group"},
		{"group names are case-sensitive", []string{"-host", "app.example.com", "-email", "ana@example.com", "-verified", "-groups", "Engineering,oncall"}, "denied", "missing_required_group"},
		{"allowlist needs a verified email", []string{"-host", "app.example.com", "-email", "ana@example.com", "-groups", "engineering,oncall"}, "denied", "email_not_verified"},
		{"domain is exact, never a suffix", []string{"-host", "app.example.com", "-email", "eve@evilexample.com", "-verified", "-groups", "engineering,oncall"}, "denied", "domain_not_allowed"},
		{"domain match ignores case", []string{"-host", "app.example.com", "-email", "ana@EXAMPLE.COM", "-verified", "-groups", "engineering, oncall"}, "allowed", "allowed"},
		{"email allowlist ignores case", []string{"-host", "ops.example.com", "-email", "ana@example.com", "-verified"}, "allowed", "allowed"},
		{"email not on the allowlist", []string{"-host", "ops.example.com", "-email", "bob@example.com", "-verified"}, "denied", "email_not_allowed"},
		{"legacy auth_required admits any principal", []string{"-host", "legacy.example.com", "-email", "x@other.org"}, "allowed", "allowed"},
		{"empty access policy admits any principal", []string{"-host", "open.example.com", "-email", "x@other.org"}, "allowed", "allowed"},
		{"no session stops at the gate", []string{"-host", "app.example.com", "-no-session"}, "denied", "authentication_required"},
		{"host is normalized like routing", []string{"-host", "APP.Example.com.", "-no-session"}, "denied", "authentication_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runExplain(t, tt.args...)
			if err != nil {
				t.Fatalf("runPolicy: %v\n%s", err, out)
			}
			for _, want := range []string{"decision: " + tt.decision + "\n", "code: " + tt.code + "\n", "scope: "} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestPolicyExplainRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing host", nil},
		{"unknown host", []string{"-host", "nope.example.com", "-no-session"}},
		{"port is part of the routing key", []string{"-host", "app.example.com:443", "-no-session"}},
		{"protected route needs an identity choice", []string{"-host", "app.example.com"}},
		{"-no-session with -email", []string{"-host", "app.example.com", "-no-session", "-email", "ana@example.com"}},
		{"-groups without -email", []string{"-host", "www.example.com", "-groups", "ops"}},
		{"invalid -expect", []string{"-host", "www.example.com", "-expect", "maybe"}},
		{"-expect mismatch", []string{"-host", "app.example.com", "-no-session", "-expect", "allowed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if out, err := runExplain(t, tt.args...); err == nil {
				t.Fatalf("runPolicy succeeded; want an error\n%s", out)
			}
		})
	}
	if _, err := runExplain(t, "-host", "app.example.com", "-no-session", "-expect", "denied"); err != nil {
		t.Errorf("-expect denied on a denied decision: %v", err)
	}
	if err := runPolicy(context.Background(), []string{"simulate"}, &bytes.Buffer{}); err == nil {
		t.Error("unknown policy subcommand accepted")
	}
}

func TestPolicyExplainJSON(t *testing.T) {
	out, err := runExplain(t, "-host", "app.example.com", "-email", "ana@example.com", "-verified", "-groups", "support", "-json")
	if err != nil {
		t.Fatalf("runPolicy: %v", err)
	}
	var got struct {
		Route     string `json:"route"`
		Protected bool   `json:"protected"`
		Policy    struct {
			AllowedDomains []string `json:"allowed_domains"`
			RequiredGroups []string `json:"required_groups"`
		} `json:"policy"`
		Principal struct {
			Email         string   `json:"email"`
			EmailVerified bool     `json:"email_verified"`
			Groups        []string `json:"groups"`
		} `json:"principal"`
		Decision struct {
			Allowed bool   `json:"allowed"`
			Code    string `json:"code"`
			Reason  string `json:"reason"`
		} `json:"decision"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Route != "app.example.com" || !got.Protected || got.Scope != "authorization_only" ||
		got.Decision.Allowed || got.Decision.Code != "missing_required_group" || got.Decision.Reason == "" ||
		got.Principal.Email != "ana@example.com" || !got.Principal.EmailVerified ||
		len(got.Principal.Groups) != 1 || len(got.Policy.AllowedDomains) != 1 || len(got.Policy.RequiredGroups) != 2 {
		t.Errorf("unexpected JSON report: %+v", got)
	}

	gate, err := runExplain(t, "-host", "app.example.com", "-no-session", "-json")
	if err != nil {
		t.Fatalf("runPolicy: %v", err)
	}
	if !strings.Contains(gate, `"scope": "gate_only"`) || strings.Contains(gate, `"principal"`) {
		t.Errorf("gate JSON should have gate_only scope and no principal:\n%s", gate)
	}
}

func TestPolicyExplainNeverPrintsAuthSection(t *testing.T) {
	for _, args := range [][]string{
		{"-host", "app.example.com", "-email", "ana@example.com", "-verified", "-groups", "engineering,oncall"},
		{"-host", "app.example.com", "-no-session", "-json"},
		{"-host", "www.example.com", "-json"},
	} {
		out, err := runExplain(t, args...)
		if err != nil {
			t.Fatalf("runPolicy %v: %v", args, err)
		}
		for _, leak := range []string{policyTestSecret, "switchboard-test", "idp.example.com"} {
			if strings.Contains(out, leak) {
				t.Errorf("output for %v contains %q:\n%s", args, leak, out)
			}
		}
	}
}
