package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/policycontext"
)

func TestExplain(t *testing.T) {
	ana := &Principal{Email: "ana@example.com", EmailVerified: true, Groups: []string{"support"}}
	needsEng := &config.AccessPolicy{RequiredGroups: []string{"engineering"}}

	tests := []struct {
		name          string
		route         config.Route
		principal     *Principal
		wantProtected bool
		wantGate      bool
		wantCode      DecisionCode
		wantAllowed   bool
	}{
		{"public, no session", config.Route{}, nil, false, false, CodeAllowed, true},
		{"public ignores principal", config.Route{}, ana, false, false, CodeAllowed, true},
		{"protected, no session stops at gate", config.Route{Access: needsEng}, nil, true, true, CodeAuthenticationRequired, false},
		{"legacy auth_required, no session", config.Route{AuthRequired: true}, nil, true, true, CodeAuthenticationRequired, false},
		{"legacy auth_required is the empty policy", config.Route{AuthRequired: true}, ana, true, false, CodeAllowed, true},
		{"empty access policy admits any principal", config.Route{Access: &config.AccessPolicy{}}, ana, true, false, CodeAllowed, true},
		{"policy evaluated by Authorize", config.Route{Access: needsEng}, ana, true, false, CodeMissingRequiredGroup, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Explain(tt.route, tt.principal)
			if got.Protected != tt.wantProtected || got.AtGate != tt.wantGate ||
				got.Decision.Code != tt.wantCode || got.Decision.Allowed != tt.wantAllowed {
				t.Errorf("Explain() = %+v; want protected=%t gate=%t code=%s allowed=%t",
					got, tt.wantProtected, tt.wantGate, tt.wantCode, tt.wantAllowed)
			}
		})
	}
}

// Explain must return exactly what Authorize returns for an authenticated
// request: it is a view of the real engine, never a second one.
func TestExplainMatchesAuthorize(t *testing.T) {
	policies := []config.AccessPolicy{
		{},
		{AllowedDomains: []string{"example.com"}},
		{AllowedEmails: []string{"Ana@Example.com"}, RequiredGroups: []string{"ops"}},
	}
	principals := []Principal{
		{Email: "ana@example.com", EmailVerified: true, Groups: []string{"ops"}},
		{Email: "ana@example.com", EmailVerified: false},
		{Email: "eve@evilexample.com", EmailVerified: true},
	}
	for _, pol := range policies {
		for _, p := range principals {
			route := config.Route{Access: &pol}
			if got, want := Explain(route, &p).Decision, Authorize(p, pol); got.Code != want.Code || got.Allowed != want.Allowed {
				t.Errorf("policy %+v principal %+v: Explain = %+v, Authorize = %+v", pol, p, got, want)
			}
		}
	}
}

func TestExplainStatesStepUp(t *testing.T) {
	route := config.Route{Host: "admin.example.com", Access: &config.AccessPolicy{MaxAuthAgeMinutes: 15}}
	got := Explain(route, &Principal{Subject: "s"})
	if !got.Decision.Allowed || !strings.Contains(got.Decision.Reason, "15 min") {
		t.Fatalf("explanation = %+v", got.Decision)
	}
}

func TestNewReportIncludesContextAndUnknownSignals(t *testing.T) {
	observedAuthTime := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
	principal := &Principal{Email: "ana@example.com", EmailVerified: true, Method: MethodPasskey, AuthTime: observedAuthTime}
	report := NewReport("admin.example.com", config.Route{
		Host:   "admin.example.com",
		Access: &config.AccessPolicy{},
	}, principal)
	if report.Context.Version == 0 {
		t.Fatal("policy report omitted decision context")
	}
	if got := report.Context.Resource.Route.Value; got == nil || *got != "admin.example.com" {
		t.Fatalf("route signal = %+v", report.Context.Resource.Route)
	}
	if report.Context.Resource.Owner.Value != nil || report.Context.Resource.DataClass.Value != nil ||
		report.Context.Resource.Sensitivity.Value != nil || report.Context.Resource.Sensitivity.Source != policycontext.SourceUnconfigured {
		t.Fatalf("unconfigured resource catalog was inferred: %+v", report.Context.Resource)
	}
	if got := report.Context.Session.Method.Value; got == nil || *got != MethodPasskey {
		t.Fatalf("session method signal = %+v", report.Context.Session.Method)
	}
	if got := report.Context.Session.UserVerified.Value; got != nil {
		t.Fatalf("email verification was incorrectly treated as WebAuthn UV: %+v", report.Context.Session.UserVerified)
	}
	if got := report.Context.Network.ArrivalPath.Value; got != nil || report.Context.Network.ArrivalPath.Source != policycontext.SourceSimulationNoRequest {
		t.Fatalf("offline simulation inferred network path: %+v", report.Context.Network.ArrivalPath)
	}
	if report.Context.Session.Method.ObservedAt.IsZero() {
		t.Fatal("policy report context has no observation time")
	}
}
