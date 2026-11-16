package routemanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

// Through a Cloudflare Tunnel, cloudflared connects from loopback over plain
// HTTP and asserts the visitor's address and scheme. The whole public
// handler must pass both to the application, and only for that peer.
func TestCloudflareTunnelIngressCarriesVisitorSchemeAndAddress(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For"))
	}))
	defer upstream.Close()
	cfg := config.Config{
		Listen:  "127.0.0.1:8888",
		Ingress: &config.Ingress{Mode: config.IngressCloudflareTunnel, CloudflareTunnel: &config.CloudflareTunnelIngress{Token: "env:OZAL_TEST_CF_TOKEN"}},
		Routes:  []config.Route{{Host: "app.example.com", Target: upstream.URL}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := New(ctx, writeConfig(t, cfg), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, peer, want string }{
		{"cloudflared on loopback", "127.0.0.1:40000", "https 198.51.100.23"},
		{"any other peer", "192.0.2.50:40000", "http 192.0.2.50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://app.example.com/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("CF-Connecting-IP", "198.51.100.23")
			w := httptest.NewRecorder()
			m.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Body.String() != tc.want {
				t.Fatalf("response %d %q, want %q", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}
