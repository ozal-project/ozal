package passkey

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	// ceremonyTTL bounds how long a begun registration or login can be
	// finished. Browsers time the ceremony out in about the same window.
	ceremonyTTL = 5 * time.Minute
	// maxCeremonies caps pending ceremonies so a flood of begin requests
	// cannot grow memory without bound; the /_/auth/ rate limit comes first.
	maxCeremonies = 10_000
)

var errCeremoniesFull = errors.New("passkey: too many pending ceremonies")

type ceremonyKind int

const (
	kindLogin ceremonyKind = iota + 1
	kindEnrol
)

// ceremony is the server side of one begun registration or login. The
// browser holds only its random ID, in a cookie it cannot read.
type ceremony struct {
	kind      ceremonyKind
	session   webauthn.SessionData
	userID    string // enrolment: the invited user
	tokenHash []byte // enrolment: the invitation being used
	returnTo  string // where to go afterwards (already validated); may be empty
	expires   time.Time
}

// ceremonies keeps pending ceremonies in memory. A restart forgets them,
// which costs a visitor one retry; it never weakens a check.
type ceremonies struct {
	now func() time.Time

	mu      sync.Mutex
	pending map[string]ceremony
}

func newCeremonies(now func() time.Time) *ceremonies {
	return &ceremonies{now: now, pending: make(map[string]ceremony)}
}

// put stores c under a new random ID and returns the ID.
func (c *ceremonies) put(entry ceremony) (string, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	now := c.now()
	entry.expires = now.Add(ceremonyTTL)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= maxCeremonies {
		for key, pending := range c.pending {
			if !now.Before(pending.expires) {
				delete(c.pending, key)
			}
		}
		if len(c.pending) >= maxCeremonies {
			return "", errCeremoniesFull
		}
	}
	c.pending[id] = entry
	return id, nil
}

// take removes and returns the ceremony: each one can be finished once,
// whether the attempt succeeds or not, so a captured response cannot be
// replayed against the same challenge.
func (c *ceremonies) take(id string, kind ceremonyKind) (ceremony, bool) {
	c.mu.Lock()
	entry, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if !ok || entry.kind != kind || !c.now().Before(entry.expires) {
		return ceremony{}, false
	}
	return entry, true
}
