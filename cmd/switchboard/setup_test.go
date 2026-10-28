package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/switchboard-project/switchboard/internal/config"
)

func TestSetupProtectedState(t *testing.T) {
	base, err := os.MkdirTemp("", "sb-init-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	dir := filepath.Join(base, "edge")
	var output bytes.Buffer
	if err := runSetup(context.Background(), []string{"-dir", dir, "-target", "http://127.0.0.1:3000"}, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8888" || cfg.Auth.Passkeys == nil || len(cfg.Routes[0].Policy().RequiredGroups) != 1 {
		t.Fatal("setup was not private and protected")
	}
	if err := runDoctor(context.Background(), []string{"-config", filepath.Join(dir, "config.json"), "-db", filepath.Join(dir, "state.db")}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	secret, err := os.ReadFile(filepath.Join(dir, "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), string(secret)) {
		t.Fatal("setup printed the console token")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if err := runSetup(context.Background(), []string{"-dir", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("setup overwrote an installation")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("existing config changed")
	}
}

func TestSetupRefusesInvalidApplicationBeforeCreatingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "edge")
	if err := runSetup(context.Background(), []string{"-dir", dir, "-target", "file:///etc/passwd"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unsafe target accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("failed validation created a directory")
	}
}

func TestRuntimeInitCreatesPrivateOperationalFile(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "root.json")
	runtimePath := filepath.Join(dir, "runtime.json")
	rootConfig := config.Config{
		Listen:      "127.0.0.1:8888",
		RuntimeFile: runtimePath,
		Routes:      []config.Route{{Host: "app.localhost", Target: "http://127.0.0.1:3000"}},
	}
	rootData, err := json.Marshal(rootConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootPath, rootData, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runRuntime(context.Background(), []string{"init", "-config", rootPath}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runRuntime: %v", err)
	}
	info, err := os.Stat(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); hasUnixPermissions() && got != 0o600 {
		t.Fatalf("runtime file mode = %o, want 600", got)
	}
	loaded, err := config.Load(rootPath)
	if err != nil || len(loaded.Routes) != 1 || loaded.Routes[0].Host != "app.localhost" {
		t.Fatalf("initialized runtime config = %+v, error = %v", loaded, err)
	}
	before, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runRuntime(context.Background(), []string{"init", "-config", rootPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("runtime init overwrote existing operational state")
	}
	after, err := os.ReadFile(runtimePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("runtime file changed after repeat init: error = %v", err)
	}
}

func TestRuntimeInitRejectsRootAliases(t *testing.T) {
	for _, alias := range []string{"exact", "symlink", "hardlink"} {
		t.Run(alias, func(t *testing.T) {
			dir := t.TempDir()
			rootPath := filepath.Join(dir, "root.json")
			if err := os.WriteFile(rootPath, []byte(`{"listen":"127.0.0.1:8888","routes":[{"host":"app.localhost","target":"http://127.0.0.1:3000"}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			runtimePath := rootPath
			if alias == "symlink" || alias == "hardlink" {
				runtimePath = filepath.Join(dir, "root-alias.json")
				var err error
				if alias == "symlink" {
					err = os.Symlink(rootPath, runtimePath)
				} else {
					err = os.Link(rootPath, runtimePath)
				}
				if err != nil {
					t.Skipf("%s unavailable: %v", alias, err)
				}
			}
			rootConfig := config.Config{
				Listen:      "127.0.0.1:8888",
				RuntimeFile: runtimePath,
				Routes:      []config.Route{{Host: "app.localhost", Target: "http://127.0.0.1:3000"}},
			}
			rootData, err := json.Marshal(rootConfig)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rootPath, rootData, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := runRuntime(context.Background(), []string{"init", "-config", rootPath}, &bytes.Buffer{}); err == nil {
				t.Fatal("runtime init accepted an alias of the root config")
			}
			after, err := os.ReadFile(rootPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("root config changed after rejected alias: %v", err)
			}
		})
	}
}

func TestRuntimeInitRequiresRuntimeFile(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "root.json")
	if err := os.WriteFile(rootPath, []byte(`{"listen":"127.0.0.1:8888","routes":[{"host":"app.localhost","target":"http://127.0.0.1:3000"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRuntime(context.Background(), []string{"init", "-config", rootPath}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "runtime_file") {
		t.Fatalf("runRuntime error = %v, want runtime_file diagnostic", err)
	}
}
