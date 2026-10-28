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

// Sessions is the persistent auth.SessionBackend over migrations 001,
// 005–009 and 015 (host sessions linked to a central one). The
// cookie carries the raw ID; only its SHA-256 is stored, so a leaked
// database file cannot be replayed as session cookies. Times are stored as
// Unix seconds, the same shape the migration defines.
type Sessions struct {
	db          *sql.DB
	lifetime    auth.Lifetime
	maxSessions int
	now         func() time.Time
}

var (
	_ auth.SessionBackend = (*Sessions)(nil)
	_ auth.LinkedSessions = (*Sessions)(nil)
)

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
	var userVerified any
	if principal.UserVerified != nil {
		userVerified = *principal.UserVerified
	}
	var deviceFirstSeen any
	if principal.Method == auth.MethodPasskey && principal.DeviceFirstSeen != nil {
		deviceFirstSeen = *principal.DeviceFirstSeen
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
		`INSERT INTO sessions (id_hash, subject, email, email_verified, groups_json, created_at, expires_at, last_seen_at, auth_time, amr, device_bound, user_verified, device_first_seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hash[:], principal.Subject, principal.Email, principal.EmailVerified, groups,
		now.Unix(), now.Add(s.lifetime.Absolute).Unix(), now.Unix(), storedAuthTime(principal.AuthTime), principal.Method, principal.DeviceBound, userVerified, deviceFirstSeen,
	); err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit session: %w", err)
	}
	return id, nil
}

// orphans deletes host sessions whose central session is gone.
const orphans = `DELETE FROM sessions WHERE parent_hash IS NOT NULL AND parent_hash NOT IN (SELECT id_hash FROM sessions)`

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// purge deletes expired rows, then the host sessions they approved.
func (s *Sessions) purge(ctx context.Context, db execer) error {
	where, args := s.purgeExpired()
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE `+where, args...); err != nil {
		return fmt.Errorf("purge expired sessions: %w", err)
	}
	if _, err := db.ExecContext(ctx, orphans); err != nil {
		return fmt.Errorf("purge orphaned host sessions: %w", err)
	}
	return nil
}

// CreateLinked stores a host session for audience under the central session
// parentID, copying its principal in the same transaction, so a login that
// ends concurrently cannot leave a host session behind it.
func (s *Sessions) CreateLinked(ctx context.Context, parentID, audience string) (string, auth.Principal, error) {
	if audience == "" {
		return "", auth.Principal{}, auth.ErrSessionNotFound
	}
	id, err := auth.NewSessionID()
	if err != nil {
		return "", auth.Principal{}, err
	}
	hash, parentHash := sha256.Sum256([]byte(id)), sha256.Sum256([]byte(parentID))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", auth.Principal{}, fmt.Errorf("begin session transaction: %w", err)
	}
	defer tx.Rollback()
	if err := s.purge(ctx, tx); err != nil {
		return "", auth.Principal{}, err
	}
	parent, found, err := s.lookup(ctx, tx, parentHash[:])
	if err != nil {
		return "", auth.Principal{}, err
	}
	if !found || !s.live(parent) || parent.audience != "" || len(parent.parent) > 0 {
		return "", auth.Principal{}, auth.ErrSessionNotFound
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		return "", auth.Principal{}, fmt.Errorf("count sessions: %w", err)
	}
	if count >= s.maxSessions {
		return "", auth.Principal{}, auth.ErrStoreFull
	}
	now := s.now()
	expires := s.expiresAt(parent.created, parent.lastSeen, parent.issued)
	if limit := now.Add(s.lifetime.Absolute); limit.Before(expires) {
		expires = limit
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, subject, email, email_verified, groups_json, created_at, expires_at, last_seen_at, auth_time, amr, device_bound, user_verified, device_first_seen, audience, parent_hash)
		 SELECT ?, subject, email, email_verified, groups_json, ?, ?, ?, auth_time, amr, device_bound, user_verified, device_first_seen, ?, id_hash FROM sessions WHERE id_hash = ?`,
		hash[:], now.Unix(), expires.Unix(), now.Unix(), audience, parentHash[:],
	); err != nil {
		return "", auth.Principal{}, fmt.Errorf("insert host session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", auth.Principal{}, fmt.Errorf("commit host session: %w", err)
	}
	return id, parent.principal, nil
}

// DeleteLogin deletes id's central session and every host session under it.
func (s *Sessions) DeleteLogin(ctx context.Context, id string) {
	hash := sha256.Sum256([]byte(id))
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE id_hash = ?1
		    OR id_hash = (SELECT parent_hash FROM sessions WHERE id_hash = ?1)
		    OR parent_hash = ?1
		    OR parent_hash = (SELECT parent_hash FROM sessions WHERE id_hash = ?1)`, hash[:]); err != nil {
		slog.Default().Debug("login delete failed", "error", err)
	}
}

