package config

import "testing"

func TestParseAcceptsExplicitTrustedProxies(t *testing.T) {
	raw := `{"listen":"127.0.0.1:8888","trusted_proxies":["127.0.0.1","::1/128"],"routes":[{"host":"app.localhost","target":"http://127.0.0.1:8000"}]}`
	if _, err := Parse([]byte(raw)); err != nil {
		t.Fatalf("explicit trusted peers rejected: %v", err)
	}
}

func TestTrustedProxyValidationAndClone(t *testing.T) {
	cfg := Config{Listen: ":8888", TrustedProxies: []string{"127.0.0.1", "::1", "192.0.2.99/24", "::ffff:127.0.0.1/128"}, Routes: []Route{{Host: "app.localhost", Target: "http://127.0.0.1:8000"}}}
	normalized, err := Validate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1/32", "::1/128", "192.0.2.0/24", "127.0.0.1/32"}
	for i, prefix := range normalized.TrustedProxies {
		if prefix != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, prefix, want[i])
		}
	}
	if cfg.TrustedProxies[0] != "127.0.0.1" {
		t.Fatal("Validate mutated caller's slice")
	}
	clone := Clone(*normalized)
	clone.TrustedProxies[0] = "changed"
	if normalized.TrustedProxies[0] != want[0] {
		t.Fatal("Clone shares trusted proxy slice")
	}
	for _, bad := range []string{"", "localhost", "127.0.0.1:1234", "::1%lo", "10.0.0.0/33", "::/129", "::ffff:127.0.0.1/80"} {
		cfg.TrustedProxies = []string{bad}
		if _, err := Validate(cfg); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	cfg.TrustedProxies = nil
	normalized, err = Validate(cfg)
	if err != nil || len(normalized.TrustedProxies) != 0 {
		t.Fatalf("unexpected default trust: %+v %v", normalized, err)
	}
}
