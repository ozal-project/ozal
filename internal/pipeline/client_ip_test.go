package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustSelectsIPWithoutChangingTransportPeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("192.0.2.0/24")}
	cases := []struct {
		name, peer string
		forwarded  []string
		want       string
	}{
		{"trusted IPv4", "127.0.0.1:1234", []string{"198.51.100.23"}, "198.51.100.23"},
		{"trusted IPv6 peer", "[::1]:1234", []string{"2001:db8::23"}, "2001:db8::23"},
		{"trusted subnet", "192.0.2.15:1234", []string{"198.51.100.23"}, "198.51.100.23"},
		{"mapped peer", "[::ffff:127.0.0.1]:1234", []string{"198.51.100.23"}, "198.51.100.23"},
		{"mapped client", "127.0.0.1:1234", []string{"::ffff:198.51.100.23"}, "198.51.100.23"},
		{"untrusted peer", "203.0.113.9:1234", []string{"198.51.100.23"}, "203.0.113.9"},
		{"outside subnet", "192.0.3.15:1234", []string{"198.51.100.23"}, "192.0.3.15"},
		{"absent", "127.0.0.1:1234", nil, "127.0.0.1"},
		{"empty", "127.0.0.1:1234", []string{""}, "127.0.0.1"},
		{"malformed", "127.0.0.1:1234", []string{"not-an-ip"}, "127.0.0.1"},
		{"comma chain", "127.0.0.1:1234", []string{"198.51.100.23, 203.0.113.9"}, "127.0.0.1"},
		{"repeated header", "127.0.0.1:1234", []string{"198.51.100.23", "198.51.100.24"}, "127.0.0.1"},
		{"scoped header", "127.0.0.1:1234", []string{"fe80::1%eth0"}, "127.0.0.1"},
		{"unspecified header", "127.0.0.1:1234", []string{"0.0.0.0"}, "127.0.0.1"},
		{"multicast header", "127.0.0.1:1234", []string{"ff02::1"}, "127.0.0.1"},
		{"malformed peer", "not-a-peer", []string{"198.51.100.23"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://app.localhost/", nil)
			req.RemoteAddr = tc.peer
			req.Header["Cf-Connecting-Ip"] = tc.forwarded
			req.Header.Set("X-Forwarded-For", "6.6.6.6, 7.7.7.7")
			req.Header.Set("X-Real-Ip", "6.6.6.6")
			req.Header.Set("Forwarded", "for=6.6.6.6")
			req.Header["X_Forwarded_For"] = []string{"6.6.6.6"}
			req.Header.Set("Cf-Ray", "trace-id")
			handler := TrustWithProxies(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ip, ok := ClientIP(r.Context())
				if tc.want == "" {
					if ok {
						t.Errorf("invalid peer produced IP: %s", ip)
					}
				} else if !ok || ip.String() != tc.want {
					t.Errorf("client IP = %s (%v), want %s", ip, ok, tc.want)
				}
				if r.RemoteAddr != tc.peer {
					t.Errorf("peer rewritten: %q", r.RemoteAddr)
				}
				for _, name := range []string{"Cf-Connecting-Ip", "X-Forwarded-For", "X-Real-Ip", "Forwarded", "X_Forwarded_For"} {
					if len(r.Header[name]) > 0 {
						t.Errorf("untrusted header %s survived", name)
					}
				}
				if r.Header.Get("Cf-Ray") != "trace-id" {
					t.Error("unrelated tracing header removed")
				}
			}))
			handler.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestTrustIsOptInAndRejectsAmbiguousAliases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stage  Stage
		header http.Header
	}{
		{"disabled by default", Trust, http.Header{"Cf-Connecting-Ip": []string{"198.51.100.23"}}},
		{"underscore alias", TrustWithProxies([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}), http.Header{"Cf_Connecting_Ip": []string{"198.51.100.23"}}},
		{"canonical plus alias", TrustWithProxies([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}), http.Header{"Cf-Connecting-Ip": []string{"198.51.100.23"}, "Cf_Connecting_Ip": []string{"198.51.100.24"}}},
		{"case duplicates", TrustWithProxies([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}), http.Header{"Cf-Connecting-Ip": []string{"198.51.100.23"}, "cf-connecting-ip": []string{"198.51.100.24"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header = tc.header
			tc.stage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ip, _ := ClientIP(r.Context())
				if ip.String() != "127.0.0.1" {
					t.Errorf("forged IP accepted: %s", ip)
				}
				if len(r.Header) > 0 {
					t.Errorf("forged IP headers leaked: %v", r.Header)
				}
			})).ServeHTTP(httptest.NewRecorder(), r)
		})
	}
	if _, ok := ClientIP(context.Background()); ok {
		t.Error("missing context value reported as valid")
	}
}

