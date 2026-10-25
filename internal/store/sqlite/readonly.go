package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
)

// OpenReadOnly opens an existing SQLite database without creating files or
// changing its journal mode. It is for diagnostics and recovery tooling.
func OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(5000)")
	db, err := sql.Open("sqlite", u.String()+"?"+query.Encode())
	if err != nil {
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	return db, nil
}

// IntegrityCheck runs SQLite's full consistency check without exposing its
// row contents, which can include database values in low-level errors.
func IntegrityCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return errors.New("SQLite integrity check could not run")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return errors.New("SQLite integrity check could not read its result")
		}
		count++
		if result != "ok" {
			return errors.New("SQLite integrity check failed")
		}
	}
	if err := rows.Err(); err != nil {
		return errors.New("SQLite integrity check could not finish")
	}
	if count == 0 {
		return errors.New("SQLite integrity check returned no result")
	}
	return nil
}

// ReadOnlyMigrationStatus reports pending migrations without creating the
// migration table. A new or unmigrated database is returned as all-pending.
func ReadOnlyMigrationStatus(ctx context.Context, db *sql.DB, migrations []Migration) ([]Status, error) {
	var present bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')`,
	).Scan(&present); err != nil {
		return nil, errors.New("could not inspect migration history")
	}
	if !present {
		statuses := make([]Status, len(migrations))
		for i, migration := range migrations {
			statuses[i] = Status{Migration: migration}
		}
		return statuses, nil
	}
	applied, err := loadApplied(ctx, db)
	if err != nil {
		return nil, errors.New("could not read migration history")
	}
	if err := verifyApplied(applied, migrations); err != nil {
		return nil, err
	}
	statuses := make([]Status, len(migrations))
	for i, migration := range migrations {
		row, ok := applied[migration.Version]
		statuses[i] = Status{Migration: migration, Applied: ok, AppliedAt: row.appliedAt}
	}
	return statuses, nil
}
