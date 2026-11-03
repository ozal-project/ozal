package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrGroupExists refuses creating a group that is already defined.
var ErrGroupExists = errors.New("this group already exists")

// GroupCatalog keeps the groups an operator created explicitly.
type GroupCatalog struct {
	db  *sql.DB
	now func() time.Time
}

func NewGroupCatalog(db *sql.DB) *GroupCatalog { return &GroupCatalog{db: db, now: time.Now} }

// Defined lists the explicitly created groups.
func (c *GroupCatalog) Defined(ctx context.Context) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT name FROM groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Create defines a group with the same rules as a membership.
func (c *GroupCatalog) Create(ctx context.Context, raw string) (string, error) {
	names, err := NormalizeGroups([]string{raw})
	if err != nil {
		return "", err
	}
	result, err := c.db.ExecContext(ctx, `INSERT INTO groups (name, created_at) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`, names[0], c.now().Unix())
	if err != nil {
		return "", fmt.Errorf("create group: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return "", ErrGroupExists
	}
	return names[0], nil
}

// Delete removes a group from the catalog. The caller checks first that
// nobody belongs to it and no application requires it.
func (c *GroupCatalog) Delete(ctx context.Context, name string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM groups WHERE name = ?`, name)
	return err
}
