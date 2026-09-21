package auth

import (
	"sync"
	"time"
)

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

func (p *pendingStore) put(state, codeVerifier, returnTo string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictExpiredLocked()
	p.byKey[state] = pendingLogin{
		codeVerifier: codeVerifier,
		returnTo:     returnTo,
		expiresAt:    time.Now().Add(pendingLoginTTL),
	}
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
