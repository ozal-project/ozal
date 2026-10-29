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
	// Audience is the one host a host session is valid on (ID-015). An
	// empty audience is a central session: valid on the login host and the
	// hosts auth.cookie_domain covers.
	Audience string
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
	// Delete removes the session for id, and the host sessions it approved
	// when it is a central one; deleting a missing session is fine.
	Delete(ctx context.Context, id string)
	// Sweep removes expired sessions in bulk, and host sessions whose
	// central session is gone.
	Sweep(ctx context.Context)
}

// LinkedSessions issues host sessions under a central one (ID-015): a
// private app on a domain the central cookie cannot reach gets its own
// host-only session once the central login vouches for the visitor. Both
// backends implement it.
type LinkedSessions interface {
	// CreateLinked stores a session valid only on audience, with the
	// principal copied from the central session parentID. It expires no
	// later than its parent, and a lookup refuses it once the parent is
	// gone or expired. It returns ErrSessionNotFound when parentID is not a
	// live central session.
	CreateLinked(ctx context.Context, parentID, audience string) (string, Principal, error)
	// DeleteLogin ends the whole login id belongs to: its central session
	// and every host session that central session approved.
	DeleteLogin(ctx context.Context, id string)
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
// don't survive a restart and aren't shared across multiple Ozal
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
	// Host sessions only: the host, the central session's ID and the
	// parent's expiry when the host session was issued.
	audience string
	parent   string
	deadline time.Time
}

var (
	_ SessionBackend = (*SessionStore)(nil)
	_ LinkedSessions = (*SessionStore)(nil)
)

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
		if !now.Before(s.expiresAt(sess)) {
			delete(s.sessions, id)
		}
	}
	for id, sess := range s.sessions {
		if _, ok := s.sessions[sess.parent]; sess.parent != "" && !ok {
			delete(s.sessions, id)
		}
	}
}

// expiresAt is expiry under the store's lifetime, capped for a host
// session by its parent's expiry at issue.
func (s *SessionStore) expiresAt(sess storedSession) time.Time {
	deadline := expiry(sess.created, sess.lastSeen, s.lifetime)
	if !sess.deadline.IsZero() && sess.deadline.Before(deadline) {
		return sess.deadline
	}
	return deadline
}

// live reports whether id is unexpired at now and, for a host session,
// whether its central session is too. The caller holds the lock.
func (s *SessionStore) live(id string, now time.Time) (storedSession, bool) {
	sess, ok := s.sessions[id]
	if !ok || !now.Before(s.expiresAt(sess)) {
		return storedSession{}, false
	}
	if sess.parent != "" {
		if _, ok := s.live(sess.parent, now); !ok {
			return storedSession{}, false
		}
	}
	return sess, true
}

// Get returns the session for id, or ok=false if it doesn't exist or has
// expired. An expired session is evicted on lookup.
func (s *SessionStore) Get(ctx context.Context, id string) (Session, bool) {
	if ctx.Err() != nil {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	sess, ok := s.live(id, now)
	if !ok {
		delete(s.sessions, id)
		return Session{}, false
	}
	if now.Sub(sess.lastSeen) >= TouchInterval {
		sess.lastSeen = now
		s.sessions[id] = sess
	}
	// Using an app counts as using the login that approved it.
	if parent, ok := s.sessions[sess.parent]; ok && now.Sub(parent.lastSeen) >= TouchInterval {
		parent.lastSeen = now
		s.sessions[sess.parent] = parent
	}
	return Session{Principal: clonePrincipal(sess.principal), ExpiresAt: s.expiresAt(sess), Audience: sess.audience}, true
}

// CreateLinked stores a host session under the central session parentID.
func (s *SessionStore) CreateLinked(ctx context.Context, parentID, audience string) (string, Principal, error) {
	if err := ctx.Err(); err != nil {
		return "", Principal{}, err
	}
	id, err := NewSessionID()
	if err != nil {
		return "", Principal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	parent, ok := s.live(parentID, now)
	if !ok || parent.audience != "" || audience == "" {
		return "", Principal{}, ErrSessionNotFound
	}
	if len(s.sessions) >= MaxSessions {
		return "", Principal{}, ErrStoreFull
	}
	s.sessions[id] = storedSession{principal: clonePrincipal(parent.principal), created: now, lastSeen: now,
		audience: audience, parent: parentID, deadline: s.expiresAt(parent)}
	return id, clonePrincipal(parent.principal), nil
}

// DeleteLogin removes id's central session and every host session under it.
func (s *SessionStore) DeleteLogin(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := id
	if sess, ok := s.sessions[id]; ok && sess.parent != "" {
		root = sess.parent
	}
	delete(s.sessions, id)
	s.deleteFamily(root)
}

// deleteFamily removes a central session and its host sessions. The
// caller holds the lock.
func (s *SessionStore) deleteFamily(root string) {
	delete(s.sessions, root)
	for id, sess := range s.sessions {
		if sess.parent == root {
			delete(s.sessions, id)
		}
	}
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

// Delete removes a session by ID, with the host sessions under it.
func (s *SessionStore) Delete(ctx context.Context, id string) {
	s.mu.Lock()
	s.deleteFamily(id)
	s.mu.Unlock()
}
