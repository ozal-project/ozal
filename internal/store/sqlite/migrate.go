package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migrations returns the schema migrations compiled into this binary.
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic(err) // the embed pattern above guarantees the directory exists
	}
	return sub
}

// Migration is one numbered schema change. Once applied anywhere, its file is
// immutable: the checksum recorded at apply time must keep matching.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string // hex SHA-256 of the file contents
}

// Status reports whether one migration has been applied to a database.
type Status struct {
	Migration Migration
	Applied   bool
	AppliedAt time.Time
}

var (
	// ErrChecksumMismatch means an already-applied migration file changed.
	// Fix by restoring the original file and adding a new migration.
	ErrChecksumMismatch = errors.New("applied migration was modified")
	// ErrDatabaseNewer means the database has migrations this binary doesn't
	// know about — typically a downgrade. Refusing is safer than guessing.
	ErrDatabaseNewer = errors.New("database is newer than this binary")
)

var migrationName = regexp.MustCompile(`^(\d{3})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

// LoadMigrations reads every *.sql file at the root of fsys and validates the
// sequence: names must be NNN_snake_case.sql, versions must start at 1 and be
// contiguous, with no duplicates. Other files (e.g. a README) are ignored.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	paths, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(paths))
	for _, path := range paths {
		match := migrationName.FindStringSubmatch(path)
		if match == nil {
			return nil, fmt.Errorf("migration %q: name must look like 001_create_sessions.sql", path)
		}
		version, _ := strconv.Atoi(match[1]) // the regexp guarantees digits
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", path, err)
		}
		sum := sha256.Sum256(data)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     match[2],
			SQL:      string(data),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for i, m := range migrations {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 001: expected %03d, found %03d_%s", i+1, m.Version, m.Name)
		}
	}
	return migrations, nil
}

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    checksum   TEXT    NOT NULL,
    applied_at INTEGER NOT NULL
) STRICT`

type appliedRow struct {
	name      string
	checksum  string
	appliedAt time.Time
}

// Migrate applies every pending migration in order, each in its own
// transaction, and returns the ones it applied. It first verifies that
// already-applied migrations are unchanged and that the database is not ahead
// of this binary. A failing migration is rolled back completely and stops the
// run; earlier migrations stay applied.
func Migrate(ctx context.Context, db *sql.DB, migrations []Migration) ([]Migration, error) {
	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	applied, err := loadApplied(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := verifyApplied(applied, migrations); err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		ran, err := applyOne(ctx, db, m)
		if err != nil {
			return done, err
		}
		if ran {
			done = append(done, m)
		}
	}
	return done, nil
}

// applyOne runs m inside one transaction. With _txlock=immediate, BEGIN takes
// the write lock, so re-checking the version inside the transaction makes
// concurrent migrators safe: the second one sees the row and skips.
func applyOne(ctx context.Context, db *sql.DB, m Migration) (ran bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("migration %03d_%s: begin: %w", m.Version, m.Name, err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("migration %03d_%s: check version: %w", m.Version, m.Name, err)
	}
	if exists > 0 {
		return false, tx.Commit()
	}

	if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
		return false, fmt.Errorf("migration %03d_%s: %w", m.Version, m.Name, err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		m.Version, m.Name, m.Checksum, time.Now().Unix(),
	)
	if err != nil {
		return false, fmt.Errorf("migration %03d_%s: record: %w", m.Version, m.Name, err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("migration %03d_%s: commit: %w", m.Version, m.Name, err)
	}
	return true, nil
}

// MigrationStatus lists every known migration and whether db has it applied,
// after the same integrity checks Migrate performs.
func MigrationStatus(ctx context.Context, db *sql.DB, migrations []Migration) ([]Status, error) {
	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	applied, err := loadApplied(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := verifyApplied(applied, migrations); err != nil {
		return nil, err
	}
	statuses := make([]Status, len(migrations))
	for i, m := range migrations {
		row, ok := applied[m.Version]
		statuses[i] = Status{Migration: m, Applied: ok, AppliedAt: row.appliedAt}
	}
	return statuses, nil
}

func loadApplied(ctx context.Context, db *sql.DB) (map[int]appliedRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]appliedRow)
	for rows.Next() {
		var version int
		var row appliedRow
		var appliedAt int64
		if err := rows.Scan(&version, &row.name, &row.checksum, &appliedAt); err != nil {
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		row.appliedAt = time.Unix(appliedAt, 0)
		applied[version] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return applied, nil
}

func verifyApplied(applied map[int]appliedRow, migrations []Migration) error {
	known := make(map[int]Migration, len(migrations))
	for _, m := range migrations {
		known[m.Version] = m
	}
	for version, row := range applied {
		m, ok := known[version]
		if !ok {
			return fmt.Errorf("%w: it has migration %03d_%s", ErrDatabaseNewer, version, row.name)
		}
		if m.Checksum != row.checksum {
			return fmt.Errorf("%w: %03d_%s", ErrChecksumMismatch, m.Version, m.Name)
		}
	}
	return nil
}
