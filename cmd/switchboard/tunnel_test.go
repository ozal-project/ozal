package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

func TestPersistentTunnelRejectsInvalidOptions(t *testing.T) {
	common := []string{"-cert", "unused.crt", "-key", "unused.key", "-ca", "unused-ca.crt", "-host", "portfolio.localhost"}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing mode", nil, "usage:"},
		{"unknown mode", []string{"init"}, "usage:"},
		{"missing identity", []string{"edge"}, "-cert, -key and -ca"},
		{"missing host", []string{"edge", "-cert", "x", "-key", "x", "-ca", "x"}, "-host"},
		{"public HTTP", append(append([]string{"edge"}, common...), "-connector", "portfolio", "-listen", "0.0.0.0:8888"), "loopback"},
		{"public ingress", append(append([]string{"edge"}, common...), "-connector", "portfolio", "-tunnel-listen", "0.0.0.0:8889"), "loopback"},
		{"missing connector", append([]string{"edge"}, common...), "-connector"},
		{"public edge destination", append(append([]string{"connect"}, common...), "-edge", "192.0.2.1:8889"), "loopback"},
		{"missing server identity", append(append([]string{"connect"}, common...), "-edge", "127.0.0.1:8889"), "-server-name"},
		{"positional argument", append(append([]string{"edge"}, common...), "extra"), "positional"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runTunnel(context.Background(), tc.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func persistentIdentityArgs(dir, role string) []string {
	return []string{"-cert", filepath.Join(dir, role+".crt"), "-key", filepath.Join(dir, role+".key"), "-ca", filepath.Join(dir, "ca.crt"), "-host", "portfolio.localhost"}
}

func TestPersistentEdgeRequiresMigratedRegistryAndActiveEnrolment(t *testing.T) {
	dir := t.TempDir()
	pki := filepath.Join(dir, "pki")
	if err := tunnel.InitLab(pki); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "state.db")
	args := append([]string{"edge"}, persistentIdentityArgs(pki, "edge")...)
	args = append(args, "-db", db, "-connector", "portfolio")
	ctx := context.Background()
	if err := runTunnel(ctx, args, io.Discard); err == nil || !strings.Contains(err.Error(), "pending migrations") {
		t.Fatalf("unmigrated registry: %v", err)
	}
	if err := runMigrate(ctx, []string{"-db", db}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := runTunnel(ctx, args, io.Discard); err == nil || !strings.Contains(err.Error(), "active enrolment") {
		t.Fatalf("missing enrolment: %v", err)
	}
	if err := runConnector(ctx, []string{"enroll", "-db", db, "-name", "portfolio", "-cert", filepath.Join(pki, "connector.crt")}, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The command must join all workers and release its DB/listeners.
	stopping, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if err := runTunnel(stopping, args, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := runConnector(ctx, []string{"revoke", "-db", db, "-name", "portfolio"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := runTunnel(ctx, args, io.Discard); err == nil || !strings.Contains(err.Error(), "active enrolment") {
		t.Fatalf("revoked enrolment: %v", err)
	}
}

func TestPersistentConnectorRejectsUnfixedOrigin(t *testing.T) {
	pki := filepath.Join(t.TempDir(), "pki")
	if err := tunnel.InitLab(pki); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"connect"}, persistentIdentityArgs(pki, "connector")...)
	args = append(args, "-edge", "127.0.0.1:1", "-server-name", "edge.localhost", "-upstream", "http://192.0.2.1:8000")
	if err := runTunnel(context.Background(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback origin: %v", err)
	}
}
