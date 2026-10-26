package sqlite

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// ErrMembershipChanged refuses an edit based on an obsolete read.
var ErrMembershipChanged = errors.New("membership changed; refresh before editing")

// NormalizeGroups bounds the directory's group identifiers without changing
// their case-sensitive meaning. Nil/empty means no memberships.
func NormalizeGroups(groups []string) ([]string, error) {
	if len(groups) > 64 {
		return nil, errors.New("at most 64 groups are allowed")
	}
	out := make([]string, 0, len(groups))
	for _, raw := range groups {
		group := strings.TrimSpace(raw)
		if group == "" || len(group) > 128 || strings.ContainsFunc(group, unicode.IsControl) {
			return nil, errors.New("group names must contain 1 to 128 bytes without control characters")
		}
		out = append(out, group)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// SetGroups atomically changes native membership and ends that person's
// sessions. Expected is required optimistic concurrency, not a policy input.
func (s *Identity) SetGroups(ctx context.Context, email string, expected, groups []string) error {
	next, err := NormalizeGroups(groups)
	if err != nil {
		return err
	}
	before, err := NormalizeGroups(expected)
	if err != nil {
		return err
	}
	encoded, err := encodeGroups(next)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin membership update: %w", err)
	}
	defer tx.Rollback()
	user, err := userByEmail(ctx, tx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return err
	}
	current, err := NormalizeGroups(user.Groups)
	if err != nil {
		return err
	}
	if !slices.Equal(before, current) {
		return ErrMembershipChanged
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET groups_json = ? WHERE id = ?`, encoded, user.ID); err != nil {
		return fmt.Errorf("update membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE subject = ? AND amr = ?`, user.Subject(), "passkey"); err != nil {
		return fmt.Errorf("end membership sessions: %w", err)
	}
	return tx.Commit()
}
