package pipeline

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubAllower struct {
	allow bool
	keys  []string
}

func (s *stubAllower) Allow(k string) bool { s.keys = append(s.keys, k); return s.allow }

func TestRateLimitStage(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	general, auth := &stubAllower{allow: true}, &stubAllower{allow: false}
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
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("auth path: got %d, headers %v", rec.Code, rec.Header())
	}
	if len(auth.keys) != 1 || auth.keys[0] != "203.0.113.9" {
		t.Fatalf("auth limiter keyed on %v, want the transport peer", auth.keys)
	}
}
