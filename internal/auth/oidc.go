package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

const (
	// SessionCookieName is Switchboard's session cookie. The proxy strips it
	// from every request so an upstream never receives the edge session.
	SessionCookieName      = "sb_session"
	defaultSessionTTL      = 8 * time.Hour
	sessionCleanupInterval = 10 * time.Minute
)

// Gate protects routes behind a login against an external OIDC provider.
// Switchboard never handles a password: it redirects to the provider,
// verifies the ID token the provider returns, and trusts that identity
// for the session's lifetime.
type Gate struct {
	oauthConfig  oauth2.Config
	verifier     *oidc.IDTokenVerifier
	cookieDomain string
	allowedHosts map[string]bool

	sessions *SessionStore
	pending  *pendingStore
}

// NewGate performs OIDC discovery against cfg.Issuer. It fails fast if the
// provider's discovery document can't be fetched — a misconfigured issuer
// should be caught at startup, not on the first login attempt.
//
// allowedHosts is the set of hosts (host:port, as they appear in the
// routing config) a post-login redirect is allowed to land on. Without
// this check, an attacker could craft a login link with return_to pointing
// anywhere and use this Gate as an open redirect once a victim logs in.
func NewGate(ctx context.Context, cfg config.Auth, allowedHosts []string) (*Gate, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery against %q: %w", cfg.Issuer, err)
	}

	ttl := defaultSessionTTL
	if cfg.SessionTTLMinutes > 0 {
		ttl = time.Duration(cfg.SessionTTLMinutes) * time.Minute
	}

	hosts := make(map[string]bool, len(allowedHosts))
	for _, h := range allowedHosts {
		host, err := config.NormalizeHost(h)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed host %q: %w", h, err)
		}
		hosts[host] = true
	}

	sessions := NewSessionStore(ttl)
	sessions.StartCleanup(ctx, sessionCleanupInterval)

	pending := newPendingStore()
	pending.startCleanup(ctx, sessionCleanupInterval)

	return &Gate{
		oauthConfig: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier:     provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		cookieDomain: cfg.CookieDomain,
		allowedHosts: hosts,
		sessions:     sessions,
		pending:      pending,
	}, nil
}

// Authenticate returns the trusted identity stored in a valid session.
// The bool is false when the cookie is absent, unknown, or expired.
func (g *Gate) Authenticate(r *http.Request) (Principal, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Principal{}, false
	}
	session, ok := g.sessions.Get(cookie.Value)
	if !ok {
		return Principal{}, false
	}
	return session.Principal, true
}

// RedirectToLogin sends the browser to the login flow, remembering the
// current absolute URL so the callback — which may land on a different
// host than this request when auth spans multiple hosts under
// cookie_domain — can send the browser back to the right place afterwards.
func (g *Gate) RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	returnTo := requestURL(r)
	http.Redirect(w, r, "/_/auth/login?return_to="+url.QueryEscape(returnTo), http.StatusFound)
}

func requestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}

// Middleware redirects to login when the request has no valid session,
// and otherwise lets it through unchanged. It does not itself decide
// which routes need auth — the caller only wraps the routes that do.
func (g *Gate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Authenticate(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		g.RedirectToLogin(w, r)
	})
}

func (g *Gate) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomString(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	verifier, err := randomString(48)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" {
		returnTo = "/"
	}
	if err := g.pending.put(state, verifier, returnTo); err != nil {
		http.Error(w, "too many logins in progress, try again shortly", http.StatusServiceUnavailable)
		return
	}

	challenge := s256Challenge(verifier)
	authURL := g.oauthConfig.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (g *Gate) HandleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	state := r.URL.Query().Get("state")
	pl, ok := g.pending.take(state)
	if !ok {
		http.Error(w, "login expired or invalid, please try again", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := g.oauthConfig.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", pl.codeVerifier))
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "provider did not return an id_token", http.StatusBadGateway)
		return
	}
	idToken, err := g.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "id_token verification failed", http.StatusUnauthorized)
		return
	}

	var claims struct {
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		Groups        []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "failed to decode id_token claims", http.StatusBadGateway)
		return
	}

	principal := Principal{
		Subject:       idToken.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Groups:        claims.Groups,
	}

	sessionID, err := g.sessions.Create(principal)
	if errors.Is(err, ErrStoreFull) {
		http.Error(w, "too many active sessions, try again shortly", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Domain:   g.cookieDomain,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, g.safeReturnTo(pl.returnTo), http.StatusFound)
}

// safeReturnTo only honors returnTo if it points at a host this Gate was
// configured to protect — otherwise it's an open-redirect vector, since
// returnTo comes from a query parameter an attacker fully controls.
func (g *Gate) safeReturnTo(returnTo string) string {
	u, err := url.Parse(returnTo)
	if err != nil || u.User != nil {
		return "/"
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "/"
	}
	host, err := config.NormalizeHost(u.Host)
	if err != nil || !g.allowedHosts[host] {
		return "/"
	}
	return returnTo
}

func (g *Gate) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		g.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Domain:   g.cookieDomain,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
