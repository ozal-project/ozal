package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/octaviosantacruz/switchboard/internal/passkey"
)

// Identity is the passkey directory over migration 004. It implements
// passkey.Directory for the login flows and adds the operator commands the
// CLI runs on the host (invite, revoke, disable). Revoking a passkey or
// disabling a user deletes that user's sessions in the same transaction, so
// the effect is immediate rather than "when the session expires".
type Identity struct {
	db  *sql.DB
	now func() time.Time
}

var _ passkey.Directory = (*Identity)(nil)

// NewIdentity builds the directory over an already-migrated database.
func NewIdentity(db *sql.DB) *Identity {
	return &Identity{db: db, now: time.Now}
}

// Invitation is what the operator hands to a user: the raw token goes in
// the enrolment link and is never stored.
type Invitation struct {
	User      passkey.User
	Token     string
	ExpiresAt time.Time
}

// Invite creates the user if the email is new and issues a one-time
// enrolment token. A user who already has an active passkey needs
// recover=true, so a mistyped command cannot quietly add a second way into
// someone's account.
func (s *Identity) Invite(ctx context.Context, email, name string, groups []string, recoverAccess bool, ttl time.Duration) (Invitation, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) < 3 || !strings.Contains(email, "@") {
		return Invitation{}, fmt.Errorf("invalid email %q", email)
	}
	groupsJSON, err := encodeGroups(groups)
	if err != nil {
		return Invitation{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Invitation{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(token))
	now := s.now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, fmt.Errorf("begin invite: %w", err)
	}
	defer tx.Rollback()

	user, err := userByEmail(ctx, tx, email)
	switch {
	case errors.Is(err, passkey.ErrNotFound):
		user = passkey.User{Email: email, Name: name, Groups: groups, CreatedAt: now}
		idBytes := make([]byte, 16)
		user.Handle = make([]byte, 32)
		if _, err := rand.Read(idBytes); err != nil {
			return Invitation{}, err
		}
		if _, err := rand.Read(user.Handle); err != nil {
			return Invitation{}, err
		}
		user.ID = hex.EncodeToString(idBytes)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, handle, email, name, groups_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			user.ID, user.Handle, user.Email, user.Name, groupsJSON, now.Unix(),
		); err != nil {
			return Invitation{}, fmt.Errorf("insert user: %w", err)
		}
	case err != nil:
		return Invitation{}, err
	default:
		if !user.DisabledAt.IsZero() {
			return Invitation{}, fmt.Errorf("user %s is disabled; enable it first", email)
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkeys WHERE user_id = ? AND revoked_at IS NULL`, user.ID).Scan(&active); err != nil {
			return Invitation{}, fmt.Errorf("count passkeys: %w", err)
		}
		if active > 0 && !recoverAccess {
			return Invitation{}, fmt.Errorf("user %s already has %d active passkey(s); use -recover to add another", email, active)
		}
	}
	purpose := "enrol"
	if recoverAccess {
		purpose = "recover"
	}
	expires := now.Add(ttl)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO invitations (token_hash, user_id, purpose, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		tokenHash[:], user.ID, purpose, now.Unix(), expires.Unix(),
	); err != nil {
		return Invitation{}, fmt.Errorf("insert invitation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Invitation{}, fmt.Errorf("commit invite: %w", err)
	}
	return Invitation{User: user, Token: token, ExpiresAt: expires}, nil
}

// InvitedUser implements passkey.Directory.
func (s *Identity) InvitedUser(ctx context.Context, tokenHash []byte) (passkey.User, error) {
	var userID string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM invitations WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, s.now().Unix(),
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return passkey.User{}, passkey.ErrInvitationInvalid
	}
	if err != nil {
		return passkey.User{}, fmt.Errorf("look up invitation: %w", err)
	}
	user, err := s.ActiveUser(ctx, userID)
	if errors.Is(err, passkey.ErrNotFound) {
		return passkey.User{}, passkey.ErrInvitationInvalid
	}
	return user, err
}

// Enrol implements passkey.Directory: the conditional UPDATE is what makes
// an invitation single-use even when two browsers finish at once.
func (s *Identity) Enrol(ctx context.Context, tokenHash []byte, userID string, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode credential: %w", err)
	}
	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enrol: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE invitations SET used_at = ? WHERE token_hash = ? AND user_id = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, userID, now,
	)
	if err != nil {
		return fmt.Errorf("use invitation: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return passkey.ErrInvitationInvalid
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO passkeys (credential_id, user_id, credential_json, backup_eligible, created_at, first_seen_pending) VALUES (?, ?, ?, ?, ?, 0)`,
		credential.ID, userID, string(encoded), credential.Flags.BackupEligible, now,
	); err != nil {
		return fmt.Errorf("insert passkey: %w", err)
	}
	return tx.Commit()
}

