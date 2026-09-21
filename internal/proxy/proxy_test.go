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
	})
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
		_, err := New([]config.Route{{Host: "bad.localhost", Target: "://not-a-url"}})
		if err == nil {
			t.Fatal("expected an error for an invalid target URL, got nil")
		}
	})
}
