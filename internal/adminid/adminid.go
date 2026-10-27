// Package adminid holds the types of administrative identity (UI-007): the
// people who run the console, kept apart from application users. It is
// shared by the admin API and the SQLite adapter and imports neither.
package adminid

import (
	"errors"
	"time"
)

var (
	// ErrEnrolmentInvalid covers an unknown, used or expired enrolment; the
	// caller never says which.
	ErrEnrolmentInvalid = errors.New("admin: enrolment invalid, used or expired")
	// ErrAlreadyEnrolled refuses a bootstrap once an administrator exists.
	ErrAlreadyEnrolled = errors.New("admin: an administrator already exists; use recover")
	// ErrAccountNotFound means no enabled administrator matched.
	ErrAccountNotFound = errors.New("admin: administrator not found")
	// ErrEmailTaken refuses inviting an email that already is an administrator.
	ErrEmailTaken = errors.New("admin: that email is already an administrator")
	// ErrLastAdministrator refuses disabling the only enabled administrator
	// who can manage administrators: nobody could invite or recover anyone.
	ErrLastAdministrator = errors.New("admin: this is the last enabled administrator who can manage administrators")
	// ErrPasskeyNotFound means the account has no active passkey with that ID.
	ErrPasskeyNotFound = errors.New("admin: passkey not found")
	// ErrLastPasskey refuses removing an account's only active passkey: only
	// the host could let that person in again.
	ErrLastPasskey = errors.New("admin: this is the account's only passkey")
)

// Passkey is one active credential of an administrator, as they see it.
type Passkey struct {
	ID         []byte
	Label      string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// Account is one administrator.
type Account struct {
	ID         string
	Handle     []byte
	Email      string
	Name       string
	Tools      []string
	DisabledAt time.Time
}

// Enrolment is what a valid, unused enrolment token allows.
type Enrolment struct {
	Kind string
	// Account is the administrator being recovered; zero for a bootstrap.
	Account Account
}

// EnrolmentRequest is what the host or an administrator asks to issue.
type EnrolmentRequest struct {
	// Kind is bootstrap (first administrator), recover (replace a person's
	// passkeys) or invite (a new administrator with fixed email and tools).
	Kind  string
	Email string
	Name  string
	Tools []string
}

// Summary is an administrator as the console lists them.
type Summary struct {
	Account
	Passkeys   int
	CreatedAt  time.Time
	LastUsedAt time.Time
}
