package main

import (
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestInviteReturnToMustBeAProtectedHost(t *testing.T) {
	cfg := config.Config{
		Auth: config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Routes: []config.Route{
			{Host: "private.example.com", Access: &config.AccessPolicy{}},
			{Host: "public.example.com"},
		},
	}
	query, err := inviteReturnTo(cfg, "https://private.example.com/notes?x=1")
	if err != nil || query != "?return_to=https%3A%2F%2Fprivate.example.com%2Fnotes%3Fx%3D1" {
		t.Fatalf("protected host: %q, %v", query, err)
	}
	if query, err := inviteReturnTo(cfg, ""); err != nil || query != "" {
		t.Fatalf("no destination: %q, %v", query, err)
	}
	for _, bad := range []string{"https://public.example.com/", "https://evil.example/", "//evil.example/", "javascript:alert(1)", "https://private.example.com@evil.example/"} {
		if _, err := inviteReturnTo(cfg, bad); err == nil || !strings.Contains(err.Error(), "protected host") {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
}
