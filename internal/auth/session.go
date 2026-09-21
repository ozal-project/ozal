// Package auth gates routes behind an external OIDC provider's login and
// manages the resulting session. It never sees or stores a password —
// only the identity an external provider vouches for.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

type Session struct {
	Email     string
	Subject   string
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

// Create stores a new session and returns its opaque, unguessable ID —
// this ID is what goes in the session cookie. 256 bits of randomness from
// crypto/rand is the whole security boundary here: knowing the ID is
// equivalent to being logged in as that user.
func (s *SessionStore) Create(email, subject string) (string, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)

	s.mu.Lock()
	s.sessions[id] = Session{Email: email, Subject: subject, ExpiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()

	return id, nil
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
