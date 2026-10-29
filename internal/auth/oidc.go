package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

const (
	// SessionCookieName is Ozal's session cookie. The proxy strips it
	// from every request so an upstream never receives the edge session.
	SessionCookieName = "sb_session"
	// HandoffCookiePrefix reserves a browser-binding cookie per transaction.
	// The proxy removes the whole namespace before forwarding to an app.
	HandoffCookiePrefix = "sb_handoff_"
	// LoginCookieName carries a signed, short-lived login attempt (state,
	// PKCE verifier, return_to). The server keeps no pending-login store:
	// the cookie is the state, signed and bound to the browser.
	LoginCookieName    = "sb_login"
	defaultSessionTTL  = 7 * 24 * time.Hour
	defaultSessionIdle = 12 * time.Hour
	loginCookieTTL     = 5 * time.Minute
)

// SessionLifetime is the configured session lifetime: session_ttl_minutes
// is the absolute limit, session_idle_minutes the idle one.
func SessionLifetime(cfg config.Auth) Lifetime {
	l := Lifetime{Idle: defaultSessionIdle, Absolute: defaultSessionTTL}
	if cfg.SessionTTLMinutes > 0 {
		l.Absolute = time.Duration(cfg.SessionTTLMinutes) * time.Minute
	}
	if cfg.SessionIdleMinutes > 0 {
		l.Idle = time.Duration(cfg.SessionIdleMinutes) * time.Minute
	}
	return l
}

// SessionCookie is the session cookie both login gates set. MaxAge makes it
// outlive the browser session; the server still ends it earlier when the
// idle limit passes or the session is revoked.
func SessionCookie(id, domain string, secure bool, l Lifetime) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Domain:   domain,
		Path:     "/",
		MaxAge:   int(l.Absolute / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Gate protects routes behind a login against an external OIDC provider.
// Ozal never handles a password: it redirects to the provider,
// verifies the ID token the provider returns, and trusts that identity
// for the session's lifetime.
type Gate struct {
	issuer       string
	oauthConfig  oauth2.Config
	verifier     *oidc.IDTokenVerifier
	cookieDomain string
	allowedHosts map[string]bool

	sessions SessionBackend
	lifetime Lifetime
	loginKey []byte
	sink     events.Sink
}

// emit publishes a login event when a sink is configured.
func (g *Gate) emit(e events.Event) {
	if g.sink == nil {
		return
	}
	e.Time = time.Now()
	g.sink.Emit(e)
}

// NewGate performs OIDC discovery against cfg.Issuer. It fails fast if the
// provider's discovery document can't be fetched — a misconfigured issuer
// should be caught at startup, not on the first login attempt.
//
// allowedHosts is the set of hosts (host:port, as they appear in the
// routing config) a post-login redirect is allowed to land on. Without
// this check, an attacker could craft a login link with return_to pointing
// anywhere and use this Gate as an open redirect once a victim logs in.
func NewGate(ctx context.Context, cfg config.Auth, allowedHosts []string, sessions SessionBackend, sink events.Sink) (*Gate, error) {
	if sessions == nil {
		return nil, errors.New("auth: NewGate requires a session store")
	}
	if sink == nil {
		sink = events.Discard
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery against %q: %w", cfg.Issuer, err)
	}

	hosts := make(map[string]bool, len(allowedHosts))
	for _, h := range allowedHosts {
		host, err := config.NormalizeHost(h)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed host %q: %w", h, err)
		}
		hosts[host] = true
	}

	return &Gate{
		issuer: cfg.Issuer,
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
		lifetime:     SessionLifetime(cfg),
		loginKey:     loginCookieKey(cfg.ClientSecret),
		sink:         sink,
	}, nil
}

