package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
)

// Sessions is the persistent auth.SessionBackend over migrations 001, 005
// and 006. The
// cookie carries the raw ID; only its SHA-256 is stored, so a leaked
// database file cannot be replayed as session cookies. Times are stored as
// Unix seconds, the same shape the migration defines.
type Sessions struct {
	db          *sql.DB
	lifetime    auth.Lifetime
	maxSessions int
	now         func() time.Time
}

var _ auth.SessionBackend = (*Sessions)(nil)

// NewSessions builds the store over an already-migrated database. It never
// runs migrations itself: applying schema changes is an explicit operator
// step (`switchboard migrate`), so a startup with a stale schema fails
// loudly instead of guessing.
func NewSessions(db *sql.DB, lifetime auth.Lifetime) *Sessions {
	return &Sessions{db: db, lifetime: lifetime, maxSessions: auth.MaxSessions, now: time.Now}
}

// purgeExpired is the WHERE clause for rows past either limit, under the
// current lifetime: the issued expiry, the absolute lifetime from creation,
// or (when set) the idle limit from the last recorded use.
func (s *Sessions) purgeExpired() (string, []any) {
	now := s.now()
	where := `expires_at <= ? OR created_at <= ?`
	args := []any{now.Unix(), now.Add(-s.lifetime.Absolute).Unix()}
	if s.lifetime.Idle > 0 {
		where += ` OR last_seen_at <= ?`
		args = append(args, now.Add(-s.lifetime.Idle).Unix())
	}
	return where, args
}

// Create stores a session in one transaction: it first deletes expired rows,
// under the current lifetime, then enforces capacity against live sessions only. The
// connection pool runs with _txlock=immediate, so the transaction takes the
// write lock at BEGIN and two concurrent logins serialize instead of both
// seeing the same free slot.
func (s *Sessions) Create(ctx context.Context, principal auth.Principal) (string, error) {
	id, err := auth.NewSessionID()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(id))
	groups, err := encodeGroups(principal.Groups)
	if err != nil {
		return "", err
	}
	now := s.now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin session transaction: %w", err)
	}
	defer tx.Rollback()

	where, args := s.purgeExpired()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE `+where, args...); err != nil {
		return "", fmt.Errorf("purge expired sessions: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		return "", fmt.Errorf("count sessions: %w", err)
	}
	if count >= s.maxSessions {
		return "", auth.ErrStoreFull
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, subject, email, email_verified, groups_json, created_at, expires_at, last_seen_at, auth_time, amr)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hash[:], principal.Subject, principal.Email, principal.EmailVerified, groups,
		now.Unix(), now.Add(s.lifetime.Absolute).Unix(), now.Unix(), storedAuthTime(principal.AuthTime), principal.Method,
	); err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit session: %w", err)
	}
	return id, nil
}

// Get returns the session for id, or ok=false when it is absent or expired.
// An expired row is evicted on lookup. A database error reads as "not
// authenticated": failing closed is the only safe direction here, and the
// error is logged for the operator.
func (s *Sessions) Get(ctx context.Context, id string) (auth.Session, bool) {
	hash := sha256.Sum256([]byte(id))
	var (
		subject     string
		email       string
		verified    int64
		groupsJSON  string
		expiresUnix int64
		createdUnix int64
		seenUnix    int64
		authUnix    int64
		method      string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT subject, email, email_verified, groups_json, expires_at, created_at, last_seen_at, auth_time, amr FROM sessions WHERE id_hash = ?`,
		hash[:],
	).Scan(&subject, &email, &verified, &groupsJSON, &expiresUnix, &createdUnix, &seenUnix, &authUnix, &method)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, false
	}
	if err != nil {
		slog.Default().Debug("session lookup failed", "error", err)
		return auth.Session{}, false
	}
	created, issued := time.Unix(createdUnix, 0), time.Unix(expiresUnix, 0)
	lastSeen := time.Unix(seenUnix, 0)
	now := s.now()
	// Apply the current snapshot policy without changing rows while a reload
	// candidate is being built. A longer lifetime never extends the issued
	// expiry.
	if !now.Before(s.expiresAt(created, lastSeen, issued)) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash[:])
		return auth.Session{}, false
	}
	// Record the use at most once per TouchInterval. A failed write only
	// means the idle limit counts from the previous use: earlier, never later.
	if now.Sub(lastSeen) >= auth.TouchInterval {
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?`, now.Unix(), hash[:]); err != nil {
			slog.Default().Debug("session touch failed", "error", err)
		} else {
			lastSeen = now
		}
	}
	var groups []string
	if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
		slog.Default().Debug("session groups are not readable", "error", err)
		groups = nil
	}
	return auth.Session{
		Principal: auth.Principal{
			Subject:       subject,
			Email:         email,
			EmailVerified: verified == 1,
			Groups:        groups,
			AuthTime:      timeOrZero(authUnix),
			Method:        method,
		},
		ExpiresAt: s.expiresAt(created, lastSeen, issued),
	}, true
}

// expiresAt is the earlier of the expiry issued at login and the current
// lifetime's rule.
func (s *Sessions) expiresAt(created, lastSeen, issued time.Time) time.Time {
	if policy := s.lifetime.Expiry(created, lastSeen); policy.Before(issued) {
		return policy
	}
	return issued
}

// Delete removes a session by ID; deleting a missing session is fine.
func (s *Sessions) Delete(ctx context.Context, id string) {
	hash := sha256.Sum256([]byte(id))
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash[:]); err != nil {
		slog.Default().Debug("session delete failed", "error", err)
	}
}

// Sweep deletes sessions past their issued expiry or the current lifetime.
func (s *Sessions) Sweep(ctx context.Context) {
	where, args := s.purgeExpired()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE `+where, args...); err != nil {
		slog.Default().Debug("session sweep failed", "error", err)
	}
}

// storedAuthTime and timeOrZero keep "no auth time" as 0 in the database and the
// zero time.Time in Go, which auth.Fresh treats as never fresh.
func storedAuthTime(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}

func encodeGroups(groups []string) (string, error) {
	if len(groups) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		return "", fmt.Errorf("encode session groups: %w", err)
	}
	return string(encoded), nil
}
