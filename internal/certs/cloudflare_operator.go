package certs

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	cf "github.com/ozal-project/ozal/internal/cloudflare"
)

func operatorError() error {
	return errors.New("cloudflare operation was not confirmed; inspect the provider before retrying")
}

func (c *CloudflareClient) OriginPull(ctx context.Context, host string) (cf.OriginPull, error) {
	host, err := cf.Hostname(host)
	if err != nil {
		return cf.OriginPull{}, err
	}
	zone, err := c.zoneFor(ctx, host)
	if err != nil {
		return cf.OriginPull{}, operatorError()
	}
	result := cf.OriginPull{ZoneID: zone, Hostname: host}
	var body struct {
		Hostname      string `json:"hostname"`
		CertificateID string `json:"cert_id"`
		Enabled       bool   `json:"enabled"`
	}
	err = c.call(ctx, http.MethodGet, "/zones/"+url.PathEscape(zone)+"/origin_tls_client_auth/hostnames/"+url.PathEscape(host), nil, &body)
	if errors.Is(err, errRecordMissing) {
		return result, nil
	}
	if err != nil {
		return result, operatorError()
	}
	if body.Hostname != "" && body.Hostname != host {
		return result, errors.New("cloudflare returned another hostname")
	}
	result.Exists, result.CertificateID, result.Enabled = true, body.CertificateID, body.Enabled
	return result, nil
}

func (c *CloudflareClient) SetOriginPull(ctx context.Context, want cf.OriginPull) error {
	host, err := cf.Hostname(want.Hostname)
	if err != nil || want.ZoneID == "" || want.CertificateID == "" || !want.Enabled {
		return errors.New("invalid Origin Pulls binding")
	}
	zone, err := c.zoneFor(ctx, host)
	if err != nil || zone != want.ZoneID {
		return errors.New("origin pulls zone changed; nothing was written")
	}
	payload := map[string]any{"config": []any{map[string]any{"hostname": host, "cert_id": want.CertificateID, "enabled": true}}}
	if err := c.call(ctx, http.MethodPut, "/zones/"+url.PathEscape(zone)+"/origin_tls_client_auth/hostnames", payload, nil); err != nil {
		return operatorError()
	}
	return nil
}

func (c *CloudflareClient) ConfigurationRules(ctx context.Context, host string) (cf.Ruleset, error) {
	host, err := cf.Hostname(host)
	if err != nil {
		return cf.Ruleset{}, err
	}
	zone, err := c.zoneFor(ctx, host)
	if err != nil {
		return cf.Ruleset{}, operatorError()
	}
	var result cf.Ruleset
	err = c.call(ctx, http.MethodGet, "/zones/"+url.PathEscape(zone)+"/rulesets/phases/"+cf.Phase+"/entrypoint", nil, &result)
	if errors.Is(err, errRecordMissing) {
		return cf.Ruleset{ZoneID: zone}, nil
	}
	if err != nil {
		return result, operatorError()
	}
	result.ZoneID = zone
	return result, nil
}

// SaveConfigurationRule uses rule-specific endpoints. It never replaces the
// entire phase entrypoint, so unrelated rules survive concurrent maintenance.
func (c *CloudflareClient) SaveConfigurationRule(ctx context.Context, set cf.Ruleset, plan cf.RulePlan) error {
	if set.ZoneID == "" {
		return errors.New("zone is required")
	}
	after := plan.After
	after.ID = ""
	base := "/zones/" + url.PathEscape(set.ZoneID) + "/rulesets"
	method, path, payload := http.MethodPost, base, any(map[string]any{"name": "Ozal hostname settings", "kind": "zone", "phase": cf.Phase, "rules": []cf.Rule{after}})
	switch plan.Action {
	case "create":
		if set.ID != "" {
			return errors.New("ruleset already exists")
		}
	case "add":
		if set.ID == "" {
			return errors.New("ruleset is required")
		}
		path = base + "/" + url.PathEscape(set.ID) + "/rules"
		payload = after
	case "update":
		if set.ID == "" || plan.Before == nil || plan.Before.ID == "" {
			return errors.New("exact rule identity is required")
		}
		method = http.MethodPatch
		path = base + "/" + url.PathEscape(set.ID) + "/rules/" + url.PathEscape(plan.Before.ID)
		payload = after
	default:
		return errors.New("rule change is not applicable")
	}
	if err := c.call(ctx, method, path, payload, nil); err != nil {
		return operatorError()
	}
	return nil
}
