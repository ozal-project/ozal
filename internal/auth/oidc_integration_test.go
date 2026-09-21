package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// fakeOIDCProvider is a minimal OIDC provider good enough to exercise
// Switchboard's real login -> code exchange -> callback path end-to-end:
// discovery document, JWKS, and a token endpoint that signs a real ID
// token. It does not implement or check PKCE/state itself — that's
// Switchboard's job, and is asserted on the Switchboard side.
type fakeOIDCProvider struct {
	server        *httptest.Server
	key           *rsa.PrivateKey
	keyID         string
	email         string
	emailVerified bool
	groups        []string
	sub           string
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p := &fakeOIDCProvider{
		key:           key,
		keyID:         "test-key",
		email:         "user@example.com",
		emailVerified: true,
		groups:        []string{"engineering"},
		sub:           "user-123",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/jwks", p.handleJWKS)
	mux.HandleFunc("/token", p.handleToken)
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeOIDCProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                p.server.URL,
		"authorization_endpoint":                p.server.URL + "/authorize",
		"token_endpoint":                        p.server.URL + "/token",
		"jwks_uri":                              p.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *fakeOIDCProvider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       p.key.Public(),
		KeyID:     p.keyID,
		Algorithm: "RS256",
		Use:       "sig",
	}}}
	json.NewEncoder(w).Encode(jwks)
}

func (p *fakeOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	// oauth2.Config sends client credentials via HTTP Basic Auth when both
	// ClientID and ClientSecret are set, not as form fields.
	clientID, _, ok := r.BasicAuth()
	if !ok {
		clientID = r.FormValue("client_id")
	}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", p.keyID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	claims := jwt.Claims{
		Issuer:   p.server.URL,
		Subject:  p.sub,
		Audience: jwt.Audience{clientID},
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt: jwt.NewNumericDate(time.Now()),
	}
	extra := map[string]any{
		"email":          p.email,
		"email_verified": p.emailVerified,
		"groups":         p.groups,
	}
	rawIDToken, err := jwt.Signed(signer).Claims(claims).Claims(extra).Serialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"id_token":     rawIDToken,
	})
}

func TestFullLoginCallbackFlow(t *testing.T) {
	provider := newFakeOIDCProvider(t)

	gate, err := NewGate(t.Context(), config.Auth{
		Issuer:       provider.server.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://app.localhost/_/auth/callback",
	}, []string{"app.localhost"})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	// Step 1: hit login, capture the state Switchboard generated.
	loginReq := httptest.NewRequest(http.MethodGet, "http://app.localhost/_/auth/login?return_to="+url.QueryEscape("http://app.localhost/dashboard"), nil)
	loginRec := httptest.NewRecorder()
	gate.HandleLogin(loginRec, loginReq)

	if loginRec.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302", loginRec.Code)
	}
	authURL, err := url.Parse(loginRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" {
		t.Fatal("expected a non-empty state in the auth URL")
	}
	if authURL.Query().Get("code_challenge") == "" || authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("expected PKCE code_challenge/code_challenge_method on the auth URL")
	}

	// Step 2: simulate the provider redirecting back with a code. Our fake
	// token endpoint doesn't validate the code's authenticity, only that a
	// real token request reaches it — which is what proves the exchange
	// wiring (client_id, code_verifier, endpoint) is correct.
	callbackReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://app.localhost/_/auth/callback?code=fake-code&state=%s", state), nil)
	callbackRec := httptest.NewRecorder()
	gate.HandleCallback(callbackRec, callbackReq)

	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302, body: %s", callbackRec.Code, callbackRec.Body.String())
	}
	if loc := callbackRec.Header().Get("Location"); loc != "http://app.localhost/dashboard" {
		t.Errorf("callback redirect = %q, want the original return_to", loc)
	}

	cookies := callbackRec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected a session cookie to be set after a successful callback")
	}

	// Step 3: the session the callback created should now authenticate
	// requests, with the identity taken from the fake provider's ID token.
	checkReq := httptest.NewRequest(http.MethodGet, "http://app.localhost/dashboard", nil)
	checkReq.AddCookie(sessionCookie)
	if !gate.IsAuthenticated(checkReq) {
		t.Fatal("expected the session set by the callback to authenticate a follow-up request")
	}

	sess, _ := gate.sessions.Get(sessionCookie.Value)
	p := sess.Principal
	if p.Email != provider.email || p.Subject != provider.sub {
		t.Errorf("principal = %+v, want email=%q subject=%q", p, provider.email, provider.sub)
	}
	if !p.EmailVerified {
		t.Error("expected the session's principal to carry email_verified=true from the id_token")
	}
	if len(p.Groups) != 1 || p.Groups[0] != "engineering" {
		t.Errorf("groups = %v, want [engineering]", p.Groups)
	}
}

func TestCallbackRejectsUnknownState(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	gate, err := NewGate(t.Context(), config.Auth{
		Issuer:       provider.server.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://app.localhost/_/auth/callback",
	}, []string{"app.localhost"})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/_/auth/callback?code=x&state=never-issued", nil)
	rec := httptest.NewRecorder()
	gate.HandleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown/replayed state", rec.Code)
	}
}
