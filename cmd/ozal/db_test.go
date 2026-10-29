package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/store/sqlite"
)

func prepareStateDatabase(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE recovery_fixture (value TEXT NOT NULL)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO recovery_fixture VALUES ('preserved')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDBBackupAndRestoreCreatePrivateSnapshots(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "state.db")
	backup := filepath.Join(dir, "backup.db")
	restored := filepath.Join(dir, "restored.db")
	prepareStateDatabase(t, ctx, source)

	var out bytes.Buffer
	if err := runDB(ctx, []string{"backup", "-db", source, "-out", backup}, &out); err != nil {
		t.Fatalf("backup: %v (%s)", err, out.String())
	}
	checkSnapshot(t, ctx, backup)
	if info, err := os.Stat(backup); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); hasUnixPermissions() && got != 0o600 {
		t.Fatalf("backup permissions = %o, want 600", got)
	}

	if err := runDB(ctx, []string{"restore", "-from", backup, "-to", restored}, &out); err != nil {
		t.Fatalf("restore: %v (%s)", err, out.String())
	}
	checkSnapshot(t, ctx, restored)
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was changed or removed: %v", err)
	}
}

func TestDBRestoreInvalidatesSessionsUnlessExplicitlyKept(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "state.db")
	prepareStateDatabase(t, ctx, source)
	db, err := sqlite.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id_hash, subject, email, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		bytes.Repeat([]byte{7}, 32), "subject-1", "user@example.test", 100, 200); err != nil {
		db.Close()
		t.Fatal(err)
	}
	const userID = "0123456789abcdef0123456789abcdef"
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, handle, email, created_at) VALUES (?, ?, ?, ?)`,
		userID, bytes.Repeat([]byte{8}, 32), "user@example.test", 100); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO passkeys (credential_id, user_id, credential_json, backup_eligible, created_at) VALUES (?, ?, ?, ?, ?)`,
		bytes.Repeat([]byte{9}, 16), userID, "{}", 0, 100); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "older-backup.db")
	if err := runDB(ctx, []string{"backup", "-db", source, "-out", backup}, &bytes.Buffer{}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	current, err := sqlite.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		current.Close()
		t.Fatal(err)
	}
	if _, err := current.ExecContext(ctx, `UPDATE passkeys SET revoked_at = 300`); err != nil {
		current.Close()
		t.Fatal(err)
	}
	if _, err := current.ExecContext(ctx, `UPDATE users SET disabled_at = 300`); err != nil {
		current.Close()
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		keepSessions bool
		want         int
	}{
		{name: "default invalidates sessions", want: 0},
		{name: "explicit keep preserves sessions", keepSessions: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"restore", "-from", backup, "-to", filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".db")}
			if tc.keepSessions {
				args = append(args, "-keep-sessions")
			}
			if err := runDB(ctx, args, &bytes.Buffer{}); err != nil {
				t.Fatalf("restore: %v", err)
			}
			restored := args[4]
			restoredDB, err := sqlite.OpenReadOnly(ctx, restored)
			if err != nil {
				t.Fatal(err)
			}
			defer restoredDB.Close()
			var count int
			if err := restoredDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != tc.want {
				t.Fatalf("restored session count = %d, want %d", count, tc.want)
			}
			var activePasskeys, enabledUsers int
			if err := restoredDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkeys WHERE revoked_at IS NULL`).Scan(&activePasskeys); err != nil {
				t.Fatal(err)
			}
			if err := restoredDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE disabled_at IS NULL`).Scan(&enabledUsers); err != nil {
				t.Fatal(err)
			}
			if activePasskeys != 1 || enabledUsers != 1 {
				t.Fatalf("historical identity state = active passkeys %d, enabled users %d; want 1 each from the backup timestamp", activePasskeys, enabledUsers)
			}
		})
	}
}

func checkSnapshot(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	db, err := sqlite.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.IntegrityCheck(ctx, db); err != nil {
		t.Fatalf("integrity check %s: %v", path, err)
	}
	var value string
	if err := db.QueryRowContext(ctx, `SELECT value FROM recovery_fixture`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("snapshot value = %q, error = %v", value, err)
	}
}

func TestDBBackupAndRestoreNeverOverwrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "state.db")
	target := filepath.Join(dir, "exists.db")
	prepareStateDatabase(t, ctx, source)
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runDB(ctx, []string{"backup", "-db", source, "-out", target}, &bytes.Buffer{}); err == nil {
		t.Fatal("backup overwrote an existing destination")
	}
	if err := runDB(ctx, []string{"restore", "-from", source, "-to", target}, &bytes.Buffer{}); err == nil {
		t.Fatal("restore overwrote an existing destination")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file = %q, error = %v", data, err)
	}
}

func TestDBBackupRefusesSymlinkSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "state.db")
	link := filepath.Join(dir, "link.db")
	prepareStateDatabase(t, ctx, source)
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := runDB(ctx, []string{"backup", "-db", link, "-out", filepath.Join(dir, "backup.db")}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("backup accepted symlink source: %v", err)
	}
}

func TestDBBackupDoesNotPublishInvalidSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "invalid.db")
	destination := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(source, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runDB(context.Background(), []string{"backup", "-db", source, "-out", destination}, &bytes.Buffer{}); err == nil {
		t.Fatal("backup accepted an invalid SQLite database")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("invalid source left a published destination: %v", err)
	}
}
