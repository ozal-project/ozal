package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrTooManyPendingLogins is returned by put when the pending-login store
// is at capacity — a burst of /_/auth/login hits faster than logins
// complete shouldn't be able to grow this store without bound.
var ErrTooManyPendingLogins = errors.New("too many logins in progress")

const maxPendingLogins = 10_000

// pendingLogin holds what a login-in-progress needs to survive the round
// trip to the OIDC provider and back: the PKCE verifier (to prove this
// callback belongs to the login that started it) and where to send the
// user once they're authenticated.
type pendingLogin struct {
	codeVerifier string
	returnTo     string
	expiresAt    time.Time
}

const pendingLoginTTL = 5 * time.Minute

// pendingStore is a short-lived, in-memory store keyed by OAuth "state".
// Separate from SessionStore: different lifetime (minutes, not hours) and
// different purpose (a login attempt in flight, not an authenticated
// user) — collapsing them into one generic store would blur that.
type pendingStore struct {
	mu    sync.Mutex
	byKey map[string]pendingLogin
}

func newPendingStore() *pendingStore {
	return &pendingStore{byKey: make(map[string]pendingLogin)}
}

func (p *pendingStore) put(state, codeVerifier, returnTo string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictExpiredLocked()
	if len(p.byKey) >= maxPendingLogins {
		return ErrTooManyPendingLogins
	}
	p.byKey[state] = pendingLogin{
		codeVerifier: codeVerifier,
		returnTo:     returnTo,
		expiresAt:    time.Now().Add(pendingLoginTTL),
	}
	return nil
}

// startCleanup periodically sweeps expired pending logins until ctx is
// canceled, independent of evictExpiredLocked's lazy sweep-on-put — a lull
// in new login attempts shouldn't leave stale entries sitting around.
func (p *pendingStore) startCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.mu.Lock()
				p.evictExpiredLocked()
				p.mu.Unlock()
			}
		}
	}()
}

// take returns and removes the pending login for state — a state is only
// ever redeemed once, valid or not.
func (p *pendingStore) take(state string) (pendingLogin, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pl, ok := p.byKey[state]
	delete(p.byKey, state)
	if !ok || time.Now().After(pl.expiresAt) {
		return pendingLogin{}, false
	}
	return pl, true
}

func (p *pendingStore) evictExpiredLocked() {
	now := time.Now()
	for k, v := range p.byKey {
		if now.After(v.expiresAt) {
			delete(p.byKey, k)
		}
	}
}
