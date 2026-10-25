package config

import (
	"strings"
	"testing"
)

func TestParseRouteWithMultipleUpstreams(t *testing.T) {
	data := []byte(`{"listen":"127.0.0.1:0","routes":[{"host":"app.localhost","upstreams":["http://one.local","https://two.local/base"],"health_path":"/healthz"}]}`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	route := cfg.Routes[0]
	if got := route.UpstreamTargets(); len(got) != 2 || got[0] != "http://one.local" || got[1] != "https://two.local/base" {
		t.Fatalf("UpstreamTargets() = %v", got)
	}
	if got := route.Destination(); got != "http://one.local, https://two.local/base" {
		t.Fatalf("Destination() = %q", got)
	}
	got := route.UpstreamTargets()
	got[0] = "http://mutated.local"
	if route.Upstreams[0] != "http://one.local" {
		t.Fatalf("UpstreamTargets returned a mutable alias: %v", route.Upstreams)
	}
	clone := CloneRoute(route)
	clone.Upstreams[1] = "http://mutated.local"
	if route.Upstreams[1] != "https://two.local/base" {
		t.Fatalf("CloneRoute shared upstream slice: %v", route.Upstreams)
	}
}

func TestParseRejectsInvalidUpstreamLists(t *testing.T) {
	base := `{"listen":"127.0.0.1:0","routes":[{"host":"app.localhost","upstreams":["http://one.local","http://two.local"]}]}`
	tests := []struct {
		name string
		json string
	}{
		{name: "only one upstream", json: strings.Replace(base, `,"http://two.local"`, "", 1)},
		{name: "duplicate target", json: strings.Replace(base, `"http://two.local"`, `"http://one.local"`, 1)},
		{name: "unsupported scheme", json: strings.Replace(base, `"http://two.local"`, `"ftp://two.local"`, 1)},
		{name: "target and upstreams", json: strings.Replace(base, `"upstreams"`, `"target":"http://single.local","upstreams"`, 1)},
		{name: "missing destination", json: `{"listen":"127.0.0.1:0","routes":[{"host":"app.localhost","upstreams":[]}]}`},
		{name: "empty upstream list beside target", json: `{"listen":"127.0.0.1:0","routes":[{"host":"app.localhost","target":"http://single.local","upstreams":[]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.json)); err == nil {
				t.Fatalf("Parse accepted invalid upstream list: %s", test.json)
			}
		})
	}
}
