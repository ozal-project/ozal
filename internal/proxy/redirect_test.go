package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

// A retired hostname answers with a permanent redirect that keeps the path
// and query, never proxies, and cannot be steered to another host.
func TestRedirectRoute(t *testing.T) {
	handler, err := New([]config.Route{{Host: "switchboard.example.com", Redirect: "https://ozal.dev"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, target string
		code           int
		location       string
	}{
		{http.MethodGet, "/", 301, "https://ozal.dev/"},
		{http.MethodGet, "/docs/install?lang=es", 301, "https://ozal.dev/docs/install?lang=es"},
		{http.MethodHead, "/x", 301, "https://ozal.dev/x"},
		{http.MethodPost, "/api", 308, "https://ozal.dev/api"},
		// A path that looks like another host stays a path on ozal.dev.
		{http.MethodGet, "//evil.example/x", 301, "https://ozal.dev//evil.example/x"},
		{http.MethodGet, "/a%2Fb", 301, "https://ozal.dev/a%2Fb"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "http://switchboard.example.com"+c.target, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != c.code || rec.Header().Get("Location") != c.location {
			t.Errorf("%s %s = %d %q, want %d %q", c.method, c.target, rec.Code, rec.Header().Get("Location"), c.code, c.location)
		}
	}
}

func TestRedirectKeepsABasePath(t *testing.T) {
	handler, err := New([]config.Route{{Host: "old.example.com", Redirect: "https://ozal.dev/archive/"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://old.example.com/post?id=1", nil))
	if got := rec.Header().Get("Location"); got != "https://ozal.dev/archive/post?id=1" {
		t.Fatalf("Location = %q", got)
	}
}
