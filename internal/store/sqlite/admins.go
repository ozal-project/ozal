package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/octaviosantacruz/switchboard/internal/adminid"
)

// Admins stores administrator accounts and their passkeys (migration 013),
// apart from application users. It implements the admin package's
// identities port.
type Admins struct {
	db  *sql.DB
	now func() time.Time
}

// NewAdmins builds the directory over an already-migrated database.
func NewAdmins(db *sql.DB) *Admins { return &Admins{db: db, now: time.Now} }

// Enrolled reports whether any administrator exists.
func (s *Admins) Enrolled(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_accounts`).Scan(&n); err != nil {
		return false, fmt.Errorf("count administrators: %w", err)
	}
	return n > 0, nil
}

// IssueEnrolment stores a one-use token hash. Issuing a new token does not
// cancel older unused ones: they all expire.
func (s *Admins) IssueEnrolment(ctx context.Context, request adminid.EnrolmentRequest, tokenHash []byte, expires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID, email, name, tools any
	switch request.Kind {
	case "bootstrap":
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_accounts`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return adminid.ErrAlreadyEnrolled
		}
	case "recover":
		account, err := scanAdmin(tx.QueryRowContext(ctx, adminColumns+` WHERE email = ? AND disabled_at IS NULL`, strings.ToLower(strings.TrimSpace(request.Email))))
		if err != nil {
			return err
		}
		accountID = account.ID
	case "invite":
		email = strings.ToLower(strings.TrimSpace(request.Email))
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_accounts WHERE email = ?`, email).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return adminid.ErrEmailTaken
		}
		if request.Name != "" {
			name = request.Name
		}
		encoded, err := json.Marshal(request.Tools)
		if err != nil {
			return err
		}
		tools = string(encoded)
	default:
		return fmt.Errorf("unknown enrolment kind %q", request.Kind)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_enrolments (token_hash, kind, account_id, email, name, tools_json, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, request.Kind, accountID, email, name, tools, s.now().Unix(), expires.Unix()); err != nil {
		return fmt.Errorf("store enrolment: %w", err)
	}
	return tx.Commit()
}

// Enrolment returns what an unused, unexpired token allows, without using it.
func (s *Admins) Enrolment(ctx context.Context, tokenHash []byte) (adminid.Enrolment, error) {
	var kind string
	var accountID, email, name, toolsJSON sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, account_id, email, name, tools_json FROM admin_enrolments WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, s.now().Unix()).Scan(&kind, &accountID, &email, &name, &toolsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return adminid.Enrolment{}, adminid.ErrEnrolmentInvalid
	}
	if err != nil {
		return adminid.Enrolment{}, err
	}
	out := adminid.Enrolment{Kind: kind}
	if kind == "invite" {
		out.Account = adminid.Account{Email: email.String, Name: name.String}
		if err := json.Unmarshal([]byte(toolsJSON.String), &out.Account.Tools); err != nil {
			return adminid.Enrolment{}, adminid.ErrEnrolmentInvalid
		}
		return out, nil
	}
	if kind == "bootstrap" {
		if enrolled, err := s.Enrolled(ctx); err != nil || enrolled {
			return adminid.Enrolment{}, adminid.ErrEnrolmentInvalid
		}
		return out, nil
	}
	account, err := scanAdmin(s.db.QueryRowContext(ctx, adminColumns+` WHERE id = ? AND disabled_at IS NULL`, accountID.String))
	if err != nil {
		return adminid.Enrolment{}, adminid.ErrEnrolmentInvalid
	}
	out.Account = account
	return out, nil
}

// CompleteEnrolment uses the token and stores the passkey in one transaction.
// The conditional UPDATE makes the token single-use even when two browsers
// finish together.
func (s *Admins) CompleteEnrolment(ctx context.Context, tokenHash []byte, account adminid.Account, credential webauthn.Credential) (adminid.Account, error) {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return adminid.Account{}, fmt.Errorf("encode credential: %w", err)
	}
	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return adminid.Account{}, err
	}
	defer tx.Rollback()
	var kind string
	var accountID, inviteEmail, inviteTools sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT kind, account_id, email, tools_json FROM admin_enrolments WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, now).Scan(&kind, &accountID, &inviteEmail, &inviteTools); err != nil {
		return adminid.Account{}, adminid.ErrEnrolmentInvalid
	}
	if result, err := tx.ExecContext(ctx, `UPDATE admin_enrolments SET used_at = ? WHERE token_hash = ? AND used_at IS NULL`, now, tokenHash); err != nil {
		return adminid.Account{}, err
	} else if n, _ := result.RowsAffected(); n != 1 {
		return adminid.Account{}, adminid.ErrEnrolmentInvalid
	}
	switch kind {
	case "bootstrap":
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_accounts`).Scan(&n); err != nil {
			return adminid.Account{}, err
		}
		if n > 0 {
			return adminid.Account{}, adminid.ErrAlreadyEnrolled
		}
		tools, err := json.Marshal(account.Tools)
		if err != nil {
			return adminid.Account{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO admin_accounts (id, handle, email, name, tools_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			account.ID, account.Handle, account.Email, account.Name, string(tools), now); err != nil {
			return adminid.Account{}, fmt.Errorf("insert administrator: %w", err)
		}
	case "invite":
		// Identity and tools come from the stored invitation, never from
		// what the browser sent back.
		if err := json.Unmarshal([]byte(inviteTools.String), &account.Tools); err != nil {
			return adminid.Account{}, adminid.ErrEnrolmentInvalid
		}
		account.Email = inviteEmail.String
		encoded, err := json.Marshal(account.Tools)
		if err != nil {
			return adminid.Account{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO admin_accounts (id, handle, email, name, tools_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			account.ID, account.Handle, account.Email, account.Name, string(encoded), now); err != nil {
			return adminid.Account{}, adminid.ErrEmailTaken
		}
	case "recover":
		account, err = scanAdmin(tx.QueryRowContext(ctx, adminColumns+` WHERE id = ? AND disabled_at IS NULL`, accountID.String))
		if err != nil {
			return adminid.Account{}, adminid.ErrEnrolmentInvalid
		}
		// Recovery assumes the old passkeys are lost or exposed.
		if _, err := tx.ExecContext(ctx, `UPDATE admin_passkeys SET revoked_at = ? WHERE account_id = ? AND revoked_at IS NULL`, now, account.ID); err != nil {
			return adminid.Account{}, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_passkeys (credential_id, account_id, credential_json, created_at) VALUES (?, ?, ?, ?)`,
		credential.ID, account.ID, string(encoded), now); err != nil {
		return adminid.Account{}, fmt.Errorf("insert passkey: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return adminid.Account{}, err
	}
	return account, nil
}

// AccountByHandle returns an enabled administrator with active passkeys.
func (s *Admins) AccountByHandle(ctx context.Context, handle []byte) (adminid.Account, []webauthn.Credential, error) {
	account, err := scanAdmin(s.db.QueryRowContext(ctx, adminColumns+` WHERE handle = ? AND disabled_at IS NULL`, handle))
	if err != nil {
		return adminid.Account{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT credential_json FROM admin_passkeys WHERE account_id = ? AND revoked_at IS NULL`, account.ID)
	if err != nil {
		return adminid.Account{}, nil, err
	}
	defer rows.Close()
	var credentials []webauthn.Credential
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return adminid.Account{}, nil, err
		}
		var credential webauthn.Credential
		if err := json.Unmarshal([]byte(encoded), &credential); err != nil {
			return adminid.Account{}, nil, fmt.Errorf("decode passkey: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return adminid.Account{}, nil, err
	}
	if len(credentials) == 0 {
		return adminid.Account{}, nil, adminid.ErrAccountNotFound
	}
	return account, credentials, nil
}

// RecordLogin stores the new counter. A revoked credential is not updated, so
// a login racing a recovery cannot bring it back.
func (s *Admins) RecordLogin(ctx context.Context, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE admin_passkeys SET credential_json = ?, last_used_at = ? WHERE credential_id = ? AND revoked_at IS NULL`,
		string(encoded), s.now().Unix(), credential.ID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return adminid.ErrAccountNotFound
	}
	return nil
}

const adminColumns = `SELECT id, handle, email, name, tools_json, disabled_at FROM admin_accounts`

func scanAdmin(row interface{ Scan(...any) error }) (adminid.Account, error) {
	var (
		account  adminid.Account
		tools    string
		disabled sql.NullInt64
	)
	if err := row.Scan(&account.ID, &account.Handle, &account.Email, &account.Name, &tools, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return adminid.Account{}, adminid.ErrAccountNotFound
		}
		return adminid.Account{}, err
	}
	if err := json.Unmarshal([]byte(tools), &account.Tools); err != nil {
		return adminid.Account{}, fmt.Errorf("decode tools: %w", err)
	}
	account.DisabledAt = unixOrZero(disabled)
	return account, nil
}

// Accounts lists every administrator, by email.
func (s *Admins) Accounts(ctx context.Context) ([]adminid.Summary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.handle, a.email, a.name, a.tools_json, a.disabled_at, a.created_at,
		(SELECT COUNT(*) FROM admin_passkeys p WHERE p.account_id = a.id AND p.revoked_at IS NULL),
		(SELECT MAX(p.last_used_at) FROM admin_passkeys p WHERE p.account_id = a.id)
		FROM admin_accounts a ORDER BY a.email`)
	if err != nil {
		return nil, fmt.Errorf("list administrators: %w", err)
	}
	defer rows.Close()
	var out []adminid.Summary
	for rows.Next() {
		var (
			summary  adminid.Summary
			tools    string
			disabled sql.NullInt64
			created  int64
			lastUsed sql.NullInt64
		)
		if err := rows.Scan(&summary.ID, &summary.Handle, &summary.Email, &summary.Name, &tools, &disabled, &created, &summary.Passkeys, &lastUsed); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tools), &summary.Tools); err != nil {
			return nil, fmt.Errorf("decode tools: %w", err)
		}
		summary.DisabledAt = unixOrZero(disabled)
		summary.CreatedAt = time.Unix(created, 0)
		summary.LastUsedAt = unixOrZero(lastUsed)
		out = append(out, summary)
	}
	return out, rows.Err()
}

