package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/policycontext"
)

const explainToken = "explain-test-token"

func explainHandler() *Handler {
	return New(&fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: explainToken},
		Routes: []config.Route{
			{Host: "www.example.com", Target: "http://127.0.0.1:18001"},
			{Host: "app.example.com", Target: "http://127.0.0.1:18002", Access: &config.AccessPolicy{
				AllowedEmails:  []string{"secret-person@example.com"},
				RequiredGroups: []string{"engineering"},
			}},
		},
	}}, nil)
}

func contextExplainHandler() *Handler {
	return New(&fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: explainToken},
		Auth: config.Auth{
			Issuer:   "https://idp.example.com",
			Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"},
		},
		Routes: []config.Route{{
			Host: "app.example.com", Target: "http://127.0.0.1:18002",
			Access: &config.AccessPolicy{ContextRule: &config.ContextRule{
				When: config.ContextConditions{SessionMethod: auth.MethodOIDC, ResourceSensitivity: "restricted"},
				Then: "step_up",
			}},
			Resource: &config.RouteResource{Owner: "platform", DataClass: "account_data", Sensitivity: "restricted"},
		}, {
			Host: "new-device.example.com", Target: "http://127.0.0.1:18003",
			Access: &config.AccessPolicy{ContextRule: &config.ContextRule{
				When: config.ContextConditions{DeviceFirstSeen: policyBoolPointer(true)},
				Then: "step_up",
			}},
		}},
	}}, nil)
}

func policyBoolPointer(value bool) *bool { return &value }

func riskyContextExplainHandler() *Handler {
	return New(&fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: explainToken},
		Auth:  config.Auth{Issuer: "https://idp.example.com"},
		Routes: []config.Route{{
			Host: "app.example.com", Target: "http://127.0.0.1:18002",
			Access: &config.AccessPolicy{ContextRule: &config.ContextRule{
				When: config.ContextConditions{ActionRisky: policyBoolPointer(true)},
				Then: "deny",
			}},
			Resource: &config.RouteResource{Owner: "platform", DataClass: "account_data", Sensitivity: "restricted", RiskyActions: []string{"POST"}},
		}},
	}}, nil)
}

// The explain API reveals allowlists and can be probed as an oracle, so it is
// held to the same bearer check as mutations.
func TestExplainRequiresBearer(t *testing.T) {
	body := `{"host":"app.example.com","principal":{"email":"secret-person@example.com","email_verified":true,"groups":["engineering"]}}`
	for _, token := range []string{"", "wrong-token"} {
		rec := request(explainHandler(), http.MethodPost, "/_/api/policy/explain", token, body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d; want 401", token, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret-person") {
			t.Errorf("token %q: unauthorized response leaks policy: %s", token, rec.Body)
		}
	}
}

func TestExplainAPI(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		status   int
		code     string
		scope    string
		protects bool
	}{
		{"allowed", `{"host":"app.example.com","principal":{"email":"Secret-Person@example.com","email_verified":true,"groups":["engineering"]}}`, 200, "allowed", auth.ScopeAuthorization, true},
		{"missing group", `{"host":"app.example.com","principal":{"email":"secret-person@example.com","email_verified":true,"groups":["Engineering"]}}`, 200, "missing_required_group", auth.ScopeAuthorization, true},
		{"no session stops at gate", `{"host":"APP.example.com.","principal":null}`, 200, "authentication_required", auth.ScopeGate, true},
		{"public route", `{"host":"www.example.com"}`, 200, "allowed", auth.ScopePublic, false},
		{"unknown host", `{"host":"nope.example.com","principal":null}`, 404, "", "", false},
		{"invalid host", `{"host":"","principal":null}`, 400, "", "", false},
		{"principal without email", `{"host":"app.example.com","principal":{"email_verified":true}}`, 400, "", "", false},
		{"unknown field", `{"host":"app.example.com","principal":null,"admin":true}`, 400, "", "", false},
		{"unconfigured method", `{"host":"app.example.com","principal":{"email":"ana@example.com","method":"oidc"}}`, 400, "", "", false},
		{"device bound without passkey method", `{"host":"app.example.com","principal":{"email":"ana@example.com","device_bound":false}}`, 400, "", "", false},
		{"user verification without passkey method", `{"host":"app.example.com","principal":{"email":"ana@example.com","user_verified":true}}`, 400, "", "", false},
		{"first seen without passkey method", `{"host":"app.example.com","principal":{"email":"ana@example.com","device_first_seen":true}}`, 400, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := request(explainHandler(), http.MethodPost, "/_/api/policy/explain", explainToken, tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d; want %d (%s)", rec.Code, tt.status, rec.Body)
			}
			if tt.status != http.StatusOK {
				return
			}
			var got auth.Report
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Decision.Code != tt.code || got.Scope != tt.scope || got.Protected != tt.protects {
				t.Errorf("report = %+v; want code=%s scope=%s protected=%t", got, tt.code, tt.scope, tt.protects)
			}
		})
	}
}

