package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOperatingProjectionPreservesHostConfigAndReferences(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "host.json")
	cfg := Config{Listen: "127.0.0.1:8888", RuntimeFile: filepath.Join(root, "runtime.json"), Admin: Admin{Token: "file:/root/admin.token"}, Routes: []Route{{Host: "old.localhost", Target: "http://127.0.0.1:3000"}}}
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("missing authoritative runtime file fell back to root")
	}
	operational, _ := OperatingJSON(cfg)
	if err := os.WriteFile(cfg.RuntimeFile, operational, 0600); err != nil {
		t.Fatal(err)
	}
	_, initial, err := LoadWithDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := Clone(cfg)
	changed.Routes[0].Host = "new.localhost"
	if err := PersistOperating(path, changed); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("operational edit rewrote host config")
	}
	loaded, digest, err := LoadWithDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Routes[0].Host != "new.localhost" || loaded.Admin.Token != "file:/root/admin.token" || digest == initial {
		t.Fatal("operational state or authority changed unexpectedly")
	}
	if err := os.WriteFile(cfg.RuntimeFile, []byte(`{"routes":[],"admin":{"listen":"0.0.0.0:9090"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("operational file changed host authority")
	}
}

func TestOperatingPathNeverReplacesItsRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "root.json")
	cfg := Config{Listen: "127.0.0.1:8888", Routes: []Route{{Host: "app.localhost", Target: "http://127.0.0.1:3000"}}}
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	cfg.RuntimeFile = path
	if err := PersistOperating(path, cfg); err == nil {
		t.Fatal("operational projection replaced root")
	}
	hardlink := filepath.Join(root, "alias.json")
	if err := os.Link(path, hardlink); err == nil {
		cfg.RuntimeFile = hardlink
		if err := PersistOperating(path, cfg); err == nil {
			t.Fatal("root hard-link alias accepted")
		}
	}
	symlink := filepath.Join(root, "symlink.json")
	if err := os.Symlink(path, symlink); err == nil {
		cfg.RuntimeFile = symlink
		if err := PersistOperating(path, cfg); err == nil {
			t.Fatal("root symlink alias accepted")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("root content changed")
	}
}

func TestLostOperatingPolicyNeverRestoresOlderPublicSeed(t *testing.T) {
	cfg := passkeyConfig()
	cfg.Routes[0].Access = nil
	cfg.RuntimeFile = filepath.Join(t.TempDir(), "runtime.json")
	path := filepath.Join(t.TempDir(), "root.json")
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Routes[0].Access = &AccessPolicy{RequiredGroups: []string{"ops"}}
	if err := PersistOperating(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !loaded.Routes[0].RequiresAuth() {
		t.Fatal("private operational policy not installed")
	}
	if err := os.Remove(cfg.RuntimeFile); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("lost private policy silently restored public root route")
	}
}
