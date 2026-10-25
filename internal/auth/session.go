// Package auth gates routes behind an external OIDC provider's login and
// manages the resulting session. It never sees or stores a password —
// only the identity an external provider vouches for.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"sync"
	"time"
)

// ErrStoreFull is returned by Create when the session store is at
// capacity. A caller sees this as "login failed, try again" — better than
// an unbounded map or table growing forever under a sustained burst of
// logins.
var ErrStoreFull = errors.New("session store is full")

const (
	// MaxSessions caps how many sessions can exist at once, independent of
	// the periodic sweep — a burst of logins faster than the sweep interval
	// shouldn't be able to grow the store without bound. Every backend
	// enforces the same number.
	MaxSessions = 10_000
	// SessionCleanupInterval is how often an owner sweeps expired sessions.
	SessionCleanupInterval = 10 * time.Minute
)

type Session struct {
	Principal Principal
	ExpiresAt time.Time
}

// Lifetime bounds a session two ways: Idle ends a session nobody has used
// for that long (an abandoned cookie), Absolute ends it that long after the
// login however busy it is (a stolen cookie in active use). Idle zero means
// no idle limit.
type Lifetime struct {
	Idle     time.Duration
	Absolute time.Duration
}

// TouchInterval is how stale a session's last-seen time may get before a
// lookup records the new one. Recording every request would turn each one
// into a database write; the cost is that the idle limit can run up to this
// much early.
const TouchInterval = time.Minute

// expiry is the moment a session stops being valid: the earlier of its idle
// deadline and its absolute deadline. It is pure so both backends apply the
// same rule and the table test pins it.
func expiry(created, lastSeen time.Time, l Lifetime) time.Time {
	deadline := created.Add(l.Absolute)
	if l.Idle > 0 {
		if idle := lastSeen.Add(l.Idle); idle.Before(deadline) {
			deadline = idle
		}
	}
	return deadline
}

// Expiry exports the rule for the persistent backend.
func (l Lifetime) Expiry(created, lastSeen time.Time) time.Time {
	return expiry(created, lastSeen, l)
}

// SessionBackend is the session storage the Gate and the route manager
// consume: the in-memory store for a single process without a state
// database, and the SQLite adapter when sessions must survive a restart.
// The cookie carries an opaque ID; only the backend decides how to protect
// it at rest. Implementations must be safe for concurrent use.
type SessionBackend interface {
	// Create stores a new session for principal and returns the opaque,
	// unguessable ID that goes in the session cookie. It returns
	// ErrStoreFull at capacity.
	Create(ctx context.Context, principal Principal) (string, error)
	// Get returns the session for id, or ok=false when it does not exist or
	// has expired. An expired session is evicted on lookup.
	Get(ctx context.Context, id string) (Session, bool)
	// Delete removes the session for id; deleting a missing session is fine.
	Delete(ctx context.Context, id string)
	// Sweep removes expired sessions in bulk.
	Sweep(ctx context.Context)
}

// NewSessionID returns the opaque session identifier: 256 bits from
// crypto/rand. Knowing the ID is equivalent to being logged in as that
// principal, so this randomness is the whole security boundary.
func NewSessionID() (string, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(idBytes), nil
}

// SweepLoop sweeps expired sessions on backend until ctx is cancelled. Get
// already evicts an expired session it happens to look up, but a session
// nobody ever presents again (an abandoned login) would otherwise sit in
// storage until the next restart — this bounds that.
func SweepLoop(ctx context.Context, backend SessionBackend, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			backend.Sweep(ctx)
		}
	}
}

// SessionStore is the in-memory, single-instance SessionBackend. Sessions
// don't survive a restart and aren't shared across multiple Switchboard
// processes. It stays the default so a deployment without a state database
// keeps working; pass a persistent backend to make sessions outlive a
// restart.
type SessionStore struct {
	lifetime Lifetime
	now      func() time.Time

	mu       sync.Mutex
	sessions map[string]storedSession
}

type storedSession struct {
	principal Principal
	created   time.Time
	lastSeen  time.Time
}

var _ SessionBackend = (*SessionStore)(nil)

func NewSessionStore(lifetime Lifetime) *SessionStore {
	return &SessionStore{lifetime: lifetime, now: time.Now, sessions: make(map[string]storedSession)}
}

// Create stores a new session for principal and returns its opaque ID.
func (s *SessionStore) Create(ctx context.Context, principal Principal) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, err := NewSessionID()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) >= MaxSessions {
		return "", ErrStoreFull
	}
	now := s.now()
	s.sessions[id] = storedSession{principal: clonePrincipal(principal), created: now, lastSeen: now}

	return id, nil
}

// Sweep removes expired sessions.
func (s *SessionStore) Sweep(ctx context.Context) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if !now.Before(expiry(sess.created, sess.lastSeen, s.lifetime)) {
			delete(s.sessions, id)
		}
	}
}

// Get returns the session for id, or ok=false if it doesn't exist or has
// expired. An expired session is evicted on lookup.
func (s *SessionStore) Get(ctx context.Context, id string) (Session, bool) {
	if ctx.Err() != nil {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	now := s.now()
	if !now.Before(expiry(sess.created, sess.lastSeen, s.lifetime)) {
		delete(s.sessions, id)
		return Session{}, false
	}
	if now.Sub(sess.lastSeen) >= TouchInterval {
		sess.lastSeen = now
		s.sessions[id] = sess
	}
	return Session{Principal: clonePrincipal(sess.principal), ExpiresAt: expiry(sess.created, sess.lastSeen, s.lifetime)}, true
}

func clonePrincipal(principal Principal) Principal {
	principal.Groups = slices.Clone(principal.Groups)
	if principal.UserVerified != nil {
		verified := *principal.UserVerified
		principal.UserVerified = &verified
	}
	if principal.DeviceFirstSeen != nil {
		firstSeen := *principal.DeviceFirstSeen
		principal.DeviceFirstSeen = &firstSeen
	}
	return principal
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(ctx context.Context, id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}
