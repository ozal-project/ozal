package pipeline

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// The public scheme decides Secure cookies, login redirects and the
// X-Forwarded-Proto an application sees. Only a trusted proxy may upgrade it,
// and only with one unambiguous claim.
func TestTrustDecidesPublicSchemeOnlyFromTrustedProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name, peer string
		header     map[string][]string
		tls        bool
		want       bool
	}{
		{"trusted proxy asserts https", "127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https"}}, false, true},
		{"trusted IPv6 proxy, mixed case", "[::1]:1234", map[string][]string{"X-Forwarded-Proto": {" HTTPS "}}, false, true},
		{"untrusted peer asserts https", "203.0.113.9:1234", map[string][]string{"X-Forwarded-Proto": {"https"}}, false, false},
		{"trusted proxy says http", "127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"http"}}, false, false},
		{"repeated claim", "127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https", "https"}}, false, false},
		{"comma-joined claim", "127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https, http"}}, false, false},
		{"underscore spelling only", "127.0.0.1:1234", map[string][]string{"X_Forwarded_Proto": {"https"}}, false, false},
		{"underscore next to canonical", "127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https"}, "X_Forwarded_Proto": {"https"}}, false, false},
		{"no claim", "127.0.0.1:1234", nil, false, false},
		{"own TLS", "203.0.113.9:1234", nil, true, true},
		{"own TLS ignores a downgrade claim", "127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"http"}}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://app.example.com/", nil)
			req.RemoteAddr = tc.peer
			for name, values := range tc.header {
				req.Header[name] = values
			}
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			var got bool
			TrustWithProxies(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = PublicHTTPS(r)
				for name := range tc.header {
					if len(r.Header[name]) > 0 {
						t.Errorf("%s survived the trust stage", name)
					}
				}
			})).ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("PublicHTTPS = %v, want %v", got, tc.want)
			}
		})
	}
}

// An inner Trust (the proxy's own) sees stripped headers; it must keep the
// outer decision instead of downgrading the scheme to the transport's.
func TestNestedTrustKeepsThePublicScheme(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	req := httptest.NewRequest("GET", "http://app.example.com/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	var got bool
	inner := TrustWithProxies(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = PublicHTTPS(r) }))
	TrustWithProxies(trusted)(inner).ServeHTTP(httptest.NewRecorder(), req)
	if !got {
		t.Fatal("inner trust downgraded the public scheme")
	}
}

func TestPublicHTTPSWithoutTrustFollowsTheTransport(t *testing.T) {
	req := httptest.NewRequest("GET", "http://app.example.com/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if PublicHTTPS(req) {
		t.Fatal("a client header upgraded the scheme without the trust stage")
	}
	req.TLS = &tls.ConnectionState{}
	if !PublicHTTPS(req) {
		t.Fatal("own TLS not reported as HTTPS")
	}
}

func FuzzAssertsHTTPS(f *testing.F) {
	for _, seed := range []string{"https", "HTTPS", " https ", "http", "https,http", "", "httpss"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if assertsHTTPS([]string{value}) && !(len(value) >= 5) {
			t.Fatalf("accepted %q", value)
		}
		if assertsHTTPS([]string{value, value}) {
			t.Fatal("accepted a repeated claim")
		}
		if assertsHTTPS(nil) {
			t.Fatal("accepted no claim")
		}
	})
}
