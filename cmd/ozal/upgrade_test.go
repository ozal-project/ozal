package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/updatelock"
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

func TestUpgradeRefusesToRunWhileAnotherUpdateHoldsTheLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "update.lock")
	lock, err := updatelock.Acquire(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	args := []string{"-unit", "ozal", "-binary", filepath.Join(dir, "ozal"), "-sha256", strings.Repeat("a", 64), "-config", filepath.Join(dir, "config.json"), "-lock", lockPath}
	err = runUpgrade(context.Background(), args, &bytes.Buffer{})
	if !errors.Is(err, updatelock.ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}
