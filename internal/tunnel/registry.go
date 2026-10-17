package tunnel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Fingerprint is the SHA-256 of a certificate's DER encoding.
type Fingerprint [sha256.Size]byte

func FingerprintOf(der []byte) Fingerprint { return sha256.Sum256(der) }

// Authorizer decides whether a verified certificate belongs to an enrolled,
// non-revoked connector, and under which name.
type Authorizer interface {
	Lookup(ctx context.Context, fp Fingerprint) (name string, ok bool, err error)
}

var (
	ErrInvalidName   = errors.New("connector name must be 1-63 characters of a-z, 0-9 or '-'")
	ErrAlreadyActive = errors.New("an active connector already uses that name or certificate")
	ErrNotFound      = errors.New("no active connector with that name")
)

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Registry stores enrolled connectors in the SQLite state database
// (migration 002). It implements Authorizer.
type Registry struct {
	db  *sql.DB
	now func() time.Time
}

func NewRegistry(db *sql.DB) *Registry { return &Registry{db: db, now: time.Now} }

// Record is one enrolment row.
type Record struct {
	Name        string
	Fingerprint Fingerprint
	CreatedAt   time.Time
	RevokedAt   time.Time // zero when active
}

func (r *Registry) Enroll(ctx context.Context, name string, fp Fingerprint) error {
	if !namePattern.MatchString(name) {
		return ErrInvalidName
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO connectors (name, cert_sha256, created_at) VALUES (?, ?, ?)`,
		name, fp[:], r.now().Unix())
	if err != nil {
		// The two partial unique indexes are the only constraints a valid
		// insert can violate.
		if isConstraint(err) {
			return ErrAlreadyActive
		}
		return fmt.Errorf("enroll connector: %w", err)
	}
	return nil
}

// Revoke marks the active connector called name as revoked. A running edge
// closes its session on the next revalidation tick.
func (r *Registry) Revoke(ctx context.Context, name string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE connectors SET revoked_at = ? WHERE name = ? AND revoked_at IS NULL`,
		r.now().Unix(), name)
	if err != nil {
		return fmt.Errorf("revoke connector: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Registry) Lookup(ctx context.Context, fp Fingerprint) (string, bool, error) {
	var name string
	err := r.db.QueryRowContext(ctx,
		`SELECT name FROM connectors WHERE cert_sha256 = ? AND revoked_at IS NULL`, fp[:]).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("look up connector: %w", err)
	}
	return name, true, nil
}

func (r *Registry) List(ctx context.Context) ([]Record, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, cert_sha256, created_at, revoked_at FROM connectors ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var (
			rec     Record
			raw     []byte
			created int64
			revoked sql.NullInt64
		)
		if err := rows.Scan(&rec.Name, &raw, &created, &revoked); err != nil {
			return nil, err
		}
		copy(rec.Fingerprint[:], raw)
		rec.CreatedAt = time.Unix(created, 0)
		if revoked.Valid {
			rec.RevokedAt = time.Unix(revoked.Int64, 0)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
