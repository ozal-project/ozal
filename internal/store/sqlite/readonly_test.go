package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyStatusDoesNotCreateMigrationTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state with spaces.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := ReadOnlyMigrationStatus(ctx, db, migrations)
	if err != nil {
		t.Fatalf("ReadOnlyMigrationStatus: %v", err)
	}
	if len(statuses) != len(migrations) {
		t.Fatalf("statuses = %d, migrations = %d", len(statuses), len(migrations))
	}
	for _, status := range statuses {
		if status.Applied {
			t.Errorf("migration %s unexpectedly applied", status.Migration.Name)
		}
	}
	var present bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_migrations')`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("read-only status created schema_migrations")
	}
}

func TestOpenReadOnlyAndIntegrityCheck(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state with spaces.db")
	writable, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writable.ExecContext(ctx, `CREATE TABLE sample (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := writable.ExecContext(ctx, `INSERT INTO sample VALUES ('ok')`); err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}

	readonly, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer readonly.Close()
	if err := IntegrityCheck(ctx, readonly); err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	var value string
	if err := readonly.QueryRowContext(ctx, `SELECT value FROM sample`).Scan(&value); err != nil || value != "ok" {
		t.Fatalf("read value = %q, error = %v", value, err)
	}
	if _, err := readonly.ExecContext(ctx, `INSERT INTO sample VALUES ('no')`); err == nil {
		t.Fatal("read-only connection accepted a write")
	}
}

func TestOpenReadOnlyDoesNotCreateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := OpenReadOnly(context.Background(), path); err == nil {
		t.Fatal("OpenReadOnly accepted a missing file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing database was created: %v", err)
	}
}
