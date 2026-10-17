package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/octaviosantacruz/switchboard/internal/events"
)

// newTestGate builds a Gate without real OIDC discovery, for testing the
// session-checking and login-cookie parts that don't need a live provider.
func newTestGate() *Gate {
	return &Gate{
		oauthConfig: oauth2.Config{
			ClientID:    "test-client",
			RedirectURL: "http://app.localhost/_/auth/callback",
			Endpoint:    oauth2.Endpoint{AuthURL: "https://idp.example.com/authorize"},
		},
		sessions:     NewSessionStore(time.Hour),
		loginKey:     loginCookieKey("test-secret"),
		allowedHosts: map[string]bool{"app.example.com": true},
	}
}

func TestMiddlewareAllowsValidSession(t *testing.T) {
	g := newTestGate()
	want := Principal{Email: "user@example.com", Subject: "sub"}
	id, _ := g.sessions.Create(want)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := g.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: id})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected the protected handler to be called with a valid session")
	}
	got, ok := g.Authenticate(req)
	if !ok || got.Email != want.Email || got.Subject != want.Subject {
		t.Fatalf("Authenticate = (%+v, %v), want (%+v, true)", got, ok, want)
	}
}

func TestMiddlewareRedirectsWithoutSession(t *testing.T) {
	g := newTestGate()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called without a session")
	})
	handler := g.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	want := "/_/auth/login?return_to=" + url.QueryEscape("http://example.com/protected")
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

func TestMiddlewareRedirectsWithInvalidSessionCookie(t *testing.T) {
	g := newTestGate()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called with a bogus session")
	})
	handler := g.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "not-a-real-session-id"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
}

func TestHandleLogoutClearsSession(t *testing.T) {
	g := newTestGate()
	id, _ := g.sessions.Create(Principal{Email: "user@example.com", Subject: "sub"})

	req := httptest.NewRequest(http.MethodGet, "/_/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: id})
	rec := httptest.NewRecorder()

	g.HandleLogout(rec, req)

	if _, ok := g.sessions.Get(id); ok {
		t.Fatal("expected session to be deleted after logout")
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (POST logout redirects to GET)", rec.Code)
	}
}

func TestS256ChallengeIsDeterministic(t *testing.T) {
	a := s256Challenge("same-verifier")
	b := s256Challenge("same-verifier")
	if a != b {
		t.Fatal("expected the same verifier to produce the same challenge")
	}
	if s256Challenge("different") == a {
		t.Fatal("expected different verifiers to produce different challenges")
	}
}

func TestSafeReturnToAllowsKnownHost(t *testing.T) {
	g := newTestGate()
	got := g.safeReturnTo("https://app.example.com/dashboard")
	if got != "https://app.example.com/dashboard" {
		t.Errorf("safeReturnTo = %q, want the original URL for an allowed host", got)
	}
}

func TestSafeReturnToRejectsUnknownHost(t *testing.T) {
	g := newTestGate()
	got := g.safeReturnTo("https://evil.example.com/phish")
	if got != "/" {
		t.Errorf("safeReturnTo = %q, want %q for a disallowed host (open-redirect guard)", got, "/")
	}
}

func TestSafeReturnToRejectsGarbage(t *testing.T) {
	g := newTestGate()
	if got := g.safeReturnTo("not a url at all \x00"); got != "/" {
		t.Errorf("safeReturnTo = %q, want %q for an unparseable value", got, "/")
	}
}

func TestRandomStringIsNotEmptyAndVaries(t *testing.T) {
	a, err := randomString(32)
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	b, _ := randomString(32)
	if a == "" || a == b {
		t.Fatalf("expected non-empty, distinct random strings, got %q and %q", a, b)
	}
}

func TestHandleLoginSetsSignedLoginCookie(t *testing.T) {
	g := newTestGate()
	req := httptest.NewRequest(http.MethodGet, "/_/auth/login?return_to="+url.QueryEscape("http://app.localhost/dashboard"), nil)
	rec := httptest.NewRecorder()

	g.HandleLogin(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	authURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" {
		t.Fatal("expected a non-empty state on the auth URL")
	}

	var loginCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == LoginCookieName {
			loginCookie = c
		}
	}
	if loginCookie == nil {
		t.Fatal("expected the login attempt to be carried in a signed cookie")
	}
	if !loginCookie.HttpOnly || loginCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login cookie flags = HttpOnly:%v SameSite:%v, want HttpOnly and Lax", loginCookie.HttpOnly, loginCookie.SameSite)
	}
	if loginCookie.MaxAge != int(loginCookieTTL.Seconds()) {
		t.Fatalf("login cookie MaxAge = %d, want %d", loginCookie.MaxAge, int(loginCookieTTL.Seconds()))
	}

	pl, ok := g.openLogin(loginCookie.Value)
	if !ok {
		t.Fatal("the freshly issued login cookie does not verify")
	}
	if pl.State != state || pl.CodeVerifier == "" {
		t.Fatalf("payload = %+v, want state %q and a verifier", pl, state)
	}
	if pl.ReturnTo != "http://app.localhost/dashboard" {
		t.Fatalf("payload return_to = %q, want the original URL", pl.ReturnTo)
	}
}

