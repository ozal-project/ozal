package config

import (
	"strings"
	"testing"
)

func TestParseRedirect(t *testing.T) {
	good := []string{"https://ozal.dev", "https://ozal.dev/", "https://ozal.dev/docs", "http://example.com:8080"}
	for _, raw := range good {
		if _, err := ParseRedirect("old.example.com", raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	bad := map[string]string{
		"relative":    "/docs",
		"no scheme":   "ozal.dev",
		"javascript":  "javascript:alert(1)",
		"ftp":         "ftp://ozal.dev",
		"user info":   "https://user:pass@ozal.dev",
		"query":       "https://ozal.dev/?next=x",
		"fragment":    "https://ozal.dev/#x",
		"own host":    "https://old.example.com/new",
		"own w/ port": "https://OLD.example.com:443",
	}
	for name, raw := range bad {
		if _, err := ParseRedirect("old.example.com:443", raw); err == nil {
			t.Errorf("%s (%s) accepted", name, raw)
		}
	}
}

func TestRedirectRouteValidation(t *testing.T) {
	base := func(r Route) Config {
		return Config{Listen: ":8080", Admin: Admin{Token: strings.Repeat("a", 32)}, Routes: []Route{r}}
	}
	if _, err := Validate(base(Route{Host: "switchboard.example.com", Redirect: "https://ozal.dev"})); err != nil {
		t.Fatalf("plain redirect refused: %v", err)
	}
	for name, r := range map[string]Route{
		"with target": {Host: "a.example.com", Redirect: "https://ozal.dev", Target: "http://127.0.0.1:1"},
		"protected":   {Host: "a.example.com", Redirect: "https://ozal.dev", AuthRequired: true},
		"health path": {Host: "a.example.com", Redirect: "https://ozal.dev", HealthPath: "/"},
		"loop":        {Host: "a.example.com", Redirect: "https://a.example.com"},
		"bad url":     {Host: "a.example.com", Redirect: "not a url"},
	} {
		if _, err := Validate(base(r)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
