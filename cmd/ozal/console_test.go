package main

import (
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestPasskeyPlaceNamesTheLoginHostWhenAdministratorsUseIt(t *testing.T) {
	passkeys := &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}
	for _, tt := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"login host", config.Config{Admin: config.Admin{LoginHost: true}, Auth: config.Auth{Passkeys: passkeys}}, "The passkey is created on auth.example.com and you return to the console"},
		{"loopback console", config.Config{Auth: config.Auth{Passkeys: passkeys}}, "Passkeys use ozal.localhost"},
		{"unreadable config", config.Config{}, "Passkeys use ozal.localhost"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := passkeyPlace(tt.cfg); got != tt.want {
				t.Fatalf("passkeyPlace() = %q, want %q", got, tt.want)
			}
		})
	}
}
