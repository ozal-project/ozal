package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// newTestGate builds a Gate without real OIDC discovery, for testing the
// session-checking parts (Middleware, HandleLogout) that don't need a
// live provider.
func newTestGate() *Gate {
	return &Gate{
		sessions:     NewSessionStore(time.Hour),
		pending:      newPendingStore(),
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
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
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