// SetDisabled disables or enables an administrator. Disabling is refused when
// it would leave no enabled administrator able to manage administrators.
func (s *Admins) SetDisabled(ctx context.Context, id string, disabled bool) (adminid.Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return adminid.Account{}, err
	}
	defer tx.Rollback()
	account, err := scanAdmin(tx.QueryRowContext(ctx, adminColumns+` WHERE id = ?`, id))
	if err != nil {
		return adminid.Account{}, err
	}
	if disabled && account.DisabledAt.IsZero() {
		rows, err := tx.QueryContext(ctx, `SELECT tools_json FROM admin_accounts WHERE disabled_at IS NULL AND id <> ?`, id)
		if err != nil {
			return adminid.Account{}, err
		}
		managers := 0
		for rows.Next() {
			var encoded string
			var tools []string
			if err := rows.Scan(&encoded); err != nil {
				rows.Close()
				return adminid.Account{}, err
			}
			if json.Unmarshal([]byte(encoded), &tools) == nil {
				for _, tool := range tools {
					if tool == "administrators" {
						managers++
					}
				}
			}
		}
		rows.Close()
		if managers == 0 {
			return adminid.Account{}, adminid.ErrLastAdministrator
		}
	}
	var value any
	if disabled {
		value = s.now().Unix()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE admin_accounts SET disabled_at = ? WHERE id = ?`, value, id); err != nil {
		return adminid.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return adminid.Account{}, err
	}
	return account, nil
}
