// Package auth gates routes behind an external OIDC provider's login and
// manages the resulting session. It never sees or stores a password —
// only the identity an external provider vouches for.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
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
	ttl time.Duration

	mu       sync.Mutex
	sessions map[string]Session
}

var _ SessionBackend = (*SessionStore)(nil)

func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{ttl: ttl, sessions: make(map[string]Session)}
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
	s.sessions[id] = Session{Principal: principal, ExpiresAt: time.Now().Add(s.ttl)}

	return id, nil
}

// Sweep removes expired sessions.
func (s *SessionStore) Sweep(ctx context.Context) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
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
	if time.Now().After(sess.ExpiresAt) {
		delete(s.sessions, id)
		return Session{}, false
	}
	return sess, true
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(ctx context.Context, id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}
