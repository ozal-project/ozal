package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidAppName refuses a name the console could not show on one line.
var ErrInvalidAppName = errors.New("use 1 to 60 characters for the name, without line breaks")

// AppNames keeps the operator's display name of each application, keyed by
// its normalized host.
type AppNames struct {
	db  *sql.DB
	now func() time.Time
}

func NewAppNames(db *sql.DB) *AppNames { return &AppNames{db: db, now: time.Now} }

// NormalizeAppName trims a name and checks it fits the column; "" clears it.
func NormalizeAppName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	if utf8.RuneCountInString(name) > 60 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidAppName
	}
	return name, nil
}

// Names returns every stored name by host.
func (s *AppNames) Names(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT host, name FROM app_names`)
	if err != nil {
		return nil, fmt.Errorf("list application names: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var host, name string
		if err := rows.Scan(&host, &name); err != nil {
			return nil, err
		}
		out[host] = name
	}
	return out, rows.Err()
}

// SetName names the application at host; an empty name removes it.
func (s *AppNames) SetName(ctx context.Context, host, raw string) error {
	name, err := NormalizeAppName(raw)
	if err != nil {
		return err
	}
	if name == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM app_names WHERE host = ?`, host)
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO app_names (host, name, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (host) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
		host, name, s.now().Unix())
	if err != nil {
		return fmt.Errorf("save application name: %w", err)
	}
	return nil
}
