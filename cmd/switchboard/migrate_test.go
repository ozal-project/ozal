package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMigrateAppliesThenReportsUpToDate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	ctx := context.Background()

	var out bytes.Buffer
	if err := runMigrate(ctx, []string{"-db", dbPath}, &out); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if !strings.Contains(out.String(), "applied 001_create_sessions") {
		t.Fatalf("first run output = %q, want applied 001_create_sessions", out.String())
	}

	out.Reset()
	if err := runMigrate(ctx, []string{"-db", dbPath}, &out); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if !strings.Contains(out.String(), "up to date") {
		t.Fatalf("second run output = %q, want up to date", out.String())
	}

	out.Reset()
	if err := runMigrate(ctx, []string{"-db", dbPath, "-status"}, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "001_create_sessions\tapplied") {
		t.Fatalf("status output = %q, want 001_create_sessions applied", out.String())
	}
}

func TestRunMigrateStatusOnFreshDatabaseIsPending(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	var out bytes.Buffer
	if err := runMigrate(context.Background(), []string{"-db", dbPath, "-status"}, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "001_create_sessions\tpending") {
		t.Fatalf("status output = %q, want 001_create_sessions pending", out.String())
	}
}

// The flag parser stops at the first positional argument, so before this
// check `migrate up -db X` silently migrated the default database.
func TestMigrateHonoursDBAfterTheActionWord(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	var out bytes.Buffer
	if err := runMigrate(context.Background(), []string{"up", "-db", dbPath}, &out); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("-db after the action was ignored: %v", err)
	}
	if err := runMigrate(context.Background(), []string{"status", "-db", dbPath}, &out); err != nil {
		t.Fatalf("migrate status: %v", err)
	}
	if err := runMigrate(context.Background(), []string{"sideways", "-db", dbPath}, &out); err == nil {
		t.Fatal("unknown action accepted")
	}
}
