package main

import (
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestStartPlainFailsSynchronouslyOnBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()

	cfg := &config.Config{Listen: busy.Addr().String()}
	if _, err := startPlain(cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("startPlain accepted an occupied port; the bind error must surface at startup")
	}
}

func TestStartPlainServesOnAFreePort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := probe.Addr().String()
	probe.Close()

	cfg := &config.Config{Listen: addr}
	server, err := startPlain(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	if err != nil {
		t.Fatalf("startPlain: %v", err)
	}
	defer server.Close()

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("response = %d %q, want 200 ok", resp.StatusCode, body)
	}
}

// Every one-shot command must be reachable through the table that main()
// wraps in a signal-aware context.
func TestSubcommandsAreRegistered(t *testing.T) {
	registered := subcommands()
	for _, name := range []string{"tunnel", "tunnel-spike", "connector", "migrate"} {
		if registered[name] == nil {
			t.Errorf("subcommand %q is not registered", name)
		}
	}
}
