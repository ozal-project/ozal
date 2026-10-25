package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/policycontext"
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
      "access": { "allowed_domains": ["example.com"], "required_groups": ["engineering", "oncall"] },
      "resource": { "owner": "platform", "data_class": "account_data", "sensitivity": "restricted", "risky_actions": ["POST", "DELETE"] } },
    { "host": "ops.example.com", "target": "http://127.0.0.1:18003",
      "access": { "allowed_emails": ["Ana@Example.com"] } },
    { "host": "legacy.example.com", "target": "http://127.0.0.1:18004", "auth_required": true },
    { "host": "open.example.com", "target": "http://127.0.0.1:18005", "access": {} }
  ]
}`

const contextPolicyTestConfig = `{
  "listen": "127.0.0.1:18888",
  "auth": {
    "issuer": "https://idp.example.com",
    "client_id": "switchboard-test",
    "client_secret": "` + policyTestSecret + `",
    "redirect_url": "https://auth.example.com/_/auth/callback",
    "cookie_domain": ".example.com",
    "passkeys": {"rp_id": "example.com", "login_url": "https://auth.example.com"}
  },
  "routes": [{
    "host": "app.example.com",
    "target": "http://127.0.0.1:18002",
    "access": {"context_rule": {
      "when": {"session_method": "oidc", "resource_sensitivity": "restricted"},
      "then": "step_up"
    }},
    "resource": {"owner": "platform", "data_class": "account_data", "sensitivity": "restricted"}
  }, {
    "host": "new-device.example.com",
    "target": "http://127.0.0.1:18003",
    "access": {"context_rule": {"when": {"device_first_seen": true}, "then": "step_up"}}
  }]
}`

const riskyContextPolicyTestConfig = `{
  "listen": "127.0.0.1:18888",
  "auth": {
    "issuer": "https://idp.example.com",
    "client_id": "switchboard-test",
    "client_secret": "` + policyTestSecret + `",
    "redirect_url": "https://auth.example.com/_/auth/callback",
    "cookie_domain": ".example.com"
  },
  "routes": [{
    "host": "app.example.com",
    "target": "http://127.0.0.1:18002",
    "access": {"context_rule": {"when": {"action_risky": true}, "then": "deny"}},
    "resource": {"owner": "platform", "data_class": "account_data", "sensitivity": "restricted", "risky_actions": ["POST"]}
  }]
}`

func runExplain(t *testing.T, args ...string) (string, error) {
	t.Helper()
	path := writeValidateConfig(t, policyTestConfig)
	var out bytes.Buffer
	err := runPolicy(context.Background(), append([]string{"explain", "-config", path}, args...), &out)
	return out.String(), err
}

func runContextExplain(t *testing.T, args ...string) (string, error) {
	t.Helper()
	path := writeValidateConfig(t, contextPolicyTestConfig)
	var out bytes.Buffer
	err := runPolicy(context.Background(), append([]string{"explain", "-config", path}, args...), &out)
	return out.String(), err
}

func runRiskContextExplain(t *testing.T, args ...string) (string, error) {
	t.Helper()
	path := writeValidateConfig(t, riskyContextPolicyTestConfig)
	var out bytes.Buffer
	err := runPolicy(context.Background(), append([]string{"explain", "-config", path}, args...), &out)
	return out.String(), err
}

func TestPolicyExplainEvaluatesContextRulesWithSuppliedSessionEvidence(t *testing.T) {
	base := []string{"-host", "app.example.com", "-email", "ana@example.com", "-verified"}
	unknown, err := runContextExplain(t, append(base, "-json")...)
	if err != nil {
		t.Fatalf("explain unknown session evidence: %v\n%s", err, unknown)
	}
	if !strings.Contains(unknown, `"code": "context_signal_unknown"`) || !strings.Contains(unknown, "session.method (source: not_recorded)") {
		t.Fatalf("unknown signal was not explained as fail-closed:\n%s", unknown)
	}

	stepUp, err := runContextExplain(t, append(base, "-method", "oidc")...)
	if err != nil || !strings.Contains(stepUp, "decision: step_up\n") || !strings.Contains(stepUp, "code: context_step_up_required\n") || !strings.Contains(stepUp, "resource.sensitivity=\"restricted\"") {
		t.Fatalf("OIDC context rule explanation: err=%v\n%s", err, stepUp)
	}
	if _, err := runContextExplain(t, append(base, "-method", "oidc", "-expect", "step_up")...); err != nil {
		t.Fatalf("-expect step_up: %v", err)
	}

	passkey, err := runContextExplain(t, append(base, "-method", "passkey", "-device-bound", "-json")...)
	if err != nil || !strings.Contains(passkey, `"code": "allowed"`) || !strings.Contains(passkey, "context rule did not match") {
		t.Fatalf("passkey context rule explanation: err=%v\n%s", err, passkey)
	}
	uv, err := runContextExplain(t, append(base, "-method", "passkey", "-user-verified", "true", "-json")...)
	if err != nil || !strings.Contains(uv, `"user_verified": true`) || !strings.Contains(uv, `"source": "simulation_input"`) {
		t.Fatalf("supplied UV evidence was not shown: err=%v\n%s", err, uv)
	}
	if _, err := runContextExplain(t, append(base, "-device-bound")...); err == nil {
		t.Fatal("-device-bound without -method was accepted")
	}
	if _, err := runContextExplain(t, append(base, "-method", "oidc", "-user-verified", "false")...); err == nil {
		t.Fatal("-user-verified with OIDC was accepted")
	}
	if _, err := runContextExplain(t, append(base, "-method", "passkey", "-user-verified", "yes")...); err == nil {
		t.Fatal("invalid -user-verified value was accepted")
	}
	if _, err := runContextExplain(t, "-host", "app.example.com", "-no-session", "-user-verified", "true"); err == nil {
		t.Fatal("-no-session with authenticator evidence was accepted")
	}
}

func TestPolicyExplainSimulatesFirstSeenPasskeyEvidence(t *testing.T) {
	base := []string{"-host", "new-device.example.com", "-email", "ana@example.com", "-verified", "-method", "passkey"}
	unknown, err := runContextExplain(t, append(base, "-json")...)
	if err != nil || !strings.Contains(unknown, `"code": "context_signal_unknown"`) {
		t.Fatalf("unknown first-seen evidence: err=%v\n%s", err, unknown)
	}
	firstSeen, err := runContextExplain(t, append(base, "-device-first-seen", "true", "-json")...)
	if err != nil || !strings.Contains(firstSeen, `"code": "context_step_up_required"`) || !strings.Contains(firstSeen, `"source": "simulation_input"`) {
		t.Fatalf("first-seen step-up: err=%v\n%s", err, firstSeen)
	}
	known, err := runContextExplain(t, append(base, "-device-first-seen", "false", "-json")...)
	if err != nil || !strings.Contains(known, `"code": "allowed"`) {
		t.Fatalf("previously known device: err=%v\n%s", err, known)
	}
	if _, err := runContextExplain(t, append(base, "-device-first-seen", "yes")...); err == nil {
		t.Fatal("invalid -device-first-seen value was accepted")
	}
	if _, err := runContextExplain(t, append(base[:5], "-device-first-seen", "true")...); err == nil {
		t.Fatal("-device-first-seen without -method passkey was accepted")
	}
}

func TestPolicyExplainClassifiesHypotheticalHTTPMethod(t *testing.T) {
	base := []string{"-host", "app.example.com", "-email", "ana@example.com", "-verified", "-method", "oidc"}
	unknown, err := runRiskContextExplain(t, base...)
	if err != nil || !strings.Contains(unknown, "code: context_signal_unknown\n") || !strings.Contains(unknown, "action.risky (source: simulation_no_request)") {
		t.Fatalf("unknown request method: err=%v\n%s", err, unknown)
	}

	post, err := runRiskContextExplain(t, append(base, "-request-method", "POST")...)
	if err != nil || !strings.Contains(post, "decision: denied\n") || !strings.Contains(post, "action.risky=true") || !strings.Contains(post, "method=POST (source=simulation_input)") {
		t.Fatalf("POST explanation: err=%v\n%s", err, post)
	}

	get, err := runRiskContextExplain(t, append(base, "-request-method", "GET")...)
	if err != nil || !strings.Contains(get, "decision: allowed\n") || !strings.Contains(get, "action.risky=false") {
		t.Fatalf("GET explanation: err=%v\n%s", err, get)
	}
	if _, err := runRiskContextExplain(t, append(base, "-request-method", "post")...); err == nil {
		t.Fatal("lowercase -request-method was accepted")
	}
	if _, err := runRiskContextExplain(t, "-host", "app.example.com", "-no-session", "-request-method", "GET"); err == nil {
		t.Fatal("-no-session with request evidence was accepted")
	}
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
			for _, want := range []string{"decision: " + tt.decision + "\n", "code: " + tt.code + "\n", "scope: ", "context (evidence only; unknown is not low"} {
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
			StepUp  bool   `json:"step_up"`
			Code    string `json:"code"`
			Reason  string `json:"reason"`
		} `json:"decision"`
		Context policycontext.Context `json:"context"`
		Scope   string                `json:"scope"`
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
	if got.Context.Version != policycontext.Version || got.Context.Session.UserVerified.Value != nil ||
		got.Context.Resource.Owner.Value == nil || *got.Context.Resource.Owner.Value != "platform" ||
		got.Context.Resource.DataClass.Value == nil || *got.Context.Resource.DataClass.Value != "account_data" ||
		got.Context.Resource.Sensitivity.Value == nil || *got.Context.Resource.Sensitivity.Value != "restricted" ||
		got.Context.Action.Risky.Value != nil {
		t.Errorf("policy explain inferred or omitted context evidence: %+v", got.Context)
	}
	textOut, err := runExplain(t, "-host", "app.example.com", "-email", "ana@example.com", "-verified", "-groups", "engineering,oncall")
	if err != nil {
		t.Fatalf("text runPolicy: %v", err)
	}
	if !strings.Contains(textOut, "owner=platform (source=route_config)") || !strings.Contains(textOut, "data_class=account_data (source=route_config)") {
		t.Errorf("text explanation omitted route catalog labels:\n%s", textOut)
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
