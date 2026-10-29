package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/policycontext"
)

func contextRuleContext(method string, deviceBound bool, sensitivity string) policycontext.Context {
	return policycontext.Build(policycontext.Input{
		ObservedAt:         time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		SessionMethod:      policycontext.Known(method, policycontext.SourceSession),
		SessionDeviceBound: policycontext.Known(deviceBound, policycontext.SourceSession),
		Sensitivity:        policycontext.Known(sensitivity, policycontext.SourceRouteConfig),
		Risky:              policycontext.Known(true, policycontext.SourceRouteConfig),
		DeviceFirstSeen:    policycontext.Unknown[bool](policycontext.SourceNotCollected),
	})
}

func boolPointer(value bool) *bool { return &value }

func TestAuthorizeWithContextEvaluatesFailClosedContextRule(t *testing.T) {
	principal := Principal{Email: "ana@example.com", EmailVerified: true, Method: MethodOIDC}
	baseWhen := config.ContextConditions{SessionMethod: MethodOIDC, ResourceSensitivity: "restricted"}
	for name, tc := range map[string]struct {
		context policycontext.Context
		policy  config.AccessPolicy
		allowed bool
		code    DecisionCode
		part    string
	}{
		"matched deny": {
			context: contextRuleContext(MethodOIDC, false, "restricted"),
			policy:  config.AccessPolicy{ContextRule: &config.ContextRule{When: baseWhen, Then: "deny"}},
			code:    CodeContextRuleDenied,
			part:    "context rule matched: session.method=\"oidc\", resource.sensitivity=\"restricted\"",
		},
		"known mismatch": {
			context: contextRuleContext(MethodPasskey, true, "restricted"),
			policy:  config.AccessPolicy{ContextRule: &config.ContextRule{When: baseWhen, Then: "deny"}},
			allowed: true,
			code:    CodeAllowed,
			part:    "context rule did not match: session.method=\"passkey\"",
		},
		"unknown evidence": {
			context: policycontext.Build(policycontext.Input{
				ObservedAt:    time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
				SessionMethod: policycontext.Known(MethodOIDC, policycontext.SourceSession),
				Sensitivity:   policycontext.Unknown[string](policycontext.SourceUnconfigured),
			}),
			policy: config.AccessPolicy{ContextRule: &config.ContextRule{When: baseWhen, Then: "deny"}},
			code:   CodeContextSignalUnknown,
			part:   "resource.sensitivity (source: unconfigured)",
		},
		"unknown device signal": {
			context: policycontext.Build(policycontext.Input{
				ObservedAt:         time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
				SessionDeviceBound: policycontext.Unknown[bool](policycontext.SourceNoSession),
			}),
			policy: config.AccessPolicy{ContextRule: &config.ContextRule{When: config.ContextConditions{
				SessionDeviceBound: boolPointer(false),
			}, Then: "deny"}},
			code: CodeContextSignalUnknown,
			part: "session.device_bound (source: no_session)",
		},
		"new passkey matched": {
			context: policycontext.Build(policycontext.Input{
				ObservedAt:      time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
				DeviceFirstSeen: policycontext.Known(true, policycontext.SourceSession),
			}),
			policy: config.AccessPolicy{ContextRule: &config.ContextRule{When: config.ContextConditions{
				DeviceFirstSeen: boolPointer(true),
			}, Then: "deny"}},
			code: CodeContextRuleDenied,
			part: "history.device_first_seen=true",
		},
		"first seen unknown": {
			context: policycontext.Build(policycontext.Input{
				ObservedAt:      time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
				DeviceFirstSeen: policycontext.Unknown[bool](policycontext.SourceNotCollected),
			}),
			policy: config.AccessPolicy{ContextRule: &config.ContextRule{When: config.ContextConditions{
				DeviceFirstSeen: boolPointer(true),
			}, Then: "deny"}},
			code: CodeContextSignalUnknown,
			part: "history.device_first_seen (source: not_collected)",
		},
		"matched step-up": {
			context: contextRuleContext(MethodOIDC, false, "restricted"),
			policy: config.AccessPolicy{ContextRule: &config.ContextRule{When: config.ContextConditions{
				SessionMethod: MethodOIDC, SessionDeviceBound: boolPointer(false),
			}, Then: "step_up"}},
			code: CodeContextStepUpRequired,
			part: "passkey step-up required",
		},
		"step-up condition no longer matches after passkey login": {
			context: contextRuleContext(MethodPasskey, true, "restricted"),
			policy:  config.AccessPolicy{ContextRule: &config.ContextRule{When: config.ContextConditions{SessionMethod: MethodOIDC}, Then: "step_up"}},
			allowed: true,
			code:    CodeAllowed,
			part:    "context rule did not match",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := AuthorizeWithContext(principal, tc.context, tc.policy)
			if got.Allowed != tc.allowed || got.Code != tc.code || !strings.Contains(got.Reason, tc.part) {
				t.Fatalf("decision = %+v, want allowed=%t code=%q reason containing %q", got, tc.allowed, tc.code, tc.part)
			}
		})
	}

	identityDenied := AuthorizeWithContext(principal, policycontext.Context{}, config.AccessPolicy{
		AllowedDomains: []string{"example.net"},
		ContextRule:    &config.ContextRule{When: config.ContextConditions{SessionMethod: MethodOIDC}, Then: "deny"},
	})
	if identityDenied.Code != CodeDomainNotAllowed {
		t.Fatalf("context rule ran before identity denial: %+v", identityDenied)
	}

	value := MethodOIDC
	unsourced := policycontext.Context{Session: policycontext.Session{Method: policycontext.Signal[string]{Value: &value}}}
	got := AuthorizeWithContext(principal, unsourced, config.AccessPolicy{
		ContextRule: &config.ContextRule{When: config.ContextConditions{SessionMethod: MethodOIDC}, Then: "deny"},
	})
	if got.Code != CodeContextSignalUnknown || !strings.Contains(got.Reason, "source: unknown") {
		t.Fatalf("unsourced signal decision = %+v", got)
	}
}
