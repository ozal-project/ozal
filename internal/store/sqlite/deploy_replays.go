package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// DeployReplays consumes domain-separated jti digests in an immediate SQLite
// transaction. Expired entries are reclaimed; live entries are never evicted
// to make capacity. The state survives handler and process replacement.
type DeployReplays struct {
	db       *sql.DB
	now      func() time.Time
	capacity int
}

func NewDeployReplays(db *sql.DB) *DeployReplays {
	return &DeployReplays{db: db, now: time.Now, capacity: 10000}
}
func (s *DeployReplays) Consume(ctx context.Context, jti string, expires time.Time) (bool, error) {
	now := s.now()
	if jti == "" || len(jti) > 256 || !expires.After(now) || expires.Sub(now) > 10*time.Minute {
		return false, errors.New("invalid deploy replay window")
	}
	digest := sha256.Sum256([]byte("switchboard-deploy-jti-v1\x00" + jti))
	key := hex.EncodeToString(digest[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM deploy_replays WHERE expires_at <= ?`, now.Unix()); err != nil {
		return false, err
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM deploy_replays WHERE jti_sha256 = ?`, key).Scan(&exists)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM deploy_replays`).Scan(&count); err != nil {
		return false, err
	}
	if count >= s.capacity {
		return false, errors.New("deploy replay capacity exhausted")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_replays (jti_sha256, expires_at) VALUES (?, ?)`, key, expires.Unix()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