// row is one sessions row as stored.
type row struct {
	principal auth.Principal
	created   time.Time
	lastSeen  time.Time
	issued    time.Time
	audience  string
	parent    []byte
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// lookup reads a row; found is false when there is none.
func (s *Sessions) lookup(ctx context.Context, db querier, hash []byte) (row, bool, error) {
	var (
		subject         string
		email           string
		verified        int64
		groupsJSON      string
		expiresUnix     int64
		createdUnix     int64
		seenUnix        int64
		authUnix        int64
		method          string
		deviceBound     int64
		userVerified    sql.NullInt64
		deviceFirstSeen sql.NullInt64
		audience        string
		parent          []byte
	)
	err := db.QueryRowContext(ctx,
		`SELECT subject, email, email_verified, groups_json, expires_at, created_at, last_seen_at, auth_time, amr, device_bound, user_verified, device_first_seen, audience, parent_hash FROM sessions WHERE id_hash = ?`,
		hash,
	).Scan(&subject, &email, &verified, &groupsJSON, &expiresUnix, &createdUnix, &seenUnix, &authUnix, &method, &deviceBound, &userVerified, &deviceFirstSeen, &audience, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, false, nil
	}
	if err != nil {
		return row{}, false, err
	}
	var groups []string
	if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
		slog.Default().Debug("session groups are not readable", "error", err)
		groups = nil
	}
	var verifiedByAuthenticator *bool
	if userVerified.Valid {
		value := userVerified.Int64 == 1
		verifiedByAuthenticator = &value
	}
	var firstSeenCredential *bool
	if method == auth.MethodPasskey && deviceFirstSeen.Valid {
		value := deviceFirstSeen.Int64 == 1
		firstSeenCredential = &value
	}
	r := row{
		principal: auth.Principal{
			Subject:         subject,
			Email:           email,
			EmailVerified:   verified == 1,
			Groups:          groups,
			AuthTime:        timeOrZero(authUnix),
			Method:          method,
			DeviceBound:     deviceBound == 1,
			UserVerified:    verifiedByAuthenticator,
			DeviceFirstSeen: firstSeenCredential,
		},
		created:  time.Unix(createdUnix, 0),
		lastSeen: time.Unix(seenUnix, 0),
		issued:   time.Unix(expiresUnix, 0),
		audience: audience,
		parent:   parent,
	}
	return r, true, nil
}

// live reports whether r is unexpired under the current lifetime. It does
// not check a host session's parent. Applying the current snapshot policy
// changes no row while a reload candidate is being built, and a longer
// lifetime never extends the issued expiry.
func (s *Sessions) live(r row) bool {
	return s.now().Before(s.expiresAt(r.created, r.lastSeen, r.issued))
}

// Get returns the session for id, or ok=false when it is absent or expired.
// An expired row is evicted on lookup. A database error reads as "not
// authenticated": failing closed is the only safe direction here, and the
// error is logged for the operator.
func (s *Sessions) Get(ctx context.Context, id string) (auth.Session, bool) {
	hash := sha256.Sum256([]byte(id))
	r, found, err := s.lookup(ctx, s.db, hash[:])
	if err != nil {
		slog.Default().Debug("session lookup failed", "error", err)
		return auth.Session{}, false
	}
	if !found {
		return auth.Session{}, false
	}
	live := s.live(r)
	// A host session is only as good as the central login that approved it.
	var parent row
	if live && len(r.parent) > 0 {
		if parent, found, err = s.lookup(ctx, s.db, r.parent); err != nil {
			slog.Default().Debug("session lookup failed", "error", err)
			return auth.Session{}, false
		}
		live = found && s.live(parent)
	}
	if !live {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash[:])
		return auth.Session{}, false
	}
	// Record the use at most once per TouchInterval. A failed write only
	// means the idle limit counts from the previous use: earlier, never later.
	now := s.now()
	lastSeen := r.lastSeen
	if now.Sub(lastSeen) >= auth.TouchInterval {
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?`, now.Unix(), hash[:]); err != nil {
			slog.Default().Debug("session touch failed", "error", err)
		} else {
			lastSeen = now
		}
	}
	// Using an app counts as using the login that approved it.
	if len(r.parent) > 0 && now.Sub(parent.lastSeen) >= auth.TouchInterval {
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?`, now.Unix(), r.parent); err != nil {
			slog.Default().Debug("session touch failed", "error", err)
		}
	}
	return auth.Session{Principal: r.principal, ExpiresAt: s.expiresAt(r.created, lastSeen, r.issued), Audience: r.audience}, true
}

// expiresAt is the earlier of the expiry issued at login and the current
// lifetime's rule.
func (s *Sessions) expiresAt(created, lastSeen, issued time.Time) time.Time {
	if policy := s.lifetime.Expiry(created, lastSeen); policy.Before(issued) {
		return policy
	}
	return issued
}

// Delete removes a session by ID, with the host sessions it approved;
// deleting a missing session is fine.
func (s *Sessions) Delete(ctx context.Context, id string) {
	hash := sha256.Sum256([]byte(id))
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?1 OR parent_hash = ?1`, hash[:]); err != nil {
		slog.Default().Debug("session delete failed", "error", err)
	}
}

// Sweep deletes sessions past their issued expiry or the current lifetime,
// then host sessions left without their central session.
func (s *Sessions) Sweep(ctx context.Context) {
	if err := s.purge(ctx, s.db); err != nil {
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
