package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestJSONSchemaMatchesConfigFieldsAndDeclaresCrossFieldConstraints(t *testing.T) {
	data, err := JSONSchema()
	if err != nil {
		t.Fatalf("JSONSchema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("schema dialect = %v", schema["$schema"])
	}
	properties := schema["properties"].(map[string]any)
	for i := 0; i < reflect.TypeOf(Config{}).NumField(); i++ {
		field := reflect.TypeOf(Config{}).Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if _, ok := properties[name]; !ok {
			t.Errorf("config field %q missing from schema", name)
		}
	}
	if schema["additionalProperties"] != false {
		t.Fatal("schema must reject unknown top-level keys like the config decoder")
	}
	if got := properties["routes"].(map[string]any)["minItems"]; got != float64(1) {
		t.Fatalf("routes.minItems = %v, want 1", got)
	}
	route := properties["routes"].(map[string]any)["items"].(map[string]any)
	if got := len(route["oneOf"].([]any)); got != 5 {
		t.Fatalf("route destination alternatives = %d, want 5", got)
	}
	upstreams := route["properties"].(map[string]any)["upstreams"].(map[string]any)
	if upstreams["minItems"] != float64(2) || upstreams["uniqueItems"] != true || upstreams["items"].(map[string]any)["minLength"] != float64(1) {
		t.Fatalf("route.upstreams schema = %v", upstreams)
	}
	access := route["properties"].(map[string]any)["access"].(map[string]any)["properties"].(map[string]any)
	methods := access["methods"].(map[string]any)["items"].(map[string]any)["enum"].([]any)
	if len(methods) != 2 || methods[0] != "oidc" || methods[1] != "passkey" {
		t.Fatalf("access.methods enum = %v", methods)
	}
	contextRule := access["context_rule"].(map[string]any)
	contextRuleProperties := contextRule["properties"].(map[string]any)
	when := contextRuleProperties["when"].(map[string]any)
	if when["minProperties"] != float64(1) {
		t.Fatal("access.context_rule.when must require at least one condition")
	}
	if got := contextRuleProperties["then"].(map[string]any)["enum"].([]any); len(got) != 2 || got[0] != "deny" || got[1] != "step_up" {
		t.Fatalf("access.context_rule.then enum = %v", got)
	}
	resource := route["properties"].(map[string]any)["resource"].(map[string]any)
	resourceProperties := resource["properties"].(map[string]any)
	sensitivity := resourceProperties["sensitivity"].(map[string]any)["enum"].([]any)
	if len(sensitivity) != 4 || sensitivity[3] != "restricted" {
		t.Fatalf("resource.sensitivity enum = %v", sensitivity)
	}
	if resourceProperties["risky_actions"].(map[string]any)["uniqueItems"] != true {
		t.Fatal("resource.risky_actions must reject duplicate methods")
	}
	audit := properties["audit"].(map[string]any)["properties"].(map[string]any)
	include := audit["include"].(map[string]any)
	if include["minItems"] != float64(1) || include["uniqueItems"] != true || include["items"].(map[string]any)["enum"].([]any)[0] != "access" {
		t.Fatalf("audit.include schema = %v", include)
	}
	auditMode := route["properties"].(map[string]any)["audit_mode"].(map[string]any)["enum"].([]any)
	if len(auditMode) != 2 || auditMode[0] != "required" || auditMode[1] != "best_effort" {
		t.Fatalf("route.audit_mode enum = %v", auditMode)
	}
}
