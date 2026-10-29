package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestTrustedProxyForwardsSelectedIPAndDropsSpoofedChains(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Clone(); w.WriteHeader(204) }))
	defer upstream.Close()
	prefixes := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	for _, tc := range []struct {
		name, peer string
		header     []string
		trusted    bool
		want       string
	}{
		{"trusted", "127.0.0.1:1234", []string{"198.51.100.23"}, true, "198.51.100.23"},
		{"untrusted", "203.0.113.9:1234", []string{"198.51.100.23"}, true, "203.0.113.9"},
		{"opt in disabled", "127.0.0.1:1234", []string{"198.51.100.23"}, false, "127.0.0.1"},
		{"trusted IPv6", "[::1]:1234", []string{"2001:db8::23"}, true, "2001:db8::23"},
		{"no header", "127.0.0.1:1234", nil, true, "127.0.0.1"},
		{"bad header", "127.0.0.1:1234", []string{"not-an-ip"}, true, "127.0.0.1"},
		{"repeated header", "127.0.0.1:1234", []string{"198.51.100.23", "198.51.100.24"}, true, "127.0.0.1"},
		{"invalid peer", "bad-peer", []string{"198.51.100.23"}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opts []Option
			if tc.trusted {
				opts = append(opts, WithTrustedProxies(prefixes))
			}
			handler, err := New([]config.Route{{Host: "public.localhost", Target: upstream.URL}}, nil, nil, opts...)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "http://public.localhost/", nil)
			req.RemoteAddr = tc.peer
			req.Header["Cf-Connecting-Ip"] = tc.header
			req.Header.Set("X-Forwarded-For", "6.6.6.6, 7.7.7.7")
			req.Header["X_Forwarded_For"] = []string{"8.8.8.8"}
			req.Header.Set("Forwarded", "for=6.6.6.6")
			req.Header.Set("X-Real-IP", "6.6.6.6")
			req.Header.Set("Cookie", "sb_session=edge-private; app_session=keep")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 204 {
				t.Fatalf("status: %d", rec.Code)
			}
			got := <-seen
			if value := got.Get("X-Forwarded-For"); value != tc.want {
				t.Errorf("XFF = %q, want %q", value, tc.want)
			}
			for _, name := range []string{"Cf-Connecting-Ip", "X_Forwarded_For", "Forwarded", "X-Real-Ip"} {
				if got.Get(name) != "" {
					t.Errorf("spoofable header %s leaked", name)
				}
			}
			if got.Get("Cookie") != "app_session=keep" {
				t.Error("edge session cookie leaked or app cookie lost")
			}
		})
	}
}
