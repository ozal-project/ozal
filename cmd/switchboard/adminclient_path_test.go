package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPathKeepsAnExplicitPathAndPrefersALocalFile(t *testing.T) {
	if got := resolveConfigPath("/srv/other.json"); got != "/srv/other.json" {
		t.Fatalf("explicit path changed to %q", got)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveConfigPath("config.json"); got != "config.json" {
		t.Fatalf("a config beside the shell was ignored: %q", got)
	}
}
