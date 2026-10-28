package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/switchboard-project/switchboard/internal/admin"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/routemanager"
)

// socketPath returns a short socket path: sun_path is 104 bytes on macOS and
// t.TempDir() embeds the test name. Windows refuses admin.socket, so the
// socket tests skip there.
func socketPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("admin.socket is refused on Windows")
	}
	dir, err := os.MkdirTemp("", "sb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "admin.sock")
}

func TestListenAdminSocketIsRefusedOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	if listener, err := listenAdminSocket(filepath.Join(t.TempDir(), "a.sock")); err == nil {
		listener.Close()
		t.Fatal("admin socket opened on Windows, where its file mode protects nothing")
	}
}

func TestListenAdminSocketIsPrivateAndReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)
	// A socket file left behind, as after a crash.
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()

	listener, err := listenAdminSocket(path)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	defer listener.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 600", perm)
	}
}

func TestListenAdminSocketRefusesRegularFile(t *testing.T) {
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if listener, err := listenAdminSocket(path); err == nil {
		listener.Close()
		t.Fatal("regular file replaced by a socket")
	}
	if body, _ := os.ReadFile(path); string(body) != "keep me" {
		t.Fatal("regular file was modified")
	}
}

// The socket marks requests local end to end: a mutation with no token at
// all is admitted.
func TestAdminSocketRequestNeedsNoBearer(t *testing.T) {
	path := socketPath(t)
	listener, err := listenAdminSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Handler:     admin.New(&reloadOnly{}, nil),
		ConnContext: admin.LocalConn,
	}
	go server.Serve(listener)
	defer server.Close()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	resp, err := client.Post("http://localhost/_/reload", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload over socket = %d, want 200", resp.StatusCode)
	}
}

// reloadOnly is the smallest route manager the admin handler accepts.
type reloadOnly struct{}

func (reloadOnly) Config() config.Config                     { return config.Config{} }
func (reloadOnly) RouteStatuses() []routemanager.RouteStatus { return nil }
func (reloadOnly) Reload() error                             { return nil }
func (reloadOnly) ConfigDigest() string                      { return "" }
func (reloadOnly) AddRoute(config.Route) error               { return nil }
func (reloadOnly) DeleteRoute(string) error                  { return nil }
