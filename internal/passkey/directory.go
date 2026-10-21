// Package passkey lets Switchboard authenticate visitors itself with WebAuthn
// passkeys (ID-003), without an external identity provider. The operator
// invites users from the host; a user enrols a passkey through a one-time
// link and then logs in with it. Only public keys and hashes are stored.
// The design and its threat model are in docs/architecture/identity-login.md.
package passkey

import (
	"context"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// SubjectPrefix namespaces passkey users in auth.Principal.Subject, so a
// policy can never confuse them with an upstream provider's subjects.
const SubjectPrefix = "sb:"

var (
	// ErrNotFound means no enabled user or active credential matched.
	ErrNotFound = errors.New("passkey: not found")
	// ErrInvitationInvalid covers an unknown, used or expired invitation;
	// callers must not tell the visitor which.
	ErrInvitationInvalid = errors.New("passkey: invitation invalid, used or expired")
	// ErrLastPasskey refuses revoking a user's only active passkey from
	// their own session: without another one, only the host could recover.
	ErrLastPasskey = errors.New("passkey: the last active passkey cannot be revoked from a session")
)

// User is someone the operator invited.
type User struct {
	ID string
	// Handle is the WebAuthn user handle: random, never displayed, what a
	// discoverable login resolves to.
	Handle     []byte
	Email      string
	Name       string
	Groups     []string
	CreatedAt  time.Time
	DisabledAt time.Time
}

// Subject is the principal subject this user's sessions carry.
func (u User) Subject() string { return SubjectPrefix + u.ID }

// Passkey is one stored credential and its bookkeeping.
type Passkey struct {
	UserID     string
	Credential webauthn.Credential
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

// Synced reports whether the authenticator may copy this passkey to other
// devices (the WebAuthn backup-eligible flag, which never changes for a
// credential). A device-bound passkey stays on one authenticator.
func (p Passkey) Synced() bool { return p.Credential.Flags.BackupEligible }

// Directory is what the login flows need from storage. The SQLite adapter
// implements it; operator commands (invite, revoke, disable) live on the
// adapter itself, outside the request path.
type Directory interface {
	// InvitedUser returns the user a valid invitation belongs to, without
	// using the invitation.
	InvitedUser(ctx context.Context, tokenHash []byte) (User, error)
	// Enrol uses the invitation and stores the credential in one
	// transaction, so a second enrolment with the same link fails.
	Enrol(ctx context.Context, tokenHash []byte, userID string, credential webauthn.Credential) error
	// UserByHandle returns an enabled user and their active credentials.
	UserByHandle(ctx context.Context, handle []byte) (User, []webauthn.Credential, error)
	// ActiveUser returns an enabled user by ID.
	ActiveUser(ctx context.Context, id string) (User, error)
	// RecordLogin stores a credential's new sign counter and flags.
	RecordLogin(ctx context.Context, credential webauthn.Credential) error

	// Self-service (ID-008), always from a session with a recent passkey
	// proof; the gate checks that before calling.

	// ActivePasskeys lists an enabled user's active passkeys, oldest first.
	ActivePasskeys(ctx context.Context, userID string) ([]Passkey, error)
	// AddPasskey stores another passkey for an enabled user.
	AddPasskey(ctx context.Context, userID string, credential webauthn.Credential) error
	// RevokeOwnPasskey revokes one of the user's active passkeys and ends
	// all their sessions in one transaction. It returns ErrNotFound for a
	// passkey that is not theirs or not active, and ErrLastPasskey for
	// their only active one.
	RevokeOwnPasskey(ctx context.Context, userID string, credentialID []byte) error
}
