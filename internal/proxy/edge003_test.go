package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

// EDGE-003: a route without a port answers on any listener port; a route
// with a port only on that port, and it wins over the portless one.
func TestPortlessRouteMatchesAnyPort(t *testing.T) {
	serve := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(name)) }))
	}
	any, pinned := serve("any"), serve("pinned")
	defer any.Close()
	defer pinned.Close()
	handler, err := New([]config.Route{
		{Host: "app.localhost", Target: any.URL},
		{Host: "app.localhost:9000", Target: pinned.URL},
		{Host: "only.localhost:8888", Target: pinned.URL},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		"app.localhost":       "any",
		"app.localhost:8888":  "any",
		"APP.localhost:443":   "any",
		"app.localhost:9000":  "pinned",
		"only.localhost:8888": "pinned",
		"only.localhost:9999": "",
		"only.localhost":      "",
		"other.localhost:80":  "",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://placeholder/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		got := ""
		if rec.Code == http.StatusOK {
			got = rec.Body.String()
		}
		if got != want {
			t.Errorf("Host %q → %q (status %d), want %q", host, got, rec.Code, want)
		}
	}
}