// UserByHandle implements passkey.Directory.
func (s *Identity) UserByHandle(ctx context.Context, handle []byte) (passkey.User, []webauthn.Credential, error) {
	user, err := scanUser(s.db.QueryRowContext(ctx, userColumns+` WHERE handle = ? AND disabled_at IS NULL`, handle))
	if err != nil {
		return passkey.User{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT credential_json FROM passkeys WHERE user_id = ? AND revoked_at IS NULL`, user.ID)
	if err != nil {
		return passkey.User{}, nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var credentials []webauthn.Credential
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return passkey.User{}, nil, err
		}
		var credential webauthn.Credential
		if err := json.Unmarshal([]byte(encoded), &credential); err != nil {
			return passkey.User{}, nil, fmt.Errorf("decode passkey: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return passkey.User{}, nil, err
	}
	if len(credentials) == 0 {
		return passkey.User{}, nil, passkey.ErrNotFound
	}
	return user, credentials, nil
}

// ActiveUser implements passkey.Directory.
func (s *Identity) ActiveUser(ctx context.Context, id string) (passkey.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, userColumns+` WHERE id = ? AND disabled_at IS NULL`, id))
}

// RecordLogin implements passkey.Directory. A revoked credential is not
// updated: a login racing a revocation must not bring it back. The conditional
// update consumes first-seen evidence exactly once across concurrent logins.
func (s *Identity) RecordLogin(ctx context.Context, credential webauthn.Credential) (bool, error) {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return false, fmt.Errorf("encode credential: %w", err)
	}
	updatedAt := s.now().Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE passkeys SET credential_json = ?, last_used_at = ?, first_seen_pending = 0 WHERE credential_id = ? AND revoked_at IS NULL AND first_seen_pending = 1`,
		string(encoded), updatedAt, credential.ID,
	)
	if err != nil {
		return false, fmt.Errorf("record login: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return false, fmt.Errorf("record first passkey login: %w", err)
	} else if n == 1 {
		return true, nil
	}
	result, err = s.db.ExecContext(ctx,
		`UPDATE passkeys SET credential_json = ?, last_used_at = ? WHERE credential_id = ? AND revoked_at IS NULL AND first_seen_pending = 0`,
		string(encoded), updatedAt, credential.ID,
	)
	if err != nil {
		return false, fmt.Errorf("record login: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return false, fmt.Errorf("record login result: %w", err)
	} else if n != 1 {
		return false, passkey.ErrNotFound
	}
	return false, nil
}

// Users lists every user, enabled or not, by email.
func (s *Identity) Users(ctx context.Context) ([]passkey.User, error) {
	rows, err := s.db.QueryContext(ctx, userColumns+` ORDER BY email`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []passkey.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// Passkeys lists a user's credentials, revoked ones included, oldest first.
func (s *Identity) Passkeys(ctx context.Context, email string) ([]passkey.Passkey, error) {
	user, err := userByEmail(ctx, s.db, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT credential_json, created_at, last_used_at, revoked_at FROM passkeys WHERE user_id = ? ORDER BY created_at, credential_id`,
		user.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var keys []passkey.Passkey
	for rows.Next() {
		var (
			encoded          string
			created          int64
			lastUsed, revoke sql.NullInt64
		)
		if err := rows.Scan(&encoded, &created, &lastUsed, &revoke); err != nil {
			return nil, err
		}
		key := passkey.Passkey{UserID: user.ID, CreatedAt: time.Unix(created, 0), LastUsedAt: unixOrZero(lastUsed), RevokedAt: unixOrZero(revoke)}
		if err := json.Unmarshal([]byte(encoded), &key.Credential); err != nil {
			return nil, fmt.Errorf("decode passkey: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// RevokePasskey revokes the user's active passkey whose credential ID
// (hex) starts with prefix, and ends the user's sessions. The prefix must
// match exactly one active passkey.
func (s *Identity) RevokePasskey(ctx context.Context, email, prefix string) error {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if len(prefix) < 4 {
		return fmt.Errorf("give at least 4 hex characters of the passkey ID")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revoke: %w", err)
	}
	defer tx.Rollback()
	user, err := userByEmail(ctx, tx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT credential_id FROM passkeys WHERE user_id = ? AND revoked_at IS NULL`, user.ID)
	if err != nil {
		return fmt.Errorf("list passkeys: %w", err)
	}
	var matches [][]byte
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(hex.EncodeToString(id), prefix) {
			matches = append(matches, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list passkeys: %w", err)
	}
	rows.Close()
	if len(matches) != 1 {
		return fmt.Errorf("%d active passkeys of %s match %q; need exactly one", len(matches), user.Email, prefix)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE passkeys SET revoked_at = ? WHERE credential_id = ?`, s.now().Unix(), matches[0]); err != nil {
		return fmt.Errorf("revoke passkey: %w", err)
	}
	if err := deleteSessions(ctx, tx, user); err != nil {
		return err
	}
	return tx.Commit()
}

// ActivePasskeys implements passkey.Directory.
func (s *Identity) ActivePasskeys(ctx context.Context, userID string) ([]passkey.Passkey, error) {
	if _, err := s.ActiveUser(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT credential_json, created_at, last_used_at FROM passkeys WHERE user_id = ? AND revoked_at IS NULL ORDER BY created_at, credential_id`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var keys []passkey.Passkey
	for rows.Next() {
		var (
			encoded  string
			created  int64
			lastUsed sql.NullInt64
		)
		if err := rows.Scan(&encoded, &created, &lastUsed); err != nil {
			return nil, err
		}
		key := passkey.Passkey{UserID: userID, CreatedAt: time.Unix(created, 0), LastUsedAt: unixOrZero(lastUsed)}
		if err := json.Unmarshal([]byte(encoded), &key.Credential); err != nil {
			return nil, fmt.Errorf("decode passkey: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// AddPasskey implements passkey.Directory. The INSERT ... SELECT stores the
// passkey only if the user is still enabled at that moment.
func (s *Identity) AddPasskey(ctx context.Context, userID string, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode credential: %w", err)
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO passkeys (credential_id, user_id, credential_json, backup_eligible, created_at, first_seen_pending)
		 SELECT ?, id, ?, ?, ?, 1 FROM users WHERE id = ? AND disabled_at IS NULL`,
		credential.ID, string(encoded), credential.Flags.BackupEligible, s.now().Unix(), userID,
	)
	if err != nil {
		return fmt.Errorf("insert passkey: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return passkey.ErrNotFound
	}
	return nil
}

// RevokeOwnPasskey implements passkey.Directory.
func (s *Identity) RevokeOwnPasskey(ctx context.Context, userID string, credentialID []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revoke: %w", err)
	}
	defer tx.Rollback()
	user, err := scanUser(tx.QueryRowContext(ctx, userColumns+` WHERE id = ? AND disabled_at IS NULL`, userID))
	if err != nil {
		return err
	}
	var active, mine int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(credential_id = ?), 0) FROM passkeys WHERE user_id = ? AND revoked_at IS NULL`,
		credentialID, userID,
	).Scan(&active, &mine); err != nil {
		return fmt.Errorf("count passkeys: %w", err)
	}
	if mine != 1 {
		return passkey.ErrNotFound
	}
	if active == 1 {
		return passkey.ErrLastPasskey
	}
	if _, err := tx.ExecContext(ctx, `UPDATE passkeys SET revoked_at = ? WHERE credential_id = ? AND user_id = ?`, s.now().Unix(), credentialID, userID); err != nil {
		return fmt.Errorf("revoke passkey: %w", err)
	}
	if err := deleteSessions(ctx, tx, user); err != nil {
		return err
	}
	return tx.Commit()
}

// SetDisabled disables (and ends the sessions of) or re-enables a user.
func (s *Identity) SetDisabled(ctx context.Context, email string, disabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin disable: %w", err)
	}
	defer tx.Rollback()
	user, err := userByEmail(ctx, tx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return err
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at = ? WHERE id = ? AND disabled_at IS NULL`, s.now().Unix(), user.ID); err != nil {
			return fmt.Errorf("disable user: %w", err)
		}
		if err := deleteSessions(ctx, tx, user); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at = NULL WHERE id = ?`, user.ID); err != nil {
		return fmt.Errorf("enable user: %w", err)
	}
	return tx.Commit()
}

func deleteSessions(ctx context.Context, tx *sql.Tx, user passkey.User) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE subject = ?`, user.Subject()); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	return nil
}

const userColumns = `SELECT id, handle, email, name, groups_json, created_at, disabled_at FROM users`

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userByEmail(ctx context.Context, q queryer, email string) (passkey.User, error) {
	return scanUser(q.QueryRowContext(ctx, userColumns+` WHERE email = ?`, email))
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (passkey.User, error) {
	var (
		user       passkey.User
		groupsJSON string
		created    int64
		disabled   sql.NullInt64
	)
	err := row.Scan(&user.ID, &user.Handle, &user.Email, &user.Name, &groupsJSON, &created, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return passkey.User{}, passkey.ErrNotFound
	}
	if err != nil {
		return passkey.User{}, fmt.Errorf("read user: %w", err)
	}
	if err := json.Unmarshal([]byte(groupsJSON), &user.Groups); err != nil {
		return passkey.User{}, fmt.Errorf("decode user groups: %w", err)
	}
	user.CreatedAt = time.Unix(created, 0)
	user.DisabledAt = unixOrZero(disabled)
	if len(user.Handle) != 32 || bytes.Equal(user.Handle, make([]byte, 32)) {
		return passkey.User{}, fmt.Errorf("user %s has an invalid handle", user.Email)
	}
	return user, nil
}

func unixOrZero(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.Unix(v.Int64, 0)
}
