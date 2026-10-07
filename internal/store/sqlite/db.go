// Package sqlite is Switchboard's persistent state store: an embedded SQLite
// database whose schema is defined by numbered, immutable migrations.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

// Open opens (creating if needed) the database at path with the settings
// every connection needs:
//
//   - foreign_keys(1): SQLite ignores FOREIGN KEY clauses unless enabled per
//     connection.
//   - journal_mode(WAL): readers don't block the writer.
//   - busy_timeout(5000): wait up to 5s for a lock instead of failing at once.
//   - _txlock=immediate: transactions take the write lock at BEGIN, so two
//     processes migrating at once serialize instead of deadlocking.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + query.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}
	return db, nil
}
