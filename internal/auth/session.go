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
// an unbounded map growing forever under a sustained burst of logins.
var ErrStoreFull = errors.New("session store is full")

// maxSessions caps how many sessions can exist at once, independent of
// the periodic cleanup sweep — a burst of logins faster than the sweep
// interval shouldn't be able to grow the store without bound.
const maxSessions = 10_000

type Session struct {
	Principal Principal
	ExpiresAt time.Time
}

// SessionStore is an in-memory, single-instance session store. Sessions
// don't survive a restart and aren't shared across multiple Switchboard
// processes — acceptable for a single-box deployment, revisit if
// Switchboard ever runs replicated.
type SessionStore struct {
	ttl time.Duration

	mu       sync.Mutex
	sessions map[string]Session
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{ttl: ttl, sessions: make(map[string]Session)}
}

// Create stores a new session for principal and returns its opaque,
// unguessable ID — this ID is what goes in the session cookie. 256 bits of
// randomness from crypto/rand is the whole security boundary here: knowing
// the ID is equivalent to being logged in as that principal.
func (s *SessionStore) Create(principal Principal) (string, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) >= maxSessions {
		return "", ErrStoreFull
	}
	s.sessions[id] = Session{Principal: principal, ExpiresAt: time.Now().Add(s.ttl)}

	return id, nil
}

// StartCleanup periodically sweeps expired sessions until ctx is
// canceled. Get already evicts an expired session it happens to look up,
// but a session nobody ever presents again (an abandoned login) would
// otherwise sit in memory until process restart — this bounds that.
func (s *SessionStore) StartCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweep()
			}
		}
	}()
}

func (s *SessionStore) sweep() {
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
func (s *SessionStore) Get(id string) (Session, bool) {
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

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}
