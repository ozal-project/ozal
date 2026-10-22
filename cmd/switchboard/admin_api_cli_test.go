package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/admin"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

// staticManager serves a fixed config to the admin handler.
type staticManager struct{ cfg config.Config }

func (m staticManager) Config() config.Config                     { return m.cfg }
func (m staticManager) RouteStatuses() []routemanager.RouteStatus { return nil }
func (m staticManager) Reload() error                             { return nil }
func (m staticManager) ConfigDigest() string                      { return "" }
func (m staticManager) AddRoute(config.Route) error               { return nil }
func (m staticManager) DeleteRoute(string) error                  { return nil }

type liveAdmin struct {
	config   string // a config file whose admin.socket is the live socket
	recorder *events.Recorder
	db       *sql.DB
	handler  http.Handler
}

// startLiveAdmin runs the real admin handler on a unix socket over a
// migrated state database, as `switchboard -db` does.
func startLiveAdmin(t *testing.T) liveAdmin {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	if err := runMigrate(ctx, []string{"-db", dbPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	db, err := openMigratedDB(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	socket := filepath.Join(dir, "admin.sock")
	cfg := config.Config{
		Listen: "127.0.0.1:8080",
		Admin:  config.Admin{Socket: socket, Token: "env:SWITCHBOARD_TEST_TOKEN"},
		Auth:   config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Routes: []config.Route{{Host: "private.example.com", Target: "http://127.0.0.1:9000", Access: &config.AccessPolicy{}}},
	}
	raw, _ := json.Marshal(map[string]any{
		"listen": cfg.Listen,
		"admin":  map[string]string{"socket": socket},
		"routes": []map[string]string{{"host": "app.localhost", "target": "http://127.0.0.1:9000"}},
	})
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	recorder := &events.Recorder{}
	handler := admin.New(staticManager{cfg}, recorder, admin.WithPeople(sqlite.NewIdentity(db)), admin.WithConnectors(tunnel.NewRegistry(db)))
	listener, err := listenAdminSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ConnContext: admin.LocalConn}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return liveAdmin{config: configPath, recorder: recorder, db: db, handler: handler}
}

func (l liveAdmin) types() []string {
	var out []string
	for _, e := range l.recorder.Events() {
		out = append(out, string(e.Type)+"/"+e.Actor)
	}
	return out
}

func TestUserCommandsGoThroughTheAdminSocket(t *testing.T) {
	live := startLiveAdmin(t)
	ctx := context.Background()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runUser(ctx, append(args, "-config", live.config), &out)
		return out.String(), err
	}
	if out, err := run("invite", "-email", "ana@example.com", "-group", "owner"); err != nil || !strings.Contains(out, "https://auth.example.com/_/auth/enrol") {
		t.Fatalf("invite: %v %q", err, out)
	}
	// Recovery: accepted because the socket is the host.
	if _, err := run("invite", "-email", "ana@example.com", "-recover"); err != nil {
		t.Fatalf("recover on the socket: %v", err)
	}
	if out, err := run("list"); err != nil || !strings.Contains(out, "ana@example.com\tsb:") || !strings.Contains(out, "enabled\tgroups=owner") {
		t.Fatalf("list: %v %q", err, out)
	}
	if out, err := run("passkeys", "-email", "ana@example.com"); err != nil || out != "" {
		t.Fatalf("passkeys: %v %q", err, out)
	}
	if _, err := run("passkeys", "-email", "nobody@example.com"); err == nil || !strings.Contains(err.Error(), "no user nobody@example.com") {
		t.Fatalf("passkeys of an unknown user: %v", err)
	}
	if out, err := run("disable", "-email", "ana@example.com"); err != nil || !strings.Contains(out, "disabled ana@example.com") {
		t.Fatalf("disable: %v %q", err, out)
	}
	if _, err := run("enable", "-email", "nobody@example.com"); err == nil || !strings.Contains(err.Error(), "no user") {
		t.Fatalf("enable unknown: %v", err)
	}
	got := strings.Join(live.types(), " ")
	if got != "user.invited/local user.invited/local user.disabled/local" {
		t.Fatalf("events = %s", got)
	}
}

// Over TCP with the admin token, recovery is refused even from the CLI.
func TestUserRecoveryOverTCPIsRefused(t *testing.T) {
	live := startLiveAdmin(t)
	t.Setenv("SWITCHBOARD_TEST_TOKEN", "secret")
	server := httptest.NewServer(live.handler)
	defer server.Close()
	var out bytes.Buffer
	err := runUser(context.Background(), []string{"invite", "-email", "ana@example.com", "-recover", "-admin", server.URL, "-token", "env:SWITCHBOARD_TEST_TOKEN"}, &out)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("recover over TCP: %v", err)
	}
}

func TestConnectorCommandsGoThroughTheAdminSocket(t *testing.T) {
	live := startLiveAdmin(t)
	ctx := context.Background()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runConnector(ctx, append(args, "-config", live.config), &out)
		return strings.TrimSpace(out.String()), err
	}
	old, next := writeCert(t, t.TempDir()), writeCert(t, t.TempDir())
	if out, err := run("current", "-name", "portfolio"); err != nil || out != "none" {
		t.Fatalf("current before enrol: %v %q", err, out)
	}
	if _, err := run("enroll", "-name", "portfolio", "-cert", old); err != nil {
		t.Fatal(err)
	}
	expected, err := run("current", "-name", "portfolio")
	if err != nil || len(expected) != 64 {
		t.Fatalf("current: %v %q", err, expected)
	}
	if _, err := run("rotate", "-name", "portfolio", "-cert", next, "-expected", expected); err != nil {
		t.Fatal(err)
	}
	third := writeCert(t, t.TempDir())
	if _, err := run("rotate", "-name", "portfolio", "-cert", third, "-expected", expected); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("stale rotate: %v", err)
	}
	if _, err := run("revoke", "-name", "portfolio"); err != nil {
		t.Fatal(err)
	}
	if out, err := run("list"); err != nil || strings.Count(out, "revoked ") != 2 {
		t.Fatalf("list: %v %q", err, out)
	}
	got := strings.Join(live.types(), " ")
	if got != "connector.enrolled/local connector.rotated/local connector.revoked/local" {
		t.Fatalf("events = %s", got)
	}
}

func TestAPICommandsNeedAnAdminTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:8080","routes":[{"host":"app.localhost","target":"http://127.0.0.1:9000"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runUser(context.Background(), []string{"list", "-config", path}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no admin.socket") {
		t.Fatalf("no socket configured: %v", err)
	}
}