// Authenticate returns the trusted identity stored in a valid session.
// The bool is false when the cookie is absent, unknown, or expired.
func (g *Gate) Authenticate(r *http.Request) (Principal, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Principal{}, false
	}
	session, ok := g.sessions.Get(r.Context(), cookie.Value)
	// This gate issues no host sessions: one copied here from a passkey
	// installation's app is not valid on these hosts.
	if !ok || session.Audience != "" || !g.AcceptsPrincipal(session.Principal) {
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
	g.emit(events.Event{Type: events.LoginStarted, Host: r.Host, RemoteAddr: r.RemoteAddr})

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

	value, err := g.sealLogin(loginPayload{
		State:        state,
		CodeVerifier: verifier,
		ReturnTo:     returnTo,
		ExpiresAt:    time.Now().Add(loginCookieTTL).Unix(),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     LoginCookieName,
		Value:    value,
		Domain:   g.cookieDomain,
		Path:     "/",
		MaxAge:   int(loginCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	challenge := s256Challenge(verifier)
	authURL := g.oauthConfig.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (g *Gate) HandleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The login attempt is valid exactly once: clear the cookie before
	// anything else, whatever the outcome. Replaying a captured callback URL
	// without the matching browser cookie fails below; a replayed code dies
	// at the provider, where authorization codes are single-use.
	g.clearLoginCookie(w, r)

	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(LoginCookieName)
	if err != nil {
		g.emit(events.Event{Type: events.LoginFailed, Host: r.Host, Code: "missing_login_cookie", RemoteAddr: r.RemoteAddr})
		http.Error(w, "login expired or invalid, please try again", http.StatusBadRequest)
		return
	}
	pl, ok := g.openLogin(cookie.Value)
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(pl.State)) != 1 {
		g.emit(events.Event{Type: events.LoginFailed, Host: r.Host, Code: "invalid_login_state", RemoteAddr: r.RemoteAddr})
		http.Error(w, "login expired or invalid, please try again", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := g.oauthConfig.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", pl.CodeVerifier))
	if err != nil {
		g.emit(events.Event{Type: events.LoginFailed, Host: r.Host, Code: "token_exchange_failed", Error: err.Error(), RemoteAddr: r.RemoteAddr})
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		g.emit(events.Event{Type: events.LoginFailed, Host: r.Host, Code: "id_token_missing", RemoteAddr: r.RemoteAddr})
		http.Error(w, "provider did not return an id_token", http.StatusBadGateway)
		return
	}
	idToken, err := g.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		g.emit(events.Event{Type: events.LoginFailed, Host: r.Host, Code: "id_token_invalid", Error: err.Error(), RemoteAddr: r.RemoteAddr})
		http.Error(w, "id_token verification failed", http.StatusUnauthorized)
		return
	}

	var claims struct {
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		Groups        []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		g.emit(events.Event{Type: events.LoginFailed, Host: r.Host, Code: "claims_invalid", Error: err.Error(), RemoteAddr: r.RemoteAddr})
		http.Error(w, "failed to decode id_token claims", http.StatusBadGateway)
		return
	}

	principal := Principal{
		Subject:       OIDCSubject(idToken.Issuer, idToken.Subject),
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Groups:        claims.Groups,
		AuthTime:      time.Now(),
		Method:        MethodOIDC,
	}

	sessionID, err := g.sessions.Create(ctx, principal)
	if errors.Is(err, ErrStoreFull) {
		g.emit(events.Event{Type: events.LoginFailed, Actor: principal.Subject, Host: r.Host, Code: "session_store_full", RemoteAddr: r.RemoteAddr})
		http.Error(w, "too many active sessions, try again shortly", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		g.emit(events.Event{Type: events.LoginFailed, Actor: principal.Subject, Host: r.Host, Code: "session_create_failed", Error: err.Error(), RemoteAddr: r.RemoteAddr})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	g.emit(events.Event{Type: events.LoginSucceeded, Actor: principal.Subject, Host: r.Host, RemoteAddr: r.RemoteAddr})

	http.SetCookie(w, SessionCookie(sessionID, g.cookieDomain, r.TLS != nil, g.lifetime))
	http.Redirect(w, r, g.safeReturnTo(pl.ReturnTo), http.StatusFound)
}

// safeReturnTo only honors returnTo if it points at a host this Gate was
// configured to protect — otherwise it's an open-redirect vector, since
// returnTo comes from a query parameter an attacker fully controls.
func (g *Gate) safeReturnTo(returnTo string) string {
	return SafeReturnTo(g.allowedHosts, returnTo)
}

// SafeReturnTo returns returnTo when it is an http(s) URL without userinfo
// on one of the allowed (normalized) hosts, and "/" otherwise. Every login
// flow uses it: a return address comes from a query parameter an attacker
// controls.
func SafeReturnTo(allowedHosts map[string]bool, returnTo string) string {
	u, err := url.Parse(returnTo)
	if err != nil || u.User != nil {
		return "/"
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "/"
	}
	host, err := config.NormalizeHost(u.Host)
	if err != nil || !allowedHosts[host] {
		return "/"
	}
	return returnTo
}

func (g *Gate) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		g.sessions.Delete(r.Context(), cookie.Value)
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// loginPayload is what a login attempt carries across the provider round
// trip, signed into LoginCookieName instead of kept server-side.
type loginPayload struct {
	State        string `json:"s"`
	CodeVerifier string `json:"v"`
	ReturnTo     string `json:"r"`
	ExpiresAt    int64  `json:"e"`
}

// loginCookieKey derives the cookie signing key from the OIDC client secret
// with a domain separator, so one secret is never used directly as the HMAC
// key of a second protocol. Rotating the client secret invalidates only
// in-flight logins (minutes), never established sessions.
func loginCookieKey(clientSecret string) []byte {
	sum := sha256.Sum256([]byte("switchboard/login-cookie/v1\x00" + clientSecret))
	return sum[:]
}

// sealLogin returns base64url(payload) + "." + base64url(HMAC-SHA256).
func (g *Gate) sealLogin(p loginPayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, g.loginKey)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// openLogin verifies the signature and expiry and returns the payload.
// Any failure is a plain false: callers must not tell an attacker which
// check failed.
func (g *Gate) openLogin(value string) (loginPayload, bool) {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok {
		return loginPayload{}, false
	}
	want, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return loginPayload{}, false
	}
	mac := hmac.New(sha256.New, g.loginKey)
	mac.Write([]byte(payload))
	if !hmac.Equal(mac.Sum(nil), want) {
		return loginPayload{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return loginPayload{}, false
	}
	var p loginPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return loginPayload{}, false
	}
	if p.State == "" || p.CodeVerifier == "" || time.Now().Unix() > p.ExpiresAt {
		return loginPayload{}, false
	}
	return p, true
}

func (g *Gate) clearLoginCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     LoginCookieName,
		Value:    "",
		Domain:   g.cookieDomain,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
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

// ServeAuthEndpoint handles the OIDC login endpoints under /_/auth/ and
// reports false for any other path, which then belongs to the route.
func (g *Gate) ServeAuthEndpoint(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/_/auth/login":
		g.HandleLogin(w, r)
	case "/_/auth/callback":
		g.HandleCallback(w, r)
	case "/_/auth/logout":
		// Logout is a state change: a GET would let any page log the
		// user out with an image tag.
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return true
		}
		g.HandleLogout(w, r)
	default:
		return false
	}
	return true
}

// AcceptsPrincipal binds a session to this gate's current issuer and method.
func (g *Gate) AcceptsPrincipal(p Principal) bool {
	return p.Method == MethodOIDC && IsOIDCSubject(g.issuer, p.Subject)
}
