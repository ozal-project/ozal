package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestRoutesByHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	handler, err := New([]config.Route{
		{Host: "known.localhost", Target: upstream.URL},
	}, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("routes a known host to its upstream", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://known.localhost/", nil)
		req.Host = "known.localhost"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != "hello from upstream" {
			t.Fatalf("body = %q, want %q", got, "hello from upstream")
		}
	})

	t.Run("returns 404 for an unknown host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://unknown.localhost/", nil)
		req.Host = "unknown.localhost"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("rejects an invalid target URL at construction time", func(t *testing.T) {
		_, err := New([]config.Route{{Host: "bad.localhost", Target: "://not-a-url"}}, nil, nil)
		if err == nil {
			t.Fatal("expected an error for an invalid target URL, got nil")
		}
	})
}

type fakeChecker struct{ healthy map[string]bool }

func (f fakeChecker) Healthy(host string) bool { return f.healthy[host] }

func TestUnhealthyUpstreamReturns503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reached"))
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "down.localhost", Target: upstream.URL}},
		fakeChecker{healthy: map[string]bool{"down.localhost": false}},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://down.localhost/", nil)
	req.Host = "down.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

type fakeGate struct {
	authenticated bool
	redirected    bool
	principal     auth.Principal
}

func (f *fakeGate) Authenticate(r *http.Request) (auth.Principal, bool) {
	return f.principal, f.authenticated
}
func (f *fakeGate) RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	f.redirected = true
	http.Redirect(w, r, "/_/auth/login", http.StatusFound)
}

func TestAuthRequiredRouteRedirectsWhenUnauthenticated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reached"))
	}))
	defer upstream.Close()

	gate := &fakeGate{authenticated: false}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !gate.redirected {
		t.Fatal("expected the gate to redirect an unauthenticated request")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
}

func TestAuthRequiredRouteProxiesWhenAuthenticated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret content"))
	}))
	defer upstream.Close()

	gate := &fakeGate{authenticated: true}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "secret content" {
		t.Fatalf("status = %d, body = %q, want 200 / secret content", rec.Code, rec.Body.String())
	}
}

func TestAuthRequiredRouteWithoutGateFailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reached"))
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, nil, // no gate configured, even though the route demands auth
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (fail closed, not open)", rec.Code)
	}
}

func TestAccessPolicyRejectsAuthenticatedPrincipalWithoutClaims(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "user-123",
			Email:         "mallory@other.com",
			EmailVerified: true,
		},
	}
	handler, err := New(
		[]config.Route{{
			Host:   "private.localhost",
			Target: upstream.URL,
			Access: &config.AccessPolicy{AllowedDomains: []string{"example.com"}},
		}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if upstreamCalled {
		t.Fatal("expected denied request not to reach upstream")
	}
}

func TestAccessPolicyAllowsMatchingClaims(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("allowed"))
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "user-123",
			Email:         "alice@example.com",
			EmailVerified: true,
			Groups:        []string{"engineering"},
		},
	}
	handler, err := New(
		[]config.Route{{
			Host:   "private.localhost",
			Target: upstream.URL,
			Access: &config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"engineering"},
			},
		}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "allowed" {
		t.Fatalf("status = %d, body = %q, want 200 / allowed", rec.Code, rec.Body.String())
	}
}

func TestProtectedRoutePropagatesVerifiedIdentityHeaders(t *testing.T) {
	var gotEmail, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = r.Header.Get(authEmailHeader)
		gotUser = r.Header.Get(authUserHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "verified-subject",
			Email:         "verified@example.com",
			EmailVerified: true,
		},
	}
	handler, err := New(
		[]config.Route{{
			Host:   "private.localhost",
			Target: upstream.URL,
			Access: &config.AccessPolicy{},
		}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	req.Header.Set(authEmailHeader, "forged@attacker.example")
	req.Header.Set(authUserHeader, "forged-subject")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if gotEmail != "verified@example.com" || gotUser != "verified-subject" {
		t.Fatalf("identity headers = (%q, %q), want verified email and subject", gotEmail, gotUser)
	}
}

func TestPublicRouteStripsClientIdentityHeaders(t *testing.T) {
	var gotEmail, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = r.Header.Get(authEmailHeader)
		gotUser = r.Header.Get(authUserHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "public.localhost", Target: upstream.URL}},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://public.localhost/", nil)
	req.Host = "public.localhost"
	req.Header.Set(authEmailHeader, "forged@attacker.example")
	req.Header.Set(authUserHeader, "forged-subject")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if gotEmail != "" || gotUser != "" {
		t.Fatalf("identity headers = (%q, %q), want both stripped", gotEmail, gotUser)
	}
}

func TestRoutesUsingNormalizedRequestHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("normalized"))
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "Known.Localhost.:8080", Target: upstream.URL}},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://known.localhost:8080/", nil)
	req.Host = "KNOWN.LOCALHOST:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "normalized" {
		t.Fatalf("status = %d, body = %q, want 200 / normalized", rec.Code, rec.Body.String())
	}
}

func TestNewRejectsDuplicateNormalizedHosts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	_, err := New(
		[]config.Route{
			{Host: "APP.localhost", Target: upstream.URL},
			{Host: "app.localhost.", Target: upstream.URL},
		},
		nil, nil,
	)
	if err == nil {
		t.Fatal("expected duplicate normalized route hosts to be rejected")
	}
}

func TestInvalidRequestHostReturns400(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "app.localhost", Target: upstream.URL}},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.Host = "bad host"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if upstreamCalled {
		t.Fatal("invalid request host should not reach upstream")
	}
}
