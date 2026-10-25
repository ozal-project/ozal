package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// JSONSchema returns an editor-oriented JSON Schema derived from the same
// structs used by the strict config decoder. Cross-field security rules still
// live in Validate and must be checked with `switchboard validate`.
func JSONSchema() ([]byte, error) {
	schema, err := schemaForType(reflect.TypeOf(Config{}))
	if err != nil {
		return nil, err
	}
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["title"] = "Switchboard configuration"
	schema["description"] = "Editor aid generated from Switchboard's config types. Run `switchboard validate` for cross-field and security validation."

	properties := schema["properties"].(map[string]any)
	properties["listen"].(map[string]any)["minLength"] = 1
	properties["routes"].(map[string]any)["minItems"] = 1

	route := properties["routes"].(map[string]any)["items"].(map[string]any)
	route["description"] = "Exactly one destination is required. HTTP routes may use target for one upstream or upstreams for round-robin selection across at least two targets."
	route["oneOf"] = []any{
		map[string]any{"required": []string{"target"}},
		map[string]any{"required": []string{"upstreams"}},
		map[string]any{"required": []string{"root"}},
		map[string]any{"required": []string{"site"}},
		map[string]any{"required": []string{"connector"}},
	}
	routeProperties := route["properties"].(map[string]any)
	routeProperties["health_path"].(map[string]any)["pattern"] = "^/"
	for _, name := range []string{"target", "root", "site", "connector"} {
		routeProperties[name].(map[string]any)["minLength"] = 1
	}
	upstreams := routeProperties["upstreams"].(map[string]any)
	upstreams["minItems"] = 2
	upstreams["uniqueItems"] = true
	upstreams["items"].(map[string]any)["minLength"] = 1

	access := routeProperties["access"].(map[string]any)["properties"].(map[string]any)
	access["max_auth_age_minutes"].(map[string]any)["minimum"] = 0
	access["methods"].(map[string]any)["items"].(map[string]any)["enum"] = []string{"oidc", "passkey"}
	contextRule := access["context_rule"].(map[string]any)
	contextRule["description"] = "One AND-composed evidence rule. Unknown required signals fail closed. step_up requires an OIDC session and redirects to passkey login."
	contextRuleProperties := contextRule["properties"].(map[string]any)
	contextWhen := contextRuleProperties["when"].(map[string]any)
	contextWhen["minProperties"] = 1
	contextWhen["description"] = "Only the listed, sourced signals are evaluated; all must match."
	whenProperties := contextWhen["properties"].(map[string]any)
	whenProperties["session_method"].(map[string]any)["enum"] = []string{"oidc", "passkey"}
	whenProperties["resource_sensitivity"].(map[string]any)["enum"] = []string{"public", "internal", "confidential", "restricted"}
	contextRuleProperties["then"].(map[string]any)["enum"] = []string{"deny", "step_up"}
	resource := routeProperties["resource"].(map[string]any)
	resource["description"] = "Owner-declared metadata for this route; access.context_rule may explicitly use its sensitivity and risky-action labels."
	resourceProperties := resource["properties"].(map[string]any)
	for _, name := range []string{"owner", "data_class"} {
		resourceProperties[name].(map[string]any)["minLength"] = 1
		resourceProperties[name].(map[string]any)["maxLength"] = 128
	}
	resourceProperties["sensitivity"].(map[string]any)["enum"] = []string{"public", "internal", "confidential", "restricted"}
	resourceProperties["risky_actions"].(map[string]any)["uniqueItems"] = true
	resourceProperties["risky_actions"].(map[string]any)["items"].(map[string]any)["enum"] = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"}
	routeProperties["audit_mode"].(map[string]any)["enum"] = []string{"required", "best_effort"}

	projects := properties["projects"].(map[string]any)
	projects["description"] = "Optional deployment trust declarations. They do not change existing site routes."
	projects["items"].(map[string]any)["description"] = "One project owns a Pages site and the hostnames routed to it."
	project := projects["items"].(map[string]any)["properties"].(map[string]any)
	project["name"].(map[string]any)["minLength"] = 1
	project["site"].(map[string]any)["minLength"] = 1
	project["hostnames"].(map[string]any)["minItems"] = 1
	project["hostnames"].(map[string]any)["uniqueItems"] = true
	project["hostnames"].(map[string]any)["items"].(map[string]any)["minLength"] = 1
	project["deployers"].(map[string]any)["minItems"] = 1
	deployer := project["deployers"].(map[string]any)["items"].(map[string]any)
	deployer["oneOf"] = []any{
		map[string]any{"required": []string{"github"}},
		map[string]any{"required": []string{"token"}},
	}
	deployerProperties := deployer["properties"].(map[string]any)
	deployerProperties["token"].(map[string]any)["pattern"] = "^(env:.+|file:.+)$"
	github := deployerProperties["github"].(map[string]any)["properties"].(map[string]any)
	github["repository_id"].(map[string]any)["minimum"] = 1
	github["repository_owner_id"].(map[string]any)["minimum"] = 1
	github["ref"].(map[string]any)["pattern"] = "^refs/(heads|tags)/.+$"
	github["workflow_ref"].(map[string]any)["pattern"] = "^[^/@\\s]+/[^/@\\s]+/\\.github/workflows/[^/@\\s]+\\.ya?ml@refs/(heads|tags)/\\S+$"
	github["workflow_ref"].(map[string]any)["description"] = "The only workflow file allowed to deploy, exactly as GitHub's workflow_ref claim names it."

	tls := properties["tls"].(map[string]any)
	tlsRequired := tls["required"].([]string)
	for i, name := range tlsRequired {
		if name == "enabled" {
			tls["required"] = append(tlsRequired[:i], tlsRequired[i+1:]...)
			break
		}
	}
	tls["allOf"] = []any{map[string]any{
		"if": map[string]any{
			"properties": map[string]any{"enabled": map[string]any{"const": true}},
			"required":   []string{"enabled"},
		},
		"then": map[string]any{
			"required": []string{"domains"},
			"properties": map[string]any{
				"domains": map[string]any{"minItems": 1},
			},
		},
	}}
	tlsProperties := tls["properties"].(map[string]any)
	tlsProperties["domains"].(map[string]any)["items"].(map[string]any)["minLength"] = 1
	tlsProperties["domains"].(map[string]any)["description"] = "TLS names; wildcard names require DNS-01."

	pages := properties["pages"].(map[string]any)["properties"].(map[string]any)
	pages["keep"].(map[string]any)["minimum"] = 0
	pages["keep"].(map[string]any)["maximum"] = 100
	deploy := pages["deploy"].(map[string]any)["properties"].(map[string]any)
	deploy["host"].(map[string]any)["minLength"] = 1
	deploy["audience"].(map[string]any)["minLength"] = 1
	deploy["audience"].(map[string]any)["maxLength"] = 512

	audit := properties["audit"].(map[string]any)["properties"].(map[string]any)
	audit["dir"].(map[string]any)["minLength"] = 1
	audit["include"].(map[string]any)["minItems"] = 1
	audit["include"].(map[string]any)["uniqueItems"] = true
	audit["include"].(map[string]any)["items"].(map[string]any)["enum"] = []string{"access", "admin"}
	audit["admin_mode"].(map[string]any)["enum"] = []string{"required", "best_effort"}
	audit["max_segment_bytes"].(map[string]any)["minimum"] = 0
	audit["max_segment_bytes"].(map[string]any)["maximum"] = 1 << 40

	limits := properties["rate_limit"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"general", "auth", "identity"} {
		limit := limits[name].(map[string]any)["properties"].(map[string]any)
		limit["rate"].(map[string]any)["exclusiveMinimum"] = 0
		limit["burst"].(map[string]any)["minimum"] = 1
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config schema: %w", err)
	}
	return append(data, '\n'), nil
}

func schemaForType(t reflect.Type) (map[string]any, error) {
	if t.Kind() == reflect.Pointer {
		return schemaForType(t.Elem())
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		items, err := schemaForType(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("config schema: unsupported map key type %s", t.Key())
		}
		values, err := schemaForType(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": values}, nil
	case reflect.Struct:
		properties := make(map[string]any, t.NumField())
		required := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			child, err := schemaForType(field.Type)
			if err != nil {
				return nil, fmt.Errorf("config schema: field %s: %w", name, err)
			}
			properties[name] = child
			if !strings.Contains(options, "omitempty") {
				required = append(required, name)
			}
		}
		object := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			object["required"] = required
		}
		return object, nil
	default:
		return nil, fmt.Errorf("config schema: unsupported field type %s", t)
	}
}
