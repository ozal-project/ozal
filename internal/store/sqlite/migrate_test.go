package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "switchboard.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func mustLoad(t *testing.T, fsys fstest.MapFS) []Migration {
	t.Helper()
	migrations, err := LoadMigrations(fsys)
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	return migrations
}

func file(sql string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(sql)} }

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return count == 1
}

func appliedVersions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	return versions
}

// The migrations shipped in the binary must always apply cleanly to an empty
// database — this is the test that catches a broken new migration file.
func TestEmbeddedMigrationsApplyToFreshDatabase(t *testing.T) {
	db, _ := openTestDB(t)
	migrations, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatalf("LoadMigrations(embedded): %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no embedded migrations found")
	}

	applied, err := Migrate(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) != len(migrations) {
		t.Fatalf("applied %d migrations, want %d", len(applied), len(migrations))
	}
	if !tableExists(t, db, "sessions") {
		t.Fatal("sessions table missing after migrating")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, _ := openTestDB(t)
	migrations := mustLoad(t, fstest.MapFS{
		"001_create_things.sql": file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`),
	})

	if _, err := Migrate(context.Background(), db, migrations); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	applied, err := Migrate(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second run applied %d migrations, want 0", len(applied))
	}
}

func TestMigrateAppliesOnlyPending(t *testing.T) {
	db, _ := openTestDB(t)
	fsys := fstest.MapFS{
		"001_create_things.sql": file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`),
	}
	if _, err := Migrate(context.Background(), db, mustLoad(t, fsys)); err != nil {
		t.Fatalf("Migrate v1: %v", err)
	}

	fsys["002_add_thing_name.sql"] = file(`ALTER TABLE things ADD COLUMN name TEXT`)
	applied, err := Migrate(context.Background(), db, mustLoad(t, fsys))
	if err != nil {
		t.Fatalf("Migrate v2: %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 2 {
		t.Fatalf("applied = %+v, want only version 2", applied)
	}
	if got := appliedVersions(t, db); len(got) != 2 {
		t.Fatalf("recorded versions = %v, want [1 2]", got)
	}
}

// Editing a migration after it ran would leave databases that applied the old
// text silently different from new ones. The runner must refuse.
func TestMigrateRejectsEditedAppliedMigration(t *testing.T) {
	db, _ := openTestDB(t)
	original := fstest.MapFS{
		"001_create_things.sql": file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`),
	}
	if _, err := Migrate(context.Background(), db, mustLoad(t, original)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	edited := fstest.MapFS{
		"001_create_things.sql": file(`CREATE TABLE things (id INTEGER PRIMARY KEY, extra TEXT)`),
	}
	_, err := Migrate(context.Background(), db, mustLoad(t, edited))
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	if _, err := MigrationStatus(context.Background(), db, mustLoad(t, edited)); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("MigrationStatus err = %v, want ErrChecksumMismatch", err)
	}
}

// A migration with several statements where a later one fails must leave no
// trace: neither its earlier statements nor its version row.
func TestMigrateRollsBackFailedMigration(t *testing.T) {
	db, _ := openTestDB(t)
	migrations := mustLoad(t, fstest.MapFS{
		"001_create_things.sql": file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`),
		"002_broken.sql": file(`
			CREATE TABLE half_done (id INTEGER PRIMARY KEY);
			THIS IS NOT SQL;
		`),
	})

	applied, err := Migrate(context.Background(), db, migrations)
	if err == nil {
		t.Fatal("Migrate succeeded, want error from broken migration")
	}
	if !strings.Contains(err.Error(), "002_broken") {
		t.Errorf("error %q does not name the failing migration", err)
	}
	if len(applied) != 1 || applied[0].Version != 1 {
		t.Fatalf("applied = %+v, want only version 1", applied)
	}
	if tableExists(t, db, "half_done") {
		t.Fatal("half_done exists: failed migration was not rolled back")
	}
	if got := appliedVersions(t, db); len(got) != 1 || got[0] != 1 {
		t.Fatalf("recorded versions = %v, want [1]", got)
	}
}

