package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExactSnapshotSafetyAndPrivateOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('ok')"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := FromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Proof.Path != "app.js" {
		t.Fatal(snap.Proof)
	}
	output := filepath.Join(t.TempDir(), "site.tgz")
	if err := WriteNew(output, snap.Archive); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if err := WriteNew(output, snap.Archive); err == nil {
		t.Fatal("overwrote archive")
	}
	secret := "ghp_" + strings.Repeat("A", 36)
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := FromDir(dir); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("secret accepted or exposed", err)
	}
	os.Remove(filepath.Join(dir, "app.js"))
	if err := os.Symlink(output, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := FromDir(dir); err == nil {
		t.Fatal("symlink accepted")
	}
	os.Remove(filepath.Join(dir, "link"))
	os.WriteFile(filepath.Join(dir, ".env"), nil, 0600)
	if _, err := FromDir(dir); err == nil {
		t.Fatal("private path accepted")
	}
}
