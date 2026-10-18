package pipeline

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/ratelimit"
)

type stubAllower struct {
	allow bool
	wait  time.Duration
	keys  []string
}

func (s *stubAllower) Allow(k string) (bool, time.Duration) {
	s.keys = append(s.keys, k)
	return s.allow, s.wait
}

func TestRateLimitStage(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	general, auth := &stubAllower{allow: true}, &stubAllower{allow: false, wait: 1500 * time.Millisecond}
	h := Chain(Trust, RateLimit(general, auth, "/_/auth/"))(ok)

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.9:1234"
		req.Header.Set("X-Forwarded-For", "6.6.6.6")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("/app"); rec.Code != http.StatusNoContent {
		t.Fatalf("general path: got %d", rec.Code)
	}
	rec := do("/_/auth/login")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("auth path: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2 (1500ms rounded up)", got)
	}
	wantKey := "203.0.113.9|example.com"
	if len(auth.keys) != 1 || auth.keys[0] != wantKey {
		t.Fatalf("auth limiter keyed on %v, want %q", auth.keys, wantKey)
	}
	if len(general.keys) != 1 || general.keys[0] != wantKey {
		t.Fatalf("general limiter keyed on %v, want %q", general.keys, wantKey)
	}
}

func TestRateLimitStageSeparatesRoutes(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	general := &stubAllower{allow: true}
	h := Chain(Trust, RateLimit(general, nil, ""))(ok)

	for _, host := range []string{"a.example.com", "b.example.com"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/app", nil)
		req.Host = host
		req.RemoteAddr = "203.0.113.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if len(general.keys) != 2 || general.keys[0] == general.keys[1] {
		t.Fatalf("keys = %v, want one per route", general.keys)
	}
}

func TestRateLimitStageEmitsEvent(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	recorder := &events.Recorder{}
	general := &stubAllower{allow: false, wait: time.Second}
	h := Chain(Trust, RateLimit(general, general, "/_/auth/", WithRateLimitSink(recorder)))(ok)

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	event, found := recorder.Find(events.AccessRateLimited)
	if !found {
		t.Fatal("no access.rate_limited event")
	}
	if event.Code != "ip_route" || event.RemoteAddr != "203.0.113.9:1234" {
		t.Fatalf("event = %+v", event)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		wait time.Duration
		want int
	}{
		{0, 1},
		{-time.Second, 1},
		{10 * time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{90 * time.Second, 90},
	}
	for _, tt := range tests {
		if got := RetryAfterSeconds(tt.wait); got != tt.want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", tt.wait, got, tt.want)
		}
	}
}

func TestRateLimitEquivalentHostsShareBudget(t *testing.T) {
	for _, hosts := range [][]string{{"APP.example", "app.example."}, {"[2001:db8::1]", "[2001:0db8:0:0:0:0:0:1]"}} {
		for _, path := range []string{"/", "/_/auth/login"} {
			t.Run(hosts[0]+path, func(t *testing.T) {
				limiter := ratelimit.NewLimiter(0.001, 1, time.Hour)
				handler := Chain(Trust, RateLimit(limiter, limiter, "/_/auth/"))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
				for i, host := range hosts {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					req.Host = host
					req.RemoteAddr = "203.0.113.9:1234"
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					want := http.StatusNoContent
					if i > 0 {
						want = http.StatusTooManyRequests
					}
					if rec.Code != want {
						t.Fatalf("host %s: status %d, want %d", host, rec.Code, want)
					}
				}
			})
		}
	}
}
