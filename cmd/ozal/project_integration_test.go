package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/admin"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pages"
	"github.com/ozal-project/ozal/internal/routemanager"
)

type trustFixtureSites struct{}

func (trustFixtureSites) Live(string) (pages.Live, bool) { return pages.Live{}, false }

func TestProjectTrustCLIUsesLiveSocketAndPersistsOnlyRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-socket service operation")
	}
	dir, err := os.MkdirTemp("", "ozal-trust-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	old := config.ProjectGitHubTrust{RepositoryID: 1, RepositoryOwnerID: 2, Ref: "refs/heads/main", WorkflowRef: "old/repo/.github/workflows/site.yml@refs/heads/main"}
	cfg := config.Config{Listen: "127.0.0.1:8888", RuntimeFile: filepath.Join(dir, "runtime.json"), Pages: &config.Pages{Dir: filepath.Join(dir, "pages")},
		Routes: []config.Route{{Host: "site.localhost", Site: "site"}}, Projects: []config.Project{{Name: "site", Site: "site", Hostnames: []string{"site.localhost"}, Deployers: []config.ProjectDeployer{{GitHub: &old}, {Token: "file:/fixture/reference-only"}}}}}
	root := filepath.Join(dir, "host.json")
	if err := config.WriteAtomic(root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.PersistOperating(root, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := routemanager.New(ctx, root, time.Hour, routemanager.WithSites(trustFixtureSites{}))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0600); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	server := &http.Server{Handler: admin.New(manager, nil), ConnContext: admin.LocalConn}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-done }()
	backup := filepath.Join(dir, "before-trust.json")
	args := []string{"trust", "-admin", "unix:" + socket, "-project", "site", "-repository-id", "3", "-owner-id", "4", "-workflow-ref", "new/repo/.github/workflows/site.yml@refs/heads/main", "-apply", "-backup", backup}
	var out bytes.Buffer
	if err := runProject(ctx, args, &out); err != nil {
		t.Fatalf("migration: %v (%s)", err, out.String())
	}
	if manager.Config().Projects[0].Deployers[0].GitHub.RepositoryID != 3 {
		t.Fatal("live snapshot did not change")
	}
	after, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("root host config was rewritten")
	}
	reloaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Projects[0].Deployers[0].GitHub.RepositoryID != 3 || reloaded.Projects[0].Deployers[1].Token != "file:/fixture/reference-only" || reloaded.Routes[0].Host != "site.localhost" {
		t.Fatal("runtime trust or existing authority lost")
	}
	// Retry uses the already migrated live state and never overwrites recovery metadata.
	backupBefore, _ := os.ReadFile(backup)
	if err := runProject(ctx, args, &out); err != nil {
		t.Fatal(err)
	}
	backupAfter, _ := os.ReadFile(backup)
	if !bytes.Equal(backupBefore, backupAfter) {
		t.Fatal("retry overwrote previous trust")
	}
}
