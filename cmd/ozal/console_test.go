package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

// A second enrol on a host that already has an administrator points to the
// console sign-in, not to recovery, which replaces that person's passkeys.
func TestSecondEnrolPointsToSignInInsteadOfRecovery(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	dir, err := os.MkdirTemp("", "ozal-enrol")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"an administrator already exists; use recover with their email"}`))
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	var out bytes.Buffer
	err = runConsole(context.Background(), []string{"enrol", "-admin", "unix:" + socket, "-url", "http://127.0.0.1:9090"}, &out)
	if err == nil {
		t.Fatal("second enrol succeeded")
	}
	for _, want := range []string{"an administrator already exists", "sign in with their passkey at http://ozal.localhost:9090/overview", "sudo ozal console recover -email"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "use recover with their email") {
		t.Fatalf("error still leads with recovery: %q", err)
	}
}

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
