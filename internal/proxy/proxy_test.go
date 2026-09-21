package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
}

func (f *fakeGate) IsAuthenticated(r *http.Request) bool { return f.authenticated }
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
