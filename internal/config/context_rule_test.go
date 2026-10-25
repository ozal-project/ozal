package config

import (
	"strings"
	"testing"
)

func contextRuleConfig(rule *ContextRule) Config {
	cfg := passkeyConfig()
	cfg.Auth.Issuer = "https://accounts.google.com"
	cfg.Auth.ClientID = "id"
	cfg.Auth.ClientSecret = "secret"
	cfg.Auth.RedirectURL = "https://auth.example.com/_/auth/callback"
	cfg.Routes[0].Resource = &RouteResource{
		Owner:        "platform",
		DataClass:    "account_data",
		Sensitivity:  "restricted",
		RiskyActions: []string{"POST", "DELETE"},
	}
	cfg.Routes[0].Access = &AccessPolicy{ContextRule: rule}
	return cfg
}

func configBoolPointer(value bool) *bool { return &value }

func TestContextRuleConfigValidation(t *testing.T) {
	cfg := contextRuleConfig(&ContextRule{
		When: ContextConditions{SessionMethod: MethodOIDC, ResourceSensitivity: "restricted"},
		Then: "step_up",
	})
	validated, err := Validate(cfg)
	if err != nil {
		t.Fatalf("Validate step-up context rule: %v", err)
	}
	if got := validated.Routes[0].Access.ContextRule; got == nil || got.Then != "step_up" || got.When.SessionMethod != MethodOIDC || got.When.ResourceSensitivity != "restricted" {
		t.Fatalf("context rule = %+v", got)
	}
	firstSeen := contextRuleConfig(&ContextRule{When: ContextConditions{DeviceFirstSeen: configBoolPointer(true)}, Then: "step_up"})
	if _, err := Validate(firstSeen); err != nil {
		t.Fatalf("valid one-time first-seen step-up: %v", err)
	}

	trueValue := true
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"empty when":     {func(c *Config) { c.Routes[0].Access.ContextRule.When = ContextConditions{} }, "must contain at least one"},
		"unknown method": {func(c *Config) { c.Routes[0].Access.ContextRule.When.SessionMethod = "sms" }, "must be oidc or passkey"},
		"unconfigured method": {func(c *Config) {
			c.Auth.Issuer = ""
			c.Auth.ClientID = ""
			c.Auth.ClientSecret = ""
			c.Auth.RedirectURL = ""
		}, "not a configured login method"},
		"unknown sensitivity":         {func(c *Config) { c.Routes[0].Access.ContextRule.When.ResourceSensitivity = "secret" }, "not a supported label"},
		"sensitivity without catalog": {func(c *Config) { c.Routes[0].Resource = nil }, "requires route.resource"},
		"risky without configured list": {func(c *Config) {
			c.Routes[0].Access.ContextRule.When = ContextConditions{ActionRisky: &trueValue}
			c.Routes[0].Resource.RiskyActions = nil
			c.Routes[0].Access.ContextRule.Then = "deny"
		}, "requires route.resource.risky_actions"},
		"unknown action":                       {func(c *Config) { c.Routes[0].Access.ContextRule.Then = "challenge" }, "must be deny or step_up"},
		"step-up without a one-time condition": {func(c *Config) { c.Routes[0].Access.ContextRule.When.SessionMethod = MethodPasskey }, "requires when.session_method=oidc or when.device_first_seen=true"},
		"step-up on previously known credential": {func(c *Config) {
			value := false
			c.Routes[0].Access.ContextRule.When = ContextConditions{DeviceFirstSeen: &value}
		}, "requires when.session_method=oidc or when.device_first_seen=true"},
		"first-seen rule without passkeys": {func(c *Config) {
			c.Auth.Passkeys = nil
			c.Routes[0].Access.ContextRule.When = ContextConditions{DeviceFirstSeen: &trueValue}
			c.Routes[0].Access.ContextRule.Then = "deny"
		}, "device_first_seen requires auth.passkeys"},
		"first-seen with OIDC condition": {func(c *Config) {
			c.Routes[0].Access.ContextRule.When = ContextConditions{SessionMethod: MethodOIDC, DeviceFirstSeen: &trueValue}
			c.Routes[0].Access.ContextRule.Then = "deny"
		}, "cannot use session_method=oidc"},
		"first-seen excludes passkeys": {func(c *Config) {
			c.Routes[0].Access.ContextRule.When = ContextConditions{DeviceFirstSeen: &trueValue}
			c.Routes[0].Access.ContextRule.Then = "deny"
			c.Routes[0].Access.Methods = []string{MethodOIDC}
		}, "requires access.methods to allow passkey"},
		"step-up without passkey":          {func(c *Config) { c.Auth.Passkeys = nil }, "requires configured passkey login"},
		"step-up method excluded by route": {func(c *Config) { c.Routes[0].Access.Methods = []string{MethodOIDC} }, "requires access.methods to allow passkey"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := contextRuleConfig(&ContextRule{
				When: ContextConditions{SessionMethod: MethodOIDC, ResourceSensitivity: "restricted"},
				Then: "step_up",
			})
			tc.mutate(&candidate)
			if _, err := Validate(candidate); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want %q", err, tc.want)
			}
		})
	}

	denyRule := contextRuleConfig(&ContextRule{When: ContextConditions{ResourceSensitivity: "restricted"}, Then: "deny"})
	if _, err := Validate(denyRule); err != nil {
		t.Fatalf("valid deny rule: %v", err)
	}
}

func TestContextRuleConfigRejectsUnknownSignalNames(t *testing.T) {
	document := `{"listen":"127.0.0.1:0","auth":{"issuer":"https://accounts.google.com","client_id":"id","client_secret":"secret","redirect_url":"https://auth.example.com/_/auth/callback","passkeys":{"rp_id":"example.com","login_url":"https://auth.example.com"}},"routes":[{"host":"app.example.com","target":"http://127.0.0.1:9000","access":{"context_rule":{"when":{"unrecognized_signal":true},"then":"deny"}}}]}`
	if _, err := Parse([]byte(document)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Parse error = %v, want unknown signal field rejected", err)
	}
}

func TestCloneRouteDeepCopiesContextRule(t *testing.T) {
	deviceBound := false
	risky := true
	firstSeen := false
	source := Route{Access: &AccessPolicy{ContextRule: &ContextRule{
		When: ContextConditions{
			SessionDeviceBound: &deviceBound,
			ActionRisky:        &risky,
			DeviceFirstSeen:    &firstSeen,
		},
		Then: "deny",
	}}}
	clone := CloneRoute(source)
	*clone.Access.ContextRule.When.SessionDeviceBound = true
	*clone.Access.ContextRule.When.ActionRisky = false
	*clone.Access.ContextRule.When.DeviceFirstSeen = true
	if deviceBound || !risky || firstSeen {
		t.Fatalf("clone shares context rule condition pointers: source = %+v", source.Access.ContextRule.When)
	}
}
