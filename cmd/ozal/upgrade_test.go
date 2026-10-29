package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeRefusesIncompletePlansBeforeTouchingAnything(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "ozal")
	db := filepath.Join(dir, "state.db")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"missing required flags", []string{"-unit", "sb"}, "usage"},
		{"relative binary", []string{"-unit", "sb", "-binary", filepath.Join("relative", "ozal"), "-sha256", "ab", "-config", "c.json"}, "absolute"},
		{"relative database", []string{"-unit", "sb", "-binary", binary, "-sha256", "ab", "-config", "c.json", "-db", filepath.Join("relative", "state.db"), "-user", "service"}, "absolute"},
		{"database without service account", []string{"-unit", "sb", "-binary", binary, "-sha256", "ab", "-config", "c.json", "-db", db}, "-db needs -user"},
		{"invalid retention", []string{"-unit", "sb", "-binary", binary, "-sha256", "ab", "-config", "c.json", "-keep", "0"}, "-keep"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := runUpgrade(context.Background(), c.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v: %v, want %q", c.args, err, c.want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("incomplete plan touched the installation directory: %v, %v", entries, err)
			}
		})
	}
}
