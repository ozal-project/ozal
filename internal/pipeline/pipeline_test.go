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
		{"groups", "X-Auth-Request-Groups"},
		{"preferred username", "X-Auth-Request-Preferred-Username"},
		{"access token", "X-Auth-Request-Access-Token"},
		{"lowercase", "x-auth-request-user"},
		{"underscore variant", "X_auth_request_email"},
		{"mixed separators", "X_Auth-Request_User"},
		{"nested name", "X-Auth-Request-Email-Extra"},
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

	t.Run("keeps a lookalike outside the prefix", func(t *testing.T) {
		h := http.Header{"X-Auth-Requested-Email": []string{"keep-me"}}
		stripIdentityHeaders(h)
		if got := h.Get("X-Auth-Requested-Email"); got != "keep-me" {
			t.Fatalf("lookalike header = %q, want keep-me", got)
		}
	})
}

func TestTrustStripsTheWholeOAuth2ProxyNamespace(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	for _, name := range []string{"X-Auth-Request-Email", "X-Auth-Request-Groups", "X-Auth-Request-Preferred-Username"} {
		req.Header.Set(name, "forged")
	}
	req.Header.Set("X-Request-Id", "keep-me")
	Trust(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"X-Auth-Request-Email", "X-Auth-Request-Groups", "X-Auth-Request-Preferred-Username"} {
			if got := r.Header.Get(name); got != "" {
				t.Errorf("%s reached the handler: %q", name, got)
			}
		}
		if got := r.Header.Get("X-Request-Id"); got != "keep-me" {
			t.Errorf("X-Request-Id = %q, want keep-me", got)
		}
	})).ServeHTTP(httptest.NewRecorder(), req)
}
