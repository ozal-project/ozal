package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/switchboard-project/switchboard/internal/admin"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/pages"
	"github.com/switchboard-project/switchboard/internal/routemanager"
	"github.com/switchboard-project/switchboard/internal/static"
	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

// tokenOnlyManager is the smallest route manager the admin handler needs:
// these tests exercise only the site endpoints.
type tokenOnlyManager struct{}

func (tokenOnlyManager) Config() config.Config {
	return config.Config{Admin: config.Admin{Token: "deploy-secret"}}
}
func (tokenOnlyManager) RouteStatuses() []routemanager.RouteStatus { return nil }
func (tokenOnlyManager) Reload() error                             { return nil }
func (tokenOnlyManager) ConfigDigest() string                      { return "" }
func (tokenOnlyManager) AddRoute(config.Route) error               { return nil }
func (tokenOnlyManager) DeleteRoute(string) error                  { return nil }

func writeBuild(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSiteDeployListRollbackEndToEnd(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	store, err := pages.NewStore(ctx, db, t.TempDir(), 0, pages.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(admin.New(tokenOnlyManager{}, nil, admin.WithPages(store)))
	defer server.Close()
	site := static.Handler{Live: func() (pages.Live, bool) { return store.Live("blog") }}
	visit := func() string {
		rec := httptest.NewRecorder()
		site.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Body.String()
	}
	t.Setenv("SWITCHBOARD_ADMIN_TOKEN", "deploy-secret")
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runSite(ctx, append(args[:1:1], append([]string{"-admin", server.URL}, args[1:]...)...), &out)
		return out.String(), err
	}

	v1 := writeBuild(t, map[string]string{"index.html": "version one", "assets/app.js": "1"})
	out, err := run("deploy", "-site", "blog", v1)
	if err != nil || !strings.Contains(out, "deployed blog") || visit() != "version one" {
		t.Fatalf("deploy v1: %q, %v; site serves %q", out, err, visit())
	}
	firstID := strings.Fields(out)[2]

	if out, err := run("deploy", "-site", "blog", writeBuild(t, map[string]string{"index.html": "version two"})); err != nil || visit() != "version two" {
		t.Fatalf("deploy v2: %q, %v", out, err)
	}
	if out, err := run("list"); err != nil || strings.Count(out, firstID) != 1 || strings.Count(out, "*") != 1 {
		t.Fatalf("list: %q, %v", out, err)
	}
	if out, err := run("rollback", "-site", "blog", "-to", firstID); err != nil || visit() != "version one" {
		t.Fatalf("rollback: %q, %v", out, err)
	}

	// A wrong token never reaches the store.
	t.Setenv("SWITCHBOARD_ADMIN_TOKEN", "wrong")
	if _, err := run("list"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("list with a wrong token: %v", err)
	}
	if _, err := run("deploy", "-site", "blog", v1); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("deploy with a wrong token: %v", err)
	}
}

func TestPackSiteRefusesSymlinks(t *testing.T) {
	dir := writeBuild(t, map[string]string{"index.html": "x"})
	if err := os.Symlink(os.TempDir(), filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if _, err := pages.Pack(dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Pack followed or kept a symlink: %v", err)
	}
}

func TestAdminBaseKeepsTheTokenOffTheNetwork(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://127.0.0.1:9090":                          true,
		"http://localhost:9091/":                         true,
		"http://switchboard.localhost:9091":              true,
		"http://other.localhost:9091":                    false,
		"http://switchboard.localhost.evil.example:9091": false,
		"http://[::1]:9090":                              true,
		"https://edge.example.com":                       true,
		"http://10.8.0.1:9091":                           false,
		"http://edge.example.com":                        false,
		"ftp://127.0.0.1":                                false,
		"127.0.0.1:9090":                                 false,
	} {
		if _, err := adminBase(raw); (err == nil) != ok {
			t.Errorf("adminBase(%q) error = %v, want ok=%v", raw, err, ok)
		}
	}
}
