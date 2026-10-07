package main

import (
	"bytes"
	"context"
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
