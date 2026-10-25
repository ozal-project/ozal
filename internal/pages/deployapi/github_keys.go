package deployapi

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	// jwksMinRefresh bounds how often a token can make the edge fetch GitHub's
	// keys. go-oidc's remote key set refetches on every unknown kid, so a
	// stream of forged tokens turned into a stream of outbound requests.
	jwksMinRefresh = time.Minute
	// jwksMaxAge forces a periodic refresh so keys GitHub has retired stop
	// being trusted even when every token names a known kid.
	jwksMaxAge = time.Hour
	// jwksMaxBytes bounds the key document; GitHub's is a few kilobytes.
	jwksMaxBytes = 1 << 20
)

var errUnknownSigningKey = errors.New("token signing key is not in GitHub's key set")

// githubKeys implements oidc.KeySet over GitHub's JWKS with a refresh
// cooldown. A failed refresh keeps the previous keys: a short GitHub outage
// must not stop deployments signed with keys already verified as GitHub's.
type githubKeys struct {
	client *http.Client
	url    string
	now    func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

func (k *githubKeys) VerifySignature(ctx context.Context, token string) ([]byte, error) {
	signed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(signed.Signatures) != 1 || signed.Signatures[0].Header.KeyID == "" {
		return nil, ErrInvalidGitHubToken
	}
	key := k.key(ctx, signed.Signatures[0].Header.KeyID)
	if key == nil {
		return nil, errUnknownSigningKey
	}
	return signed.Verify(key)
}

// key returns the public key for kid, refreshing the set first when kid is
// unknown or the set is old, but never more than once per jwksMinRefresh.
func (k *githubKeys) key(ctx context.Context, kid string) *rsa.PublicKey {
	k.mu.Lock()
	now := k.now()
	key, known := k.keys[kid]
	refresh := (!known || now.Sub(k.fetchedAt) > jwksMaxAge) && now.Sub(k.lastAttempt) >= jwksMinRefresh
	if refresh {
		// Claimed under the lock, fetched outside it: concurrent callers use
		// the current set instead of queuing behind the network.
		k.lastAttempt = now
	}
	k.mu.Unlock()
	if !refresh {
		return key
	}

	fetched, err := k.fetch(ctx)
	k.mu.Lock()
	defer k.mu.Unlock()
	if err == nil {
		k.keys, k.fetchedAt = fetched, now
	}
	return k.keys[kid]
}

func (k *githubKeys) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub JWKS status %d", resp.StatusCode)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, jwksMaxBytes)).Decode(&set); err != nil {
		return nil, errors.New("GitHub JWKS is not a valid key set")
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, candidate := range set.Keys {
		public, ok := candidate.Key.(*rsa.PublicKey)
		if !ok || candidate.KeyID == "" || (candidate.Use != "" && candidate.Use != "sig") ||
			(candidate.Algorithm != "" && candidate.Algorithm != string(jose.RS256)) {
			continue
		}
		keys[candidate.KeyID] = public
	}
	if len(keys) == 0 {
		return nil, errors.New("GitHub JWKS has no usable RS256 signing keys")
	}
	return keys, nil
}
