package sqlite

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/switchboard-project/switchboard/internal/auth"
)

func (s *Sessions) ListOwned(ctx context.Context, subject string) ([]auth.SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id_hash, created_at, last_seen_at, expires_at, amr FROM sessions WHERE subject = ? AND parent_hash IS NULL ORDER BY created_at DESC, id_hash`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auth.SessionInfo{}
	for rows.Next() {
		var id []byte
		var created, seen, issued int64
		var method string
		if err := rows.Scan(&id, &created, &seen, &issued, &method); err != nil {
			return nil, err
		}
		expiry := s.expiresAt(time.Unix(created, 0), time.Unix(seen, 0), time.Unix(issued, 0))
		if !s.now().Before(expiry) {
			continue
		}
		out = append(out, auth.SessionInfo{ID: hex.EncodeToString(id), Created: time.Unix(created, 0), LastSeen: time.Unix(seen, 0), Expires: expiry, Method: method})
	}
	return out, rows.Err()
}

func (s *Sessions) RevokeOwned(ctx context.Context, subject, id string, all bool) error {
	if all {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE subject = ?`, subject)
		return err
	}
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 32 {
		return auth.ErrSessionNotFound
	}
	// A login's host sessions end with it (ID-015).
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE subject = ?1 AND (id_hash = ?2 OR parent_hash = ?2)`, subject, raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return auth.ErrSessionNotFound
	}
	return nil
}