func TestMigrateRejectsDatabaseNewerThanBinary(t *testing.T) {
	db, _ := openTestDB(t)
	newer := fstest.MapFS{
		"001_create_things.sql":  file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`),
		"002_add_thing_name.sql": file(`ALTER TABLE things ADD COLUMN name TEXT`),
	}
	if _, err := Migrate(context.Background(), db, mustLoad(t, newer)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	older := fstest.MapFS{"001_create_things.sql": newer["001_create_things.sql"]}
	_, err := Migrate(context.Background(), db, mustLoad(t, older))
	if !errors.Is(err, ErrDatabaseNewer) {
		t.Fatalf("err = %v, want ErrDatabaseNewer", err)
	}
}

func TestLoadMigrationsValidatesSequence(t *testing.T) {
	ok := `SELECT 1`
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{
			name:    "gap in versions",
			fsys:    fstest.MapFS{"001_a.sql": file(ok), "003_c.sql": file(ok)},
			wantErr: "contiguous",
		},
		{
			name:    "duplicate version",
			fsys:    fstest.MapFS{"001_a.sql": file(ok), "001_b.sql": file(ok), "002_c.sql": file(ok)},
			wantErr: "contiguous",
		},
		{
			name:    "does not start at 001",
			fsys:    fstest.MapFS{"002_a.sql": file(ok)},
			wantErr: "contiguous",
		},
		{
			name:    "missing zero padding",
			fsys:    fstest.MapFS{"1_a.sql": file(ok)},
			wantErr: "name must look like",
		},
		{
			name:    "dash instead of underscore",
			fsys:    fstest.MapFS{"001-a.sql": file(ok)},
			wantErr: "name must look like",
		},
		{
			name:    "uppercase description",
			fsys:    fstest.MapFS{"001_Create.sql": file(ok)},
			wantErr: "name must look like",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMigrations(tt.fsys)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMigrationsIgnoresNonSQLFiles(t *testing.T) {
	migrations := mustLoad(t, fstest.MapFS{
		"README.md":        file("notes"),
		"001_create_a.sql": file(`CREATE TABLE a (id INTEGER)`),
	})
	if len(migrations) != 1 || migrations[0].Name != "create_a" {
		t.Fatalf("migrations = %+v, want only 001_create_a", migrations)
	}
}

func TestMigrationStatusReportsPending(t *testing.T) {
	db, _ := openTestDB(t)
	fsys := fstest.MapFS{"001_create_things.sql": file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`)}
	if _, err := Migrate(context.Background(), db, mustLoad(t, fsys)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	fsys["002_add_thing_name.sql"] = file(`ALTER TABLE things ADD COLUMN name TEXT`)

	statuses, err := MigrationStatus(context.Background(), db, mustLoad(t, fsys))
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if len(statuses) != 2 || !statuses[0].Applied || statuses[1].Applied {
		t.Fatalf("statuses = %+v, want [applied, pending]", statuses)
	}
	if statuses[0].AppliedAt.IsZero() {
		t.Error("applied migration has zero AppliedAt")
	}
}

// Two processes starting at once (e.g. a restart overlapping the old process)
// must not apply the same migration twice.
func TestConcurrentMigrateAppliesOnce(t *testing.T) {
	_, path := openTestDB(t)
	migrations := mustLoad(t, fstest.MapFS{
		"001_create_things.sql":  file(`CREATE TABLE things (id INTEGER PRIMARY KEY)`),
		"002_add_thing_name.sql": file(`ALTER TABLE things ADD COLUMN name TEXT`),
	})

	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := Open(context.Background(), path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			_, err = Migrate(context.Background(), db, migrations)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate: %v", err)
		}
	}

	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := appliedVersions(t, db); len(got) != 2 {
		t.Fatalf("recorded versions = %v, want [1 2]", got)
	}
}

func TestOpenConfiguresConnection(t *testing.T) {
	db, _ := openTestDB(t)
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
}

// Migration 005 adds last_seen_at to a live sessions table: existing rows
// count as last seen at their login, so the idle limit applies to them from
// the upgrade on instead of ending them all.
func TestSessionLastSeenMigrationBackfillsExistingRows(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	all, err := LoadMigrations(Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, all[:4]); err != nil {
		t.Fatalf("Migrate 001–004: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id_hash, subject, created_at, expires_at) VALUES (x'01', 'sub', 1800000000, 1800028800)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, all); err != nil {
		t.Fatalf("Migrate 005: %v", err)
	}
	var seen int64
	if err := db.QueryRowContext(ctx, `SELECT last_seen_at FROM sessions WHERE id_hash = x'01'`).Scan(&seen); err != nil || seen != 1800000000 {
		t.Fatalf("last_seen_at = %d, %v; want the creation time", seen, err)
	}
}
