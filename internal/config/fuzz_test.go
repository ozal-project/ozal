package config

import (
	"encoding/json"
	"testing"
)

// FuzzParse checks that arbitrary bytes either fail cleanly or produce a
// validated config that survives its own JSON round trip.
func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"listen":":8888","routes":[{"host":"app.localhost","target":"http://127.0.0.1:9000"}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"routes":[]}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"listen":"","routes":[{"host":"a","root":"/tmp"}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := Parse(data)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal validated config: %v", err)
		}
		if _, err := Parse(encoded); err != nil {
			t.Fatalf("validated config does not round-trip: %v\nconfig: %s", err, encoded)
		}
	})
}

// FuzzNormalizeHost checks that an accepted host normalizes to a stable value.
func FuzzNormalizeHost(f *testing.F) {
	f.Add("Example.COM.:443")
	f.Add("[::1]:8443")
	f.Add("localhost")
	f.Add("app.localhost:8888")
	f.Add("")

	f.Fuzz(func(t *testing.T, host string) {
		normalized, err := NormalizeHost(host)
		if err != nil {
			return
		}
		again, err := NormalizeHost(normalized)
		if err != nil {
			t.Fatalf("NormalizeHost(%q) = %q, which is rejected again: %v", host, normalized, err)
		}
		if again != normalized {
			t.Fatalf("NormalizeHost is not idempotent: %q -> %q -> %q", host, normalized, again)
		}
	})
}
