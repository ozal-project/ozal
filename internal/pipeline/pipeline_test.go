package pipeline

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// recorder appends a stage name when it runs; requests see A,B in order and
// responses unwind B,A — that is what "first listed is outermost" means.
func recorder(name string, log *[]string) Stage {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*log = append(*log, name+":in")
			next.ServeHTTP(w, r)
			*log = append(*log, name+":out")
		})
	}
}

func TestChainRunsStagesInOrder(t *testing.T) {
	var log []string
	handler := Chain(recorder("trust", &log), recorder("identify", &log), recorder("forward", &log))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"trust:in", "identify:in", "forward:in", "forward:out", "identify:out", "trust:out"}
	if len(log) != len(want) {
		t.Fatalf("stages ran %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("stage order = %v, want %v", log, want)
		}
	}
}

func TestTrustStripsSpoofedIdentityHeaders(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"canonical email", "X-Auth-Request-Email"},
		{"canonical user", "X-Auth-Request-User"},
		{"lowercase", "x-auth-request-user"},
		{"underscore variant", "X_auth_request_email"},
		{"mixed separators", "X_Auth-Request_User"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{tc.header: []string{"forged"}}
			h["X-Request-Id"] = []string{"keep-me"}
			stripIdentityHeaders(h)
			if _, ok := h[tc.header]; ok {
				t.Fatalf("%s survived the strip", tc.header)
			}
			if got := h.Get("X-Request-Id"); got != "keep-me" {
				t.Fatalf("X-Request-Id = %q, want keep-me", got)
			}
		})
	}

	t.Run("keeps a longer lookalike", func(t *testing.T) {
		h := http.Header{"X-Auth-Request-Email-Extra": []string{"keep-me"}}
		stripIdentityHeaders(h)
		if got := h.Get("X-Auth-Request-Email-Extra"); got != "keep-me" {
			t.Fatalf("lookalike header = %q, want keep-me", got)
		}
	})
}
