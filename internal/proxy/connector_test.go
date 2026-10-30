package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/pipeline"
)

func TestConnectorRouteRequiresExplicitTransport(t *testing.T) {
	routes := []config.Route{{Host: "app.localhost", Connector: "portfolio"}}
	if _, err := New(routes, nil, nil); err == nil {
		t.Fatal("connector route accepted with TCP fallback")
	}
	route := config.Route{Host: "app.localhost", Connector: "portfolio", Target: "http://localhost:8000"}
	if _, err := New([]config.Route{route}, nil, nil, WithConnectorTransports(map[string]http.RoundTripper{"portfolio": http.DefaultTransport})); err == nil {
		t.Fatal("connector route with a second destination accepted")
	}
}

// edgeThroughConnector wires an edge proxy to a connector proxy the way the
// tunnel does, minus the mTLS transport the tunnel tests already cover: the
// edge's connector transport dials the connector's listener, and the
// connector forwards to app with authenticated tunnel trust.
func edgeThroughConnector(t *testing.T, route config.Route, gate *fakeGate, app http.Handler) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(app)
	t.Cleanup(upstream.Close)
	connector, err := New([]config.Route{{Host: route.Host, Target: upstream.URL}}, nil, nil, WithAuthenticatedTunnelTrust())
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	tunnel := httptest.NewServer(connector)
	t.Cleanup(tunnel.Close)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, tunnel.Listener.Addr().String())
	}}
	t.Cleanup(transport.CloseIdleConnections)
	route.Connector = "homelab"
	edge, err := New([]config.Route{route}, nil, gate, WithConnectorTransports(map[string]http.RoundTripper{"homelab": transport}))
	if err != nil {
		t.Fatalf("edge: %v", err)
	}
	return edge
}

func TestProtectedConnectorRouteDeliversTheAuthorizedIdentity(t *testing.T) {
	var seen http.Header
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "sub-ana", Email: "ana@example.com", EmailVerified: true}}
	route := config.Route{Host: "app.localhost", Access: &config.AccessPolicy{AllowedEmails: []string{"ana@example.com"}}}
	edge := edgeThroughConnector(t, route, gate, app)

	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.Header.Set(pipeline.IdentityUserHeader, "sub-mallory") // forged by the visitor
	req.Header.Set(pipeline.VisitorUserHeader, "sub-mallory")
	req.Header.Set("Cookie", auth.SessionCookieName+"=session; sb_login=login-attempt; sb_handoff=legacy-binding; sb_handoff_transaction=binding; theme=dark")
	rec := httptest.NewRecorder()
	edge.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := seen.Get(pipeline.IdentityUserHeader); got != "sub-ana" {
		t.Errorf("application saw user %q, want the authorized subject", got)
	}
	if got := seen.Get(pipeline.IdentityEmailHeader); got != "ana@example.com" {
		t.Errorf("application saw email %q, want the authorized email", got)
	}
	for _, internal := range []string{pipeline.VisitorUserHeader, pipeline.VisitorEmailHeader, pipeline.VisitorIPHeader} {
		if got := seen.Get(internal); got != "" {
			t.Errorf("internal header %s reached the application: %q", internal, got)
		}
	}
	if got := seen.Get("Cookie"); got != "theme=dark" {
		t.Errorf("application saw cookies %q, want the edge session removed", got)
	}
}

func TestConnectorRouteIdentityCannotBeForged(t *testing.T) {
	var seen http.Header
	reached := false
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached, seen = true, r.Header.Clone() })

	t.Run("anonymous route", func(t *testing.T) {
		edge := edgeThroughConnector(t, config.Route{Host: "app.localhost"}, nil, app)
		req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
		for _, name := range []string{pipeline.IdentityUserHeader, pipeline.IdentityEmailHeader, pipeline.VisitorUserHeader, pipeline.VisitorEmailHeader, "X-Ozal-Visitor_User"} {
			req.Header[name] = []string{"forged"}
		}
		edge.ServeHTTP(httptest.NewRecorder(), req)
		for _, name := range []string{pipeline.IdentityUserHeader, pipeline.IdentityEmailHeader} {
			if got := seen.Get(name); got != "" {
				t.Errorf("forged %s reached the application: %q", name, got)
			}
		}
	})

	t.Run("unauthenticated visitor", func(t *testing.T) {
		reached = false
		route := config.Route{Host: "app.localhost", Access: &config.AccessPolicy{}}
		edge := edgeThroughConnector(t, route, &fakeGate{}, app)
		rec := httptest.NewRecorder()
		edge.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil))
		if rec.Code != http.StatusFound || reached {
			t.Fatalf("status = %d, reached = %v; want a login redirect before the tunnel", rec.Code, reached)
		}
	})

	t.Run("denied by policy", func(t *testing.T) {
		reached = false
		gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "sub-eve", Email: "eve@example.com", EmailVerified: true}}
		route := config.Route{Host: "app.localhost", Access: &config.AccessPolicy{AllowedEmails: []string{"ana@example.com"}}}
		edge := edgeThroughConnector(t, route, gate, app)
		rec := httptest.NewRecorder()
		edge.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil))
		if rec.Code != http.StatusForbidden || reached {
			t.Fatalf("status = %d, reached = %v; want 403 before the tunnel", rec.Code, reached)
		}
	})
}
