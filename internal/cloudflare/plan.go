// Package cloudflare holds pure plans for the operator's optional integration.
package cloudflare

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const Phase = "http_config_settings"
const legacyMarker = "switchboard: CI deploy endpoint is not challenged (server-ops publish-site.py deploy-endpoint)"

func Hostname(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(host) > 253 || !strings.Contains(host, ".") {
		return "", errors.New("a fully qualified DNS hostname is required")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid DNS hostname")
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", errors.New("invalid DNS hostname")
			}
		}
	}
	return host, nil
}

type OriginPull struct {
	ZoneID        string `json:"zone_id"`
	Exists        bool   `json:"exists"`
	Hostname      string `json:"hostname"`
	CertificateID string `json:"cert_id"`
	Enabled       bool   `json:"enabled"`
}

func PlanOriginPull(host string, current, reference OriginPull) (OriginPull, bool, error) {
	host, err := Hostname(host)
	if err != nil {
		return OriginPull{}, false, err
	}
	if current.Hostname != host || current.ZoneID == "" || current.ZoneID != reference.ZoneID || !reference.Exists || !reference.Enabled || reference.CertificateID == "" {
		return OriginPull{}, false, errors.New("an enabled reference in the same zone is required")
	}
	want := OriginPull{ZoneID: current.ZoneID, Exists: true, Hostname: host, CertificateID: reference.CertificateID, Enabled: true}
	if current.Exists && current != want {
		return OriginPull{}, false, errors.New("hostname has different Origin Pulls settings; nothing may replace them")
	}
	return want, current != want, nil
}

type Rule struct {
	ID          string                     `json:"id,omitempty"`
	Ref         string                     `json:"ref,omitempty"`
	Action      string                     `json:"action"`
	Expression  string                     `json:"expression"`
	Description string                     `json:"description"`
	Enabled     bool                       `json:"enabled"`
	Parameters  map[string]json.RawMessage `json:"action_parameters"`
}
type Ruleset struct {
	ZoneID string `json:"zone_id"`
	ID     string `json:"id"`
	Rules  []Rule `json:"rules"`
}
type RulePlan struct {
	Action string
	Before *Rule
	After  Rule
}

func DeployRule(host string) (Rule, error) {
	host, err := Hostname(host)
	if err != nil {
		return Rule{}, err
	}
	sum := sha256.Sum256([]byte(host))
	return Rule{Ref: "ozal_ci_" + hex.EncodeToString(sum[:8]), Action: "set_config", Expression: fmt.Sprintf("(http.host eq %q)", host), Description: "ozal: CI deploy endpoint (managed by ozalctl)", Enabled: true,
		Parameters: map[string]json.RawMessage{"security_level": json.RawMessage(`"essentially_off"`), "bic": json.RawMessage(`false`)}}, nil
}

func sameParameters(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y, ok := b[k]
		if !ok || strings.TrimSpace(string(x)) != strings.TrimSpace(string(y)) {
			return false
		}
	}
	return true
}

func PlanDeployRule(host string, current Ruleset) (RulePlan, error) {
	want, err := DeployRule(host)
	if err != nil {
		return RulePlan{}, err
	}
	plan := RulePlan{Action: "add", After: want}
	if current.ZoneID == "" {
		return plan, errors.New("zone identity is required")
	}
	if current.ID == "" {
		plan.Action = "create"
		return plan, nil
	}
	for _, rule := range current.Rules {
		owned := rule.Ref == want.Ref || (rule.Description == legacyMarker && rule.Expression == want.Expression)
		if !owned {
			continue
		}
		if plan.Before != nil {
			return plan, errors.New("multiple managed rules match; review them explicitly")
		}
		if rule.Expression != want.Expression || rule.Action != "set_config" {
			return plan, errors.New("managed rule has different scope; nothing was changed")
		}
		for key := range rule.Parameters {
			if key != "security_level" && key != "bic" {
				return plan, errors.New("managed rule contains additional settings; review them explicitly")
			}
		}
		for key, value := range rule.Parameters {
			if key == "bic" && string(value) != "true" && string(value) != "false" {
				return plan, errors.New("invalid managed browser setting")
			}
			if key == "security_level" {
				var level string
				if json.Unmarshal(value, &level) != nil {
					return plan, errors.New("invalid managed security setting")
				}
				switch level {
				case "off", "essentially_off", "low", "medium", "high", "under_attack":
				default:
					return plan, errors.New("unknown managed security setting")
				}
			}
		}
		copy := rule
		plan.Before = &copy
		plan.After.ID = rule.ID
		// Preserve identity and operator descriptions when only settings need updating.
		plan.After.Ref, plan.After.Description = rule.Ref, rule.Description
		plan.Action = "update"
		if rule.Enabled && sameParameters(rule.Parameters, want.Parameters) {
			plan.Action = "ok"
		}
	}
	return plan, nil
}
