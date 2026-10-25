package deployapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubVerifierChecksSignatureIssuerAudienceExpiryAndIDs(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: key.Public(), KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != GitHubJWKS {
			t.Fatalf("unexpected verification network request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(jwks))), Header: make(http.Header)}, nil
	})}
	verifier := NewGitHubVerifier(context.Background(), "deploy-test", client)
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		change func(*jwt.Claims, map[string]any)
		valid  bool
	}{
		{"valid", func(*jwt.Claims, map[string]any) {}, true},
		{"issuer", func(c *jwt.Claims, _ map[string]any) { c.Issuer = "https://evil.example" }, false},
		{"audience", func(c *jwt.Claims, _ map[string]any) { c.Audience = jwt.Audience{"other"} }, false},
		{"expired", func(c *jwt.Claims, _ map[string]any) { c.Expiry = jwt.NewNumericDate(now.Add(-time.Minute)) }, false},
		{"not yet valid", func(c *jwt.Claims, _ map[string]any) { c.NotBefore = jwt.NewNumericDate(now.Add(2 * time.Minute)) }, false},
		{"nonnumeric repository", func(_ *jwt.Claims, c map[string]any) { c["repository_id"] = "not-an-id" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := jwt.Claims{Issuer: GitHubIssuer, Subject: "repo:owner/repo:ref:refs/heads/main", Audience: jwt.Audience{"deploy-test"}, IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), Expiry: jwt.NewNumericDate(now.Add(4 * time.Minute)), ID: "unique-jti"}
			extra := map[string]any{"repository_id": "123", "repository_owner_id": "42", "ref": "refs/heads/main", "workflow_ref": testWorkflowRef, "event_name": "push", "sha": strings.Repeat("a", 40), "repository": "owner/repo", "run_id": "12345"}
			tc.change(&base, extra)
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
			if err != nil {
				t.Fatal(err)
			}
			token, err := jwt.Signed(signer).Claims(base).Claims(extra).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			claims, err := verifier.Verify(context.Background(), token)
			if (err == nil) != tc.valid {
				t.Fatalf("Verify error=%v,valid=%v", err, tc.valid)
			}
			if tc.valid && (claims.RepositoryID != 123 || claims.RepositoryOwnerID != 42 || claims.JWTID != "unique-jti" || claims.CommitSHA != strings.Repeat("a", 40) || claims.WorkflowRef != testWorkflowRef || claims.EventName != "push") {
				t.Fatalf("verified claims=%+v", claims)
			}
			if tc.valid {
				parts := strings.Split(token, ".")
				parts[1] = "e30" // retain the signature but replace the claims.
				if _, err := verifier.Verify(context.Background(), strings.Join(parts, ".")); err == nil {
					t.Fatal("accepted tampered signed claims")
				}
			}
		})
	}
}

func TestGitHubKeyRefreshIsRateLimitedAndFollowsRotation(t *testing.T) {
	newKey := func() *rsa.PrivateKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	original, rotated, forged := newKey(), newKey(), newKey()
	published := []jose.JSONWebKey{{Key: original.Public(), KeyID: "original", Algorithm: "RS256", Use: "sig"}}
	fetches := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		fetches++
		jwks, _ := json.Marshal(jose.JSONWebKeySet{Keys: published})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(jwks))), Header: make(http.Header)}, nil
	})}
	verifier := NewGitHubVerifier(context.Background(), "deploy-test", client)
	now := time.Now().UTC().Truncate(time.Second)
	verifier.now = func() time.Time { return now }
	sign := func(key *rsa.PrivateKey, kid string) string {
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", kid))
		if err != nil {
			t.Fatal(err)
		}
		token, err := jwt.Signed(signer).Claims(jwt.Claims{Issuer: GitHubIssuer, Audience: jwt.Audience{"deploy-test"}, IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), Expiry: jwt.NewNumericDate(now.Add(4 * time.Minute)), ID: kid}).Claims(map[string]any{
			"repository_id": "123", "repository_owner_id": "42", "ref": "refs/heads/main", "workflow_ref": testWorkflowRef, "event_name": "push", "sha": strings.Repeat("a", 40), "repository": "owner/repo", "run_id": "12345",
		}).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	verify := func(token string, valid bool, wantFetches int) {
		t.Helper()
		if _, err := verifier.Verify(context.Background(), token); (err == nil) != valid {
			t.Fatalf("Verify error=%v, want valid=%v", err, valid)
		}
		if fetches != wantFetches {
			t.Fatalf("JWKS fetches=%d, want %d", fetches, wantFetches)
		}
	}

	verify(sign(original, "original"), true, 1)
	// A burst of tokens with unknown kids reuses the fresh set: no fetch each.
	for i := range 20 {
		verify(sign(forged, fmt.Sprintf("forged-%d", i)), false, 1)
	}
	// After the cooldown GitHub's rotated key is picked up by one refresh.
	now = now.Add(2 * time.Minute)
	published = append(published, jose.JSONWebKey{Key: rotated.Public(), KeyID: "rotated", Algorithm: "RS256", Use: "sig"})
	verify(sign(rotated, "rotated"), true, 2)
	verify(sign(forged, "forged-again"), false, 2)
	// A key GitHub retired stops verifying once the set ages out.
	now = now.Add(2 * time.Hour)
	published = published[1:]
	verify(sign(original, "original"), false, 3)
	verify(sign(rotated, "rotated"), true, 3)
}
