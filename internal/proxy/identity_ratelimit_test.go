package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

// identityStub records the keys it is charged with and answers with a
// scripted decision.
type identityStub struct {
	mu      sync.Mutex
	allow   bool
	wait    time.Duration
	keys    []string
	charged int
}

func (s *identityStub) Allow(key string) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.charged++
	s.keys = append(s.keys, key)
	return s.allow, s.wait
}

func TestIdentityLimiterRefusesAfterPolicyAllows(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("reached"))
	}))
	defer upstream.Close()

	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "user-1", Email: "ana@example.com"}}
	limiter := &identityStub{allow: false, wait: 2 * time.Second}
	recorder := &events.Recorder{}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, gate,
		WithIdentityLimiter(limiter),
		WithEventSink(recorder),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	if rec.Body.String() == "reached" {
		t.Fatal("a rate-limited request must not reach the upstream")
	}
	if limiter.charged != 1 || len(limiter.keys) != 1 || limiter.keys[0] != "identity|user-1" {
		t.Fatalf("charged keys = %v", limiter.keys)
	}
	if recorder.Has(events.AccessAllowed) {
		t.Fatal("a rate-limited request must not be reported as allowed")
	}
	event, found := recorder.Find(events.AccessRateLimited)
	if !found || event.Code != "identity" || event.Actor != "user-1" {
		t.Fatalf("event = %+v, found %v", event, found)
	}
}

func TestIdentityLimiterAllowsWithinBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("reached"))
	}))
	defer upstream.Close()

	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "user-1"}}
	limiter := &identityStub{allow: true}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, gate,
		WithIdentityLimiter(limiter),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "reached" {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if limiter.charged != 1 {
		t.Fatalf("charged %d times, want 1", limiter.charged)
	}
}

func TestIdentityLimiterIgnoresUnauthenticatedRequests(t *testing.T) {
	// Public routes never reach the identity dimension: there is no
	// principal, and the per-IP stage is responsible for them.
	limiter := &identityStub{allow: false}
	handler, err := New([]config.Route{{Host: "public.localhost", Target: "http://127.0.0.1:1"}}, nil, nil, WithIdentityLimiter(limiter))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://public.localhost/", nil)
	req.Host = "public.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if limiter.charged != 0 {
		t.Fatalf("identity limiter was charged %d times on a public route", limiter.charged)
	}
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("a public route must not be limited by the identity dimension")
	}
}
