package cloudflare

import (
	"encoding/json"
	"testing"
)

func TestScopedPlansRefuseReplacementAndBroaderRules(t *testing.T) {
	for _, host := range []string{`x.example.com") or true`, "*.example.com", "https://example.com", "a..com", "localhost", "a.com:443"} {
		if _, err := Hostname(host); err == nil {
			t.Fatalf("accepted %q", host)
		}
	}
	current := OriginPull{ZoneID: "zone", Hostname: "new.example.com"}
	reference := OriginPull{ZoneID: "zone", Hostname: "old.example.com", Exists: true, Enabled: true, CertificateID: "cert"}
	want, changed, err := PlanOriginPull(current.Hostname, current, reference)
	if err != nil || !changed || want.CertificateID != "cert" {
		t.Fatal(want, changed, err)
	}
	current.Exists = true
	current.CertificateID = "other"
	if _, _, err := PlanOriginPull(current.Hostname, current, reference); err == nil {
		t.Fatal("replaced another binding")
	}
	rule, _ := DeployRule("deploy.example.com")
	set := Ruleset{ZoneID: "zone", ID: "set", Rules: []Rule{{ID: "unrelated", Expression: "true"}, rule}}
	plan, err := PlanDeployRule("deploy.example.com", set)
	if err != nil || plan.Action != "ok" {
		t.Fatal(plan, err)
	}
	set.Rules[1].Expression = "true"
	if _, err := PlanDeployRule("deploy.example.com", set); err == nil {
		t.Fatal("accepted broad managed expression")
	}
	set.Rules[1] = rule
	set.Rules[1].Parameters = map[string]json.RawMessage{"bic": json.RawMessage(`"secret"`)}
	if _, err := PlanDeployRule("deploy.example.com", set); err == nil {
		t.Fatal("accepted opaque settings")
	}
	set.Rules[1] = rule
	set.Rules = append(set.Rules, rule)
	if _, err := PlanDeployRule("deploy.example.com", set); err == nil {
		t.Fatal("accepted ambiguous managed rules")
	}
}