func TestTrustCapturesImmutableProxyList(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	stage := TrustWithProxies(prefixes)
	prefixes[0] = netip.MustParsePrefix("203.0.113.0/24")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("CF-Connecting-IP", "198.51.100.23")
	stage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _ := ClientIP(r.Context())
		if ip.String() != "198.51.100.23" {
			t.Error("mutating the input changed the stage")
		}
	})).ServeHTTP(httptest.NewRecorder(), r)
}

func TestAuthenticatedTunnelTakesVisitorIPFromTheEdgeAssertion(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   string
	}{
		{"IPv4", []string{"198.51.100.23"}, "198.51.100.23"},
		{"IPv6", []string{"2001:db8::23"}, "2001:db8::23"},
		{"mapped IPv4", []string{"::ffff:198.51.100.23"}, "198.51.100.23"},
		{"absent keeps the tunnel peer", nil, "10.8.0.1"},
		{"empty", []string{""}, "10.8.0.1"},
		{"malformed", []string{"not-an-ip"}, "10.8.0.1"},
		{"comma chain", []string{"198.51.100.23, 203.0.113.9"}, "10.8.0.1"},
		{"repeated", []string{"198.51.100.23", "198.51.100.24"}, "10.8.0.1"},
		{"scoped", []string{"fe80::1%eth0"}, "10.8.0.1"},
		{"unspecified", []string{"0.0.0.0"}, "10.8.0.1"},
		{"multicast", []string{"ff02::1"}, "10.8.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://app.localhost/", nil)
			req.RemoteAddr = "10.8.0.1:51000"
			req.Header[VisitorIPHeader] = tc.values
			var got string
			var leaked bool
			TrustAuthenticatedTunnel()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ip, _ := ClientIP(r.Context())
				got = ip.String()
				for name := range r.Header {
					leaked = leaked || len(name) > 0 && http.CanonicalHeaderKey(name) == VisitorIPHeader
				}
			})).ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("client IP = %s, want %s", got, tc.want)
			}
			if leaked {
				t.Error("the internal assertion header reached the application")
			}
		})
	}

	t.Run("underscore alias rejects the whole claim", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://app.localhost/", nil)
		req.RemoteAddr = "10.8.0.1:51000"
		req.Header["X_Switchboard_Visitor_Ip"] = []string{"198.51.100.23"}
		req.Header[VisitorIPHeader] = []string{"198.51.100.23"}
		var got string
		TrustAuthenticatedTunnel()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _ := ClientIP(r.Context())
			got = ip.String()
		})).ServeHTTP(httptest.NewRecorder(), req)
		if got != "10.8.0.1" {
			t.Errorf("client IP = %s: an aliased claim must be ignored entirely", got)
		}
	})
}

// Everywhere except behind an authenticated tunnel, the assertion header is
// just client input: it is never believed and never forwarded.
func TestVisitorAssertionIsNeverTrustedOnOrdinaryListeners(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	for name, stage := range map[string]Stage{
		"no trusted proxies":   Trust,
		"trusted proxy peer":   TrustWithProxies(trusted),
		"untrusted proxy peer": TrustWithProxies([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}),
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://app.localhost/", nil)
			req.RemoteAddr = "203.0.113.9:1234"
			req.Header[VisitorIPHeader] = []string{"198.51.100.23"}
			req.Header["X_Switchboard_Visitor_Ip"] = []string{"198.51.100.23"}
			var got string
			var remaining []string
			stage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ip, _ := ClientIP(r.Context())
				got = ip.String()
				for name := range r.Header {
					remaining = append(remaining, name)
				}
			})).ServeHTTP(httptest.NewRecorder(), req)
			if got != "203.0.113.9" {
				t.Errorf("client IP = %s, want the transport peer", got)
			}
			if len(remaining) != 0 {
				t.Errorf("assertion headers reached the handler: %v", remaining)
			}
		})
	}
}
