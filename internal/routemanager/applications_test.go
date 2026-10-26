package routemanager

import (
	"context"
	"errors"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplicationEditRejectsStaleSnapshotAndPersistsAfterRestart(t *testing.T) {
	path := writeConfig(t, routeConfig("http://127.0.0.1:3000"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	before := manager.ConfigDigest()
	route := config.Route{Host: "new.localhost", Target: "http://127.0.0.1:3001"}
	if err := manager.PutApplication(route, before); err != nil {
		t.Fatal(err)
	}
	if err := manager.PutApplication(config.Route{Host: "evil.localhost", Target: "http://127.0.0.1:3002"}, before); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("stale write accepted")
	}
	installed := manager.Config()
	if len(installed.Routes) != 2 {
		t.Fatal("failed edit affected snapshot")
	}
	fresh, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Config().Routes) != 2 {
		t.Fatal("edit did not survive restart")
	}
}

func TestRemovingApplicationPreservesContentAndRefusesStaleEdit(t *testing.T) {
	path := writeConfig(t, routeConfig("http://127.0.0.1:3000"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PutApplication(config.Route{Host: "trial.localhost", Target: "http://127.0.0.1:3001"}, manager.ConfigDigest()); err != nil {
		t.Fatal(err)
	}
	reviewed := manager.ConfigDigest()
	if err := manager.RemoveApplication("trial.localhost", reviewed); err != nil {
		t.Fatal(err)
	}
	if len(manager.Config().Routes) != 1 {
		t.Fatal("removed other applications")
	}
	if err := manager.RemoveApplication("app.localhost", reviewed); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("stale removal accepted")
	}
	if err := manager.RemoveApplication("app.localhost", manager.ConfigDigest()); err == nil {
		t.Fatal("removed last route")
	}
}

func TestOperationalEditsKeepHostConfigAndSurviveRestart(t *testing.T) {
	cfg := routeConfig("http://127.0.0.1:3000")
	cfg.RuntimeFile = filepath.Join(t.TempDir(), "runtime.json")
	operational, err := config.OperatingJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RuntimeFile, operational, 0600); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, cfg)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PutApplication(config.Route{Host: "new.localhost", Target: "http://127.0.0.1:3001"}, manager.ConfigDigest()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("root-owned host configuration was changed")
	}
	restarted, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Config().Routes) != 2 {
		t.Fatal("runtime config did not survive restart")
	}
	if err := restarted.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(restarted.Config().Routes) != 2 {
		t.Fatal("reload lost operational state")
	}
}

func TestExternalRootEditRefusesConsoleMutationWithoutReportingWrongDigest(t *testing.T) {
	cfg := routeConfig("http://127.0.0.1:3000")
	cfg.RuntimeFile = filepath.Join(t.TempDir(), "runtime.json")
	path := writeConfig(t, cfg)
	if err := config.PersistOperating(path, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := New(ctx, path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	before := manager.ConfigDigest()
	runtimeBefore, _ := os.ReadFile(cfg.RuntimeFile)
	cfg.Admin.Token = "env:NEW_ADMIN_TOKEN_REFERENCE"
	overwriteConfig(t, path, cfg)
	if err := manager.PutApplication(config.Route{Host: "new.localhost", Target: "http://127.0.0.1:3001"}, before); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("console overwrote a root change")
	}
	runtimeAfter, _ := os.ReadFile(cfg.RuntimeFile)
	if string(runtimeBefore) != string(runtimeAfter) || manager.ConfigDigest() != before || len(manager.Config().Routes) != 1 {
		t.Fatal("stale save changed policy or reported an uninstalled source digest")
	}
}