func TestOpenLoginRejectsTamperingAndExpiry(t *testing.T) {
	g := newTestGate()
	valid, err := g.sealLogin(loginPayload{
		State:        "state-a",
		CodeVerifier: "verifier-a",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sealLogin: %v", err)
	}
	if _, ok := g.openLogin(valid); !ok {
		t.Fatal("expected the untouched cookie to verify")
	}

	flipped := []byte(valid)
	if flipped[0] == 'A' {
		flipped[0] = 'B'
	} else {
		flipped[0] = 'A'
	}
	expired, _ := g.sealLogin(loginPayload{State: "s", CodeVerifier: "v", ReturnTo: "/", ExpiresAt: time.Now().Add(-time.Second).Unix()})
	emptyState, _ := g.sealLogin(loginPayload{CodeVerifier: "v", ReturnTo: "/", ExpiresAt: time.Now().Add(time.Minute).Unix()})
	emptyVerifier, _ := g.sealLogin(loginPayload{State: "s", ReturnTo: "/", ExpiresAt: time.Now().Add(time.Minute).Unix()})

	rejected := map[string]string{
		"tampered payload":   string(flipped),
		"tampered signature": valid + "x",
		"missing separator":  "no-dot-here",
		"garbage":            "!!!.???",
		"expired":            expired,
		"empty state":        emptyState,
		"empty verifier":     emptyVerifier,
		"signed by someone else": func() string {
			other := &Gate{loginKey: loginCookieKey("another-secret")}
			v, _ := other.sealLogin(loginPayload{State: "s", CodeVerifier: "v", ReturnTo: "/", ExpiresAt: time.Now().Add(time.Minute).Unix()})
			return v
		}(),
	}
	for name, value := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, ok := g.openLogin(value); ok {
				t.Fatalf("openLogin accepted %q", name)
			}
		})
	}
}

func TestCallbackWithoutLoginCookieFails(t *testing.T) {
	g := newTestGate()
	req := httptest.NewRequest(http.MethodGet, "/_/auth/callback?code=stolen-code&state=attacker-state", nil)
	rec := httptest.NewRecorder()

	g.HandleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (replayed callback without the browser cookie)", rec.Code)
	}
}

func TestCallbackRejectsStateMismatchAndClearsCookie(t *testing.T) {
	g := newTestGate()
	value, err := g.sealLogin(loginPayload{
		State:        "state-a",
		CodeVerifier: "verifier-a",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sealLogin: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_/auth/callback?code=x&state=state-b", nil)
	req.AddCookie(&http.Cookie{Name: LoginCookieName, Value: value})
	rec := httptest.NewRecorder()

	g.HandleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a state that does not match the cookie", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == LoginCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected the login cookie to be cleared even on failure")
	}
}

func TestLoginEmitsEvents(t *testing.T) {
	g := newTestGate()
	recorder := &events.Recorder{}
	g.sink = recorder

	g.HandleLogin(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_/auth/login", nil))
	if !recorder.Has(events.LoginStarted) {
		t.Fatal("missing login.started event")
	}

	g.HandleCallback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_/auth/callback?code=x&state=y", nil))
	failed, ok := recorder.Find(events.LoginFailed)
	if !ok || failed.Code != "missing_login_cookie" {
		t.Fatalf("login.failed event = %+v, %v", failed, ok)
	}
}

func TestSafeReturnToNormalizesKnownHost(t *testing.T) {
	g := newTestGate()
	returnTo := "https://APP.EXAMPLE.COM./dashboard"
	if got := g.safeReturnTo(returnTo); got != returnTo {
		t.Fatalf("safeReturnTo = %q, want %q", got, returnTo)
	}
}

func TestSafeReturnToRejectsUnsafeSchemesAndUserinfo(t *testing.T) {
	g := newTestGate()
	for _, returnTo := range []string{
		"javascript://app.example.com/alert",
		"ftp://app.example.com/file",
		"//app.example.com/scheme-relative",
		"https://attacker@app.example.com/dashboard",
	} {
		t.Run(returnTo, func(t *testing.T) {
			if got := g.safeReturnTo(returnTo); got != "/" {
				t.Fatalf("safeReturnTo(%q) = %q, want /", returnTo, got)
			}
		})
	}
}
