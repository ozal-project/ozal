package certs

import (
	"context"
	"encoding/json"
	cf "github.com/ozal-project/ozal/internal/cloudflare"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperatorUsesExactRuleEndpointAndSuppressesProviderSecrets(t *testing.T) {
	failed := false
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed {
			w.WriteHeader(403)
			io.WriteString(w, `{"success":false,"errors":[{"message":"private-token"}]}`)
			return
		}
		if r.Method != "PATCH" || r.URL.Path != "/zones/zone/rulesets/set/rules/managed" {
			t.Error(r.Method, r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["id"]; ok {
			t.Error("read-only id sent")
		}
		writes++
		io.WriteString(w, `{"success":true,"result":{}}`)
	}))
	defer server.Close()
	client, _ := NewCloudflareClient(CloudflareConfig{Token: "private-token", BaseURL: server.URL, HTTPClient: server.Client()})
	rule, _ := cf.DeployRule("deploy.example.com")
	rule.ID = "managed"
	set := cf.Ruleset{ZoneID: "zone", ID: "set"}
	plan := cf.RulePlan{Action: "update", Before: &rule, After: rule}
	if err := client.SaveConfigurationRule(context.Background(), set, plan); err != nil || writes != 1 {
		t.Fatal(err, writes)
	}
	failed = true
	if err := client.SaveConfigurationRule(context.Background(), set, plan); err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatal("provider error not sanitized", err)
	}
}

func TestOperatorNeverFollowsAuthenticatedRedirect(t *testing.T) {
	followed := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
		io.WriteString(w, `{"success":true,"result":[]}`)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, _ := NewCloudflareClient(CloudflareConfig{Token: "private-token", BaseURL: source.URL})
	if _, err := client.OriginPull(context.Background(), "ozal.example.com"); err == nil {
		t.Fatal("accepted redirect")
	}
	if followed {
		t.Fatal("followed credential-bearing redirect")
	}
}