func TestExplainAPIContextRuleUsesSuppliedSessionEvidence(t *testing.T) {
	tests := []struct {
		name      string
		principal string
		code      string
		allowed   bool
		stepUp    bool
	}{
		{"unknown method fails closed", `{"email":"ana@example.com"}`, "context_signal_unknown", false, false},
		{"OIDC session steps up", `{"email":"ana@example.com","method":"oidc"}`, "context_step_up_required", false, true},
		{"passkey session no longer matches", `{"email":"ana@example.com","method":"passkey","device_bound":true}`, "allowed", true, false},
		{"passkey UV input is visible", `{"email":"ana@example.com","method":"passkey","user_verified":true}`, "allowed", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"host":"app.example.com","principal":` + tc.principal + `}`
			rec := request(contextExplainHandler(), http.MethodPost, "/_/api/policy/explain", explainToken, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			var report auth.Report
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Decision.Code != tc.code || report.Decision.Allowed != tc.allowed || report.Decision.StepUp != tc.stepUp {
				t.Fatalf("decision = %+v", report.Decision)
			}
			if tc.name == "passkey UV input is visible" && (report.Context.Session.UserVerified.Value == nil || !*report.Context.Session.UserVerified.Value || report.Context.Session.UserVerified.Source != policycontext.SourceSimulationInput) {
				t.Fatalf("user_verified evidence = %+v", report.Context.Session.UserVerified)
			}
		})
	}
}

func TestExplainAPIClassifiesHypotheticalHTTPMethod(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		code   string
		value  *bool
	}{
		{name: "risky POST", method: "POST", code: "context_rule_denied", value: policyBoolPointer(true)},
		{name: "non-risky GET", method: "GET", code: "allowed", value: policyBoolPointer(false)},
		{name: "unknown method", code: "context_signal_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"host":"app.example.com","principal":{"email":"ana@example.com","method":"oidc"}`
			if tc.method != "" {
				body += `,"request_method":"` + tc.method + `"`
			}
			body += `}`
			rec := request(riskyContextExplainHandler(), http.MethodPost, "/_/api/policy/explain", explainToken, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			var report auth.Report
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Decision.Code != tc.code || (report.Context.Action.Risky.Value == nil) != (tc.value == nil) {
				t.Fatalf("report = %+v", report)
			}
			if tc.value != nil && *report.Context.Action.Risky.Value != *tc.value {
				t.Fatalf("risky signal = %+v, want %t", report.Context.Action.Risky, *tc.value)
			}
		})
	}
	invalid := request(riskyContextExplainHandler(), http.MethodPost, "/_/api/policy/explain", explainToken,
		`{"host":"app.example.com","principal":{"email":"ana@example.com","method":"oidc"},"request_method":"post"}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("lowercase request method status = %d: %s", invalid.Code, invalid.Body)
	}
}

func TestExplainAPIUsesHypotheticalFirstSeenEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		principal string
		code      string
		firstSeen *bool
	}{
		{name: "unknown fails closed", principal: `{"email":"ana@example.com"}`, code: "context_signal_unknown"},
		{name: "new passkey steps up", principal: `{"email":"ana@example.com","method":"passkey","device_first_seen":true}`, code: "context_step_up_required", firstSeen: policyBoolPointer(true)},
		{name: "known passkey is allowed", principal: `{"email":"ana@example.com","method":"passkey","device_first_seen":false}`, code: "allowed", firstSeen: policyBoolPointer(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"host":"new-device.example.com","principal":` + tc.principal + `}`
			rec := request(contextExplainHandler(), http.MethodPost, "/_/api/policy/explain", explainToken, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			var report auth.Report
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Decision.Code != tc.code {
				t.Fatalf("decision = %+v, want %s", report.Decision, tc.code)
			}
			if tc.firstSeen != nil {
				got := report.Context.History.DeviceFirstSeen
				if got.Value == nil || *got.Value != *tc.firstSeen || got.Source != policycontext.SourceSimulationInput {
					t.Fatalf("first-seen context = %+v", got)
				}
			}
		})
	}
}

func TestSimulatorPageMovedToConsole(t *testing.T) {
	h := New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: explainToken}}}, nil, WithEvents(events.NewBroker()))
	rec := request(h, http.MethodGet, "/simulator", "", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), explainToken) {
		t.Fatalf("simulator: status %d; the page must never carry the admin token", rec.Code)
	}
	if old := request(h, http.MethodGet, "/_/simulator", "", ""); old.Code != http.StatusMovedPermanently || old.Header().Get("Location") != "/simulator" {
		t.Errorf("old simulator URL: %d %v", old.Code, old.Header())
	}
}
