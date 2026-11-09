package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/assertion"
	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
)

const testIssuer = "https://auth.example.com"

func assertionRoutes(upstream string) []config.Route {
	return []config.Route{
		{Host: "custodia.example.com", Target: upstream, Access: &config.AccessPolicy{RequiredGroups: []string{"members"}}, IdentityAssertion: &config.RouteAssertion{Audience: "custodia"}},
		{Host: "wiki.example.com", Target: upstream, Access: &config.AccessPolicy{RequiredGroups: []string{"members"}}},
		{Host: "public.example.com", Target: upstream},
	}
}

// The application receives an assertion it can verify, for its own audience,
// only on routes that ask for one; a client can never supply its own.
func TestIdentityAssertionReachesOnlyItsApplication(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Clone(); w.WriteHeader(204) }))
	defer upstream.Close()
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := assertion.NewSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "passkey:alice", Email: "alice@example.com", Groups: []string{"members"}}}
	handler, err := New(assertionRoutes(upstream.URL), nil, gate,
		WithIdentityAssertion(signer, testIssuer),
		WithTrustedProxies([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}))
	if err != nil {
		t.Fatal(err)
	}
	forged := "eyJhbGciOiJub25lIn0.eyJzdWIiOiJyb290In0."
	for _, tc := range []struct {
		host   string
		signed bool
	}{
		{"custodia.example.com", true},
		{"wiki.example.com", false},
		{"public.example.com", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tc.host+"/", nil)
			// cloudflared reaches the edge over HTTP, carrying public HTTPS.
			req.RemoteAddr = "127.0.0.1:40000"
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set(assertion.Header, forged)
			req.Header["X_Ozal_Assertion"] = []string{forged}
			handler.ServeHTTP(httptest.NewRecorder(), req)
			got := <-seen
			if got.Get("X-Forwarded-Proto") != "https" {
				t.Fatal("public HTTPS scheme lost while forwarding the assertion")
			}
			for name := range got {
				if strings.EqualFold(name, "X_Ozal_Assertion") {
					t.Fatal("an underscore spelling reached the application")
				}
			}
			token := got.Get(assertion.Header)
			if !tc.signed {
				if token != "" {
					t.Fatalf("assertion %q reached a route that did not ask for one", token)
				}
				return
			}
			if token == forged || token == "" {
				t.Fatalf("the application got %q, not this edge's assertion", token)
			}
			claims, reject, err := assertion.Verify(token, []ed25519.PublicKey{public}, testIssuer, "custodia", time.Now(), time.Second)
			if err != nil || reject != "" || claims.Subject != "passkey:alice" || claims.Email != "alice@example.com" {
				t.Fatalf("claims %+v, reject %q, %v", claims, reject, err)
			}
			if _, reject, _ := assertion.Verify(token, []ed25519.PublicKey{public}, testIssuer, "wiki", time.Now(), time.Second); reject != assertion.RejectAudience {
				t.Fatalf("assertion accepted by another audience: %q", reject)
			}
		})
	}
}

type failingSigner struct{}

func (failingSigner) Sign(string, string, assertion.Person) (string, error) {
	return "", errors.New("no key")
}

func TestIdentityAssertionFailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("forwarded without an assertion") }))
	defer upstream.Close()
	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "passkey:alice", Groups: []string{"members"}}}
	if _, err := New(assertionRoutes(upstream.URL), nil, gate); err == nil {
		t.Fatal("built a route that asks for an assertion without a signing key")
	}
	handler, err := New(assertionRoutes(upstream.URL), nil, gate, WithIdentityAssertion(failingSigner{}, testIssuer))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://custodia.example.com/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	// Unauthenticated visitors are sent to login, never forwarded.
	gate.authenticated = false
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://custodia.example.com/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("anonymous status %d", rec.Code)
	}
}
