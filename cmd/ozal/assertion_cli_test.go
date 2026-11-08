package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/assertion"
	"github.com/ozal-project/ozal/internal/config"
)

func TestAssertionPublicKeyPrintsOnlyThePublicKey(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as an unprivileged user")
	}
	dir := t.TempDir()
	cfg := config.Config{Listen: "127.0.0.1:8888", IdentityAssertion: &config.IdentityAssertion{KeyDir: filepath.Join(dir, "keys")},
		Auth:   config.Auth{Passkeys: &config.Passkeys{RPID: "localhost", LoginURL: "http://localhost:8888"}},
		Routes: []config.Route{{Host: "app.localhost:8888", Target: "http://127.0.0.1:3000", Access: &config.AccessPolicy{RequiredGroups: []string{"members"}}, IdentityAssertion: &config.RouteAssertion{Audience: "app"}}}}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runAssertion(context.Background(), []string{"public-key", "-config", path}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "-----BEGIN PUBLIC KEY-----") || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("output:\n%s", out.String())
	}
	if _, err := assertion.ParsePublicKeyPEM(out.Bytes()); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	runAssertion(context.Background(), []string{"public-key", "-config", path}, &again)
	if again.String() != out.String() {
		t.Fatal("the key changed between runs")
	}
}
