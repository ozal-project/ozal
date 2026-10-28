package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
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
	for _, name := range []string{"completion", "db", "doctor", "init", "schema", "version", "tunnel", "tunnel-spike", "connector", "migrate"} {
		if registered[name] == nil {
			t.Errorf("subcommand %q is not registered", name)
		}
	}
}

func TestStartupIncompleteAuditTailPointsToRepairWithoutLeakingDetails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("audit owner-only file modes are unsupported on Windows")
	}
	for _, tc := range []struct {
		name       string
		auditMode  string
		withDBPath bool
	}{
		{name: "required"},
		{name: "best_effort", auditMode: "best_effort", withDBPath: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			auditDir := filepath.Join(root, "private-audit-dir")
			if err := os.Mkdir(auditDir, 0o700); err != nil {
				t.Fatal(err)
			}
			segmentPath := filepath.Join(auditDir, "audit-000001.jsonl")
			if err := os.WriteFile(segmentPath, []byte(`{"raw-tail-marker":"partial`), 0o600); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(root, "config.json")
			route := config.Route{Host: "app.localhost", Target: "http://127.0.0.1:1", AuditMode: tc.auditMode}
			configData, err := json.Marshal(config.Config{
				Listen: "127.0.0.1:0",
				Routes: []config.Route{route},
				Audit:  &config.AuditConfig{Dir: auditDir, Include: []string{"access"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, configData, 0o600); err != nil {
				t.Fatal(err)
			}

			args := []string{"run", ".", "-config", configPath}
			if tc.withDBPath {
				// If best-effort audit incorrectly permits startup past the torn
				// journal, opening this directory as SQLite makes the process exit
				// quickly instead of leaving a server running in the test.
				args = append(args, "-db", root)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "go", args...)
			output, runErr := command.CombinedOutput()
			if runErr == nil {
				t.Fatalf("Switchboard started with an unterminated audit tail: %s", output)
			}
			if ctx.Err() != nil {
				t.Fatalf("Switchboard startup timed out: %s", output)
			}
			if !strings.Contains(string(output), "switchboard audit repair") {
				t.Fatalf("startup failure does not point to audit repair: %s", output)
			}
			for _, sensitive := range []string{auditDir, "audit-000001.jsonl", "last record is not newline terminated", "raw-tail-marker"} {
				if strings.Contains(string(output), sensitive) {
					t.Fatalf("startup failure leaked %q: %s", sensitive, output)
				}
			}
		})
	}
}
