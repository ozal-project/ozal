package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/pages"
	"github.com/switchboard-project/switchboard/internal/pipeline"
)

type auditAppenderFunc func(context.Context, events.Event) error

func (f auditAppenderFunc) Append(ctx context.Context, event events.Event) error {
	return f(ctx, event)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRequiredAccessAuditFailureStopsBeforeUpstream(t *testing.T) {
	upstreamCalled := false
	transport := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		upstreamCalled = true
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})

	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "person-1"}}
	var appended events.Event
	appender := auditAppenderFunc(func(_ context.Context, event events.Event) error {
		appended = event
		return errors.New("disk full")
	})
	handler, err := New([]config.Route{{Host: "private.localhost", Target: "http://upstream.local", Access: &config.AccessPolicy{}}}, nil, gate, WithAuditAppender(appender), WithTransport(transport))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if upstreamCalled {
		t.Fatal("upstream received request after required audit append failed")
	}
	if appended.Type != events.AccessAllowed || appended.Host != "private.localhost" {
		t.Fatalf("appended event = %+v", appended)
	}
}

func TestRoutesByHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	handler, err := New([]config.Route{
		{Host: "known.localhost", Target: upstream.URL},
	}, nil, nil)
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
		_, err := New([]config.Route{{Host: "bad.localhost", Target: "://not-a-url"}}, nil, nil)
		if err == nil {
			t.Fatal("expected an error for an invalid target URL, got nil")
		}
	})
}

type fakeChecker struct{ healthy map[string]bool }

func (f fakeChecker) Healthy(host string) bool { return f.healthy[host] }

func TestUnhealthyUpstreamReturns503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reached"))
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "down.localhost", Target: upstream.URL}},
		fakeChecker{healthy: map[string]bool{"down.localhost": false}},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://down.localhost/", nil)
	req.Host = "down.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

type fakeGate struct {
	authenticated bool
	redirected    bool
	principal     auth.Principal
}

func (f *fakeGate) Authenticate(r *http.Request) (auth.Principal, bool) {
	return f.principal, f.authenticated
}
func (f *fakeGate) RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	f.redirected = true
	http.Redirect(w, r, "/_/auth/login", http.StatusFound)
}

func TestUpstream5xxDoesNotPreventRecovery(t *testing.T) {
	calls := 0
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		statusCode := http.StatusOK
		body := "recovered"
		if calls == 1 {
			statusCode = http.StatusServiceUnavailable
			body = "temporarily unavailable\n"
		}
		return &http.Response{
			StatusCode: statusCode,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	handler, err := New([]config.Route{{Host: "recover.localhost", Target: "http://upstream.local"}}, nil, nil, WithTransport(transport))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "upstream 503 is preserved", statusCode: http.StatusServiceUnavailable, body: "temporarily unavailable\n"},
		{name: "later request recovers", statusCode: http.StatusOK, body: "recovered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://recover.localhost/", nil)
			req.Host = "recover.localhost"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.statusCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.statusCode)
			}
			if got := rec.Body.String(); got != tc.body {
				t.Fatalf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

func TestAuthRequiredRouteRedirectsWhenUnauthenticated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reached"))
	}))
	defer upstream.Close()

	gate := &fakeGate{authenticated: false}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !gate.redirected {
		t.Fatal("expected the gate to redirect an unauthenticated request")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
}

func TestAuthRequiredRouteProxiesWhenAuthenticated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret content"))
	}))
	defer upstream.Close()

	gate := &fakeGate{authenticated: true}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "secret content" {
		t.Fatalf("status = %d, body = %q, want 200 / secret content", rec.Code, rec.Body.String())
	}
}

func TestAuthRequiredRouteWithoutGateFailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reached"))
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true}},
		nil, nil, // no gate configured, even though the route demands auth
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (fail closed, not open)", rec.Code)
	}
}

func TestAccessPolicyRejectsAuthenticatedPrincipalWithoutClaims(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "user-123",
			Email:         "mallory@other.com",
			EmailVerified: true,
		},
	}
	recorder := &events.Recorder{}
	handler, err := New(
		[]config.Route{{
			Host:   "private.localhost",
			Target: upstream.URL,
			Access: &config.AccessPolicy{AllowedDomains: []string{"example.com"}},
			Resource: &config.RouteResource{
				Owner: "platform", DataClass: "account_data", Sensitivity: "restricted",
				RiskyActions: []string{"POST", "DELETE"},
			},
		}},
		nil, gate, WithEventSink(recorder),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if upstreamCalled {
		t.Fatal("expected denied request not to reach upstream")
	}
	denied, ok := recorder.Find(events.AccessDenied)
	if !ok || denied.Actor != "user-123" || denied.Host != "private.localhost" || denied.Code == "" {
		t.Fatalf("access.denied event = %+v, %v", denied, ok)
	}
	if denied.Context == nil || denied.Context.Resource.Route.Value == nil || *denied.Context.Resource.Route.Value != "private.localhost" || denied.Context.Session.UserVerified.Value != nil {
		t.Fatalf("access.denied event lost route evidence or inferred unknown user verification: %+v", denied.Context)
	}
	if denied.Context.Resource.Owner.Value == nil || *denied.Context.Resource.Owner.Value != "platform" ||
		denied.Context.Resource.DataClass.Value == nil || *denied.Context.Resource.DataClass.Value != "account_data" ||
		denied.Context.Resource.Sensitivity.Value == nil || *denied.Context.Resource.Sensitivity.Value != "restricted" ||
		denied.Context.Action.Risky.Value == nil || *denied.Context.Action.Risky.Value {
		t.Fatalf("access.denied event omitted route labels or misclassified GET: %+v", denied.Context)
	}
}

func TestAccessPolicyAllowsMatchingClaims(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("allowed"))
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "user-123",
			Email:         "alice@example.com",
			EmailVerified: true,
			Groups:        []string{"engineering"},
		},
	}
	recorder := &events.Recorder{}
	handler, err := New(
		[]config.Route{{
			Host:   "private.localhost",
			Target: upstream.URL,
			Access: &config.AccessPolicy{
				AllowedDomains: []string{"example.com"},
				RequiredGroups: []string{"engineering"},
			},
		}},
		nil, gate, WithEventSink(recorder),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "allowed" {
		t.Fatalf("status = %d, body = %q, want 200 / allowed", rec.Code, rec.Body.String())
	}
	allowed, ok := recorder.Find(events.AccessAllowed)
	if !ok || allowed.Actor != "user-123" || allowed.Host != "private.localhost" {
		t.Fatalf("access.allowed event = %+v, %v", allowed, ok)
	}
	if allowed.Context == nil || allowed.Context.Action.HTTPMethod.Value == nil || *allowed.Context.Action.HTTPMethod.Value != http.MethodGet {
		t.Fatalf("access.allowed event lost request context: %+v", allowed.Context)
	}
}

func TestProtectedRoutePropagatesVerifiedIdentityHeaders(t *testing.T) {
	var gotEmail, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = r.Header.Get(pipeline.IdentityEmailHeader)
		gotUser = r.Header.Get(pipeline.IdentityUserHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "verified-subject",
			Email:         "verified@example.com",
			EmailVerified: true,
		},
	}
	handler, err := New(
		[]config.Route{{
			Host:   "private.localhost",
			Target: upstream.URL,
			Access: &config.AccessPolicy{},
		}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	req.Header.Set(pipeline.IdentityEmailHeader, "forged@attacker.example")
	req.Header.Set(pipeline.IdentityUserHeader, "forged-subject")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if gotEmail != "verified@example.com" || gotUser != "verified-subject" {
		t.Fatalf("identity headers = (%q, %q), want verified email and subject", gotEmail, gotUser)
	}
}

func TestPublicRouteStripsClientIdentityHeaders(t *testing.T) {
	var gotEmail, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = r.Header.Get(pipeline.IdentityEmailHeader)
		gotUser = r.Header.Get(pipeline.IdentityUserHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "public.localhost", Target: upstream.URL}},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://public.localhost/", nil)
	req.Host = "public.localhost"
	req.Header.Set(pipeline.IdentityEmailHeader, "forged@attacker.example")
	req.Header.Set(pipeline.IdentityUserHeader, "forged-subject")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if gotEmail != "" || gotUser != "" {
		t.Fatalf("identity headers = (%q, %q), want both stripped", gotEmail, gotUser)
	}
}

func TestDevErrorPages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	closedPort := func() string {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := probe.Addr().String()
		probe.Close()
		return addr
	}

	fetch := func(t *testing.T, handler http.Handler, host string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	t.Run("unknown host lists the configured hosts when enabled", func(t *testing.T) {
		handler, err := New([]config.Route{{Host: "known.localhost", Target: upstream.URL}}, nil, nil, WithDevErrors())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		rec := fetch(t, handler, "unknown.localhost")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		for _, want := range []string{"no route for host", "known.localhost"} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("dev 404 body missing %q: %s", want, rec.Body.String())
			}
		}
	})

	t.Run("unreachable upstream names the target when enabled", func(t *testing.T) {
		addr := closedPort()
		handler, err := New([]config.Route{{Host: "down.localhost", Target: "http://" + addr}}, nil, nil, WithDevErrors())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		rec := fetch(t, handler, "down.localhost")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
		for _, want := range []string{addr, "hint"} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("dev 502 body missing %q: %s", want, rec.Body.String())
			}
		}
	})

	t.Run("production stays terse", func(t *testing.T) {
		addr := closedPort()
		handler, err := New([]config.Route{
			{Host: "known.localhost", Target: upstream.URL},
			{Host: "down.localhost", Target: "http://" + addr},
		}, nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		notFound := fetch(t, handler, "unknown.localhost")
		if notFound.Code != http.StatusNotFound || strings.Contains(notFound.Body.String(), "known hosts") {
			t.Fatalf("production 404 = %d %q, want a terse message without the route list", notFound.Code, notFound.Body.String())
		}
		badGateway := fetch(t, handler, "down.localhost")
		if badGateway.Code != http.StatusBadGateway || strings.Contains(badGateway.Body.String(), addr) {
			t.Fatalf("production 502 = %d %q, want no upstream address", badGateway.Code, badGateway.Body.String())
		}
	})
}

func TestRoutesUsingNormalizedRequestHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("normalized"))
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "Known.Localhost.:8080", Target: upstream.URL}},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://known.localhost:8080/", nil)
	req.Host = "KNOWN.LOCALHOST:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "normalized" {
		t.Fatalf("status = %d, body = %q, want 200 / normalized", rec.Code, rec.Body.String())
	}
}

func TestNewRejectsDuplicateNormalizedHosts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	_, err := New(
		[]config.Route{
			{Host: "APP.localhost", Target: upstream.URL},
			{Host: "app.localhost.", Target: upstream.URL},
		},
		nil, nil,
	)
	if err == nil {
		t.Fatal("expected duplicate normalized route hosts to be rejected")
	}
}

func TestInvalidRequestHostReturns400(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	handler, err := New(
		[]config.Route{{Host: "app.localhost", Target: upstream.URL}},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.Host = "bad host"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if upstreamCalled {
		t.Fatal("invalid request host should not reach upstream")
	}
}

func TestStaticRouteServesSiteBehindAccessPolicy(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("static home"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "user-1", Email: "ana@example.com", EmailVerified: true}}
	handler, err := New(
		[]config.Route{{
			Host:   "site.localhost",
			Root:   site,
			Access: &config.AccessPolicy{AllowedDomains: []string{"example.com"}},
		}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://site.localhost/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(); rec.Code != http.StatusOK || rec.Body.String() != "static home" {
		t.Fatalf("allowed = %d %q, want 200 static home", rec.Code, rec.Body.String())
	}

	gate.principal.Email = "mallory@other.com"
	if rec := get(); rec.Code != http.StatusForbidden {
		t.Fatalf("denied = %d, want 403", rec.Code)
	}
}

func TestNewRejectsMissingStaticRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := New([]config.Route{{Host: "site.localhost", Root: missing}}, nil, nil); err == nil {
		t.Fatal("New accepted a static root that does not exist")
	}
}

type fakeSites map[string]string

func (f fakeSites) Live(site string) (pages.Live, bool) {
	dir, ok := f[site]
	return pages.Live{Dir: dir}, ok
}

func TestSiteRouteServesTheLiveDeployment(t *testing.T) {
	live := t.TempDir()
	if err := os.WriteFile(filepath.Join(live, "index.html"), []byte("deployed"), 0o644); err != nil {
		t.Fatal(err)
	}
	routes := []config.Route{{Host: "blog.localhost", Site: "blog"}, {Host: "empty.localhost", Site: "empty"}}
	if _, err := New(routes, nil, nil); err == nil {
		t.Fatal("New accepted site routes without a deployment store")
	}
	handler, err := New(routes, nil, nil, WithSites(fakeSites{"blog": live}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for host, want := range map[string]int{"blog.localhost": http.StatusOK, "empty.localhost": http.StatusServiceUnavailable} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
		if rec.Code != want {
			t.Fatalf("%s: status %d, want %d", host, rec.Code, want)
		}
	}
}

func TestStripCookie(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"keeps the other cookies", "sb_session=secret; theme=dark; lang=es", "theme=dark; lang=es"},
		{"removes the only cookie", "sb_session=secret", ""},
		{"removes the first pair", "sb_session=secret; a=1", "a=1"},
		{"removes the last pair", "a=1; sb_session=secret", "a=1"},
		{"removes a middle pair", "a=1; sb_session=secret; b=2", "a=1; b=2"},
		{"leaves an unrelated header alone", "a=1; b=2", "a=1; b=2"},
		{"empty header", "", ""},
		{"tolerates spaces", "  a=1 ;  sb_session=secret ; b=2 ", "a=1; b=2"},
		{"keeps values containing equals", "token=a=b; sb_session=secret", "token=a=b"},
		{"matches the name exactly", "sb_session_extra=1; x=y", "sb_session_extra=1; x=y"},
		{"drops a valueless target pair", "sb_session; a=1", "a=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripCookie(tc.header, "sb_session"); got != tc.want {
				t.Fatalf("stripCookie(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func TestStripAccessCookies(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"sb_handoff_one=a; sb_handoff_two=b; app=1", "app=1"},
		{"sb_handoff=old; sb_session=edge; sb_handoff_new=b", ""},
		{" sb_handoff_one =a ; app_session=a=b ; sb_session=x", "app_session=a=b"},
		{"sb_handoffapp=1; sb_session_extra=2; theme=dark", "sb_handoffapp=1; sb_session_extra=2; theme=dark"},
	} {
		if got := stripAccessCookies(tc.header); got != tc.want {
			t.Fatalf("cookie filtering = %q, want %q", got, tc.want)
		}
	}
}

func TestPublicRouteStripsSessionCookie(t *testing.T) {
	session := auth.SessionCookieName

	t.Run("keeps the other cookies", func(t *testing.T) {
		var gotCookie string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCookie = r.Header.Get("Cookie")
			w.WriteHeader(http.StatusNoContent)
		}))
		defer upstream.Close()

		handler, err := New([]config.Route{{Host: "public.localhost", Target: upstream.URL}}, nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "http://public.localhost/", nil)
		req.Host = "public.localhost"
		req.Header.Set("Cookie", session+"=edge-session; sb_handoff=legacy-binding; sb_handoff_transaction=binding; theme=dark")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if gotCookie != "theme=dark" {
			t.Fatalf("upstream Cookie = %q, want %q", gotCookie, "theme=dark")
		}
		if strings.Contains(gotCookie, session) {
			t.Fatalf("upstream received the session cookie: %q", gotCookie)
		}
	})

	t.Run("removes the header when only the session is present", func(t *testing.T) {
		var gotCookie string
		upstreamGot := false
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstreamGot = true
			gotCookie = r.Header.Get("Cookie")
			w.WriteHeader(http.StatusNoContent)
		}))
		defer upstream.Close()

		handler, err := New([]config.Route{{Host: "public.localhost", Target: upstream.URL}}, nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "http://public.localhost/", nil)
		req.Host = "public.localhost"
		req.Header.Set("Cookie", session+"=edge-session")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent || !upstreamGot {
			t.Fatalf("status = %d upstream = %v, want 204 / reached", rec.Code, upstreamGot)
		}
		if gotCookie != "" {
			t.Fatalf("upstream Cookie = %q, want no header", gotCookie)
		}
	})
}

func sendRawRequest(t *testing.T, addr, host, path string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn,
		"GET %s HTTP/1.1\r\nHost: %s\r\nX_Auth_Request_Email: forged@attacker.example\r\nX_Auth_Request_User: forged-subject\r\nConnection: close\r\n\r\n",
		path, host)
	if err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	return string(resp)
}

func captureHeaders(t *testing.T) (*httptest.Server, chan http.Header) {
	t.Helper()
	got := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	return upstream, got
}

// identityHeaders returns every header in h whose name normalizes to a
// Switchboard identity header, as "Name=value" strings. The check repeats the
// dash/underscore normalization independently of the production helper so a
// broken strip cannot hide behind a broken detector.
func identityHeaders(h http.Header) []string {
	var found []string
	for name, values := range h {
		normalized := strings.ReplaceAll(name, "_", "-")
		if strings.EqualFold(normalized, pipeline.IdentityEmailHeader) || strings.EqualFold(normalized, pipeline.IdentityUserHeader) {
			found = append(found, name+"="+strings.Join(values, ","))
		}
	}
	sort.Strings(found)
	return found
}

func TestSpoofedIdentityHeadersOverRawSocket(t *testing.T) {
	t.Run("public route", func(t *testing.T) {
		upstream, got := captureHeaders(t)
		handler, err := New([]config.Route{{Host: "public.localhost", Target: upstream.URL}}, nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		edge := httptest.NewServer(handler)
		t.Cleanup(edge.Close)

		resp := sendRawRequest(t, strings.TrimPrefix(edge.URL, "http://"), "public.localhost", "/")
		if !strings.Contains(resp, "204") {
			t.Fatalf("response = %q, want 204", resp)
		}
		select {
		case headers := <-got:
			if found := identityHeaders(headers); len(found) != 0 {
				t.Fatalf("upstream identity headers = %q, want none", found)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("upstream was not reached")
		}
	})

	t.Run("protected route", func(t *testing.T) {
		upstream, got := captureHeaders(t)
		gate := &fakeGate{
			authenticated: true,
			principal: auth.Principal{
				Subject:       "verified-subject",
				Email:         "verified@example.com",
				EmailVerified: true,
			},
		}
		handler, err := New(
			[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true, Access: &config.AccessPolicy{}}},
			nil, gate,
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		edge := httptest.NewServer(handler)
		t.Cleanup(edge.Close)

		resp := sendRawRequest(t, strings.TrimPrefix(edge.URL, "http://"), "private.localhost", "/")
		if !strings.Contains(resp, "204") {
			t.Fatalf("response = %q, want 204", resp)
		}
		select {
		case headers := <-got:
			want := []string{
				pipeline.IdentityEmailHeader + "=verified@example.com",
				pipeline.IdentityUserHeader + "=verified-subject",
			}
			sort.Strings(want)
			if found := identityHeaders(headers); !slices.Equal(found, want) {
				t.Fatalf("upstream identity headers = %q, want %q", found, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("upstream was not reached")
		}
	})
}

// fakeCookieGate authenticates only when the session cookie is readable: it
// catches the ordering mistake of stripping the cookie before identify runs.
type fakeCookieGate struct {
	authenticated bool
	principal     auth.Principal
}

func (f *fakeCookieGate) Authenticate(r *http.Request) (auth.Principal, bool) {
	if _, err := r.Cookie(auth.SessionCookieName); err != nil {
		return auth.Principal{}, false
	}
	return f.principal, f.authenticated
}

func (f *fakeCookieGate) RedirectToLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/_/auth/login", http.StatusFound)
}

func TestSessionCookieIsVisibleToTheGateAndNotToTheUpstream(t *testing.T) {
	var gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	gate := &fakeCookieGate{
		authenticated: true,
		principal:     auth.Principal{Subject: "user-1", Email: "ana@example.com", EmailVerified: true},
	}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true, Access: &config.AccessPolicy{}}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	req.Header.Set("Cookie", auth.SessionCookieName+"=edge-session; app=1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: the gate must read the session cookie before the upstream loses it", rec.Code)
	}
	if gotCookie != "app=1" {
		t.Fatalf("upstream Cookie = %q, want app=1 (session stripped, other cookies kept)", gotCookie)
	}
}

type upstreamObservation struct {
	header http.Header
	host   string
}

func TestReverseProxySetsForwardedHeadersFromTheEdge(t *testing.T) {
	seen := make(chan upstreamObservation, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- upstreamObservation{header: r.Header.Clone(), host: r.Host}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler, err := New([]config.Route{{Host: "public.localhost", Target: upstream.URL}}, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name      string
		scheme    string
		wantProto string
	}{
		{"plain http", "http", "http"},
		{"tls", "https", "https"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.scheme+"://public.localhost/", nil)
			req.Host = "public.localhost"
			req.RemoteAddr = "203.0.113.7:5678"
			req.Header.Set("X-Forwarded-For", "6.6.6.6")
			req.Header.Set("X-Forwarded-Host", "evil.example")
			req.Header.Set("X-Forwarded-Proto", "https")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}

			got := <-seen
			if got.header.Get("X-Forwarded-For") != "203.0.113.7" {
				t.Errorf("X-Forwarded-For = %q, want the client IP only (client chain replaced)", got.header.Get("X-Forwarded-For"))
			}
			if got.header.Get("X-Forwarded-Host") != "public.localhost" {
				t.Errorf("X-Forwarded-Host = %q, want public.localhost", got.header.Get("X-Forwarded-Host"))
			}
			if got.header.Get("X-Forwarded-Proto") != tc.wantProto {
				t.Errorf("X-Forwarded-Proto = %q, want %q", got.header.Get("X-Forwarded-Proto"), tc.wantProto)
			}
			if got.host != "public.localhost" {
				t.Errorf("upstream Host = %q, want the public host preserved", got.host)
			}
		})
	}
}

func TestProtectedRouteStripsSessionCookie(t *testing.T) {
	var gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	gate := &fakeGate{
		authenticated: true,
		principal: auth.Principal{
			Subject:       "user-123",
			Email:         "alice@example.com",
			EmailVerified: true,
		},
	}
	handler, err := New(
		[]config.Route{{Host: "private.localhost", Target: upstream.URL, AuthRequired: true, Access: &config.AccessPolicy{}}},
		nil, gate,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://private.localhost/", nil)
	req.Host = "private.localhost"
	req.Header.Set("Cookie", auth.SessionCookieName+"=edge-session; app_session=upstream-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if gotCookie != "app_session=upstream-token" {
		t.Fatalf("upstream Cookie = %q, want %q", gotCookie, "app_session=upstream-token")
	}
}

// An upstream that accepts the connection and never answers must not hold the
// visitor's request (and its goroutine) forever.
func TestSilentUpstreamTimesOutWith504(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release) // runs first, so Close is not stuck waiting for the handler

	handler, err := New([]config.Route{{Host: "slow.localhost", Target: upstream.URL}}, nil, nil,
		WithResponseHeaderTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://slow.localhost/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		done <- rec
	}()
	select {
	case rec := <-done:
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, want 504", rec.Code)
		}
		if strings.Contains(rec.Body.String(), upstream.Listener.Addr().String()) {
			t.Fatalf("504 body leaks the upstream address: %q", rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy is still waiting for a silent upstream")
	}
}

// Routes may point at internal hosts: an HTTP_PROXY variable in the process
// environment must never reroute that traffic.
func TestUpstreamTransportIsBounded(t *testing.T) {
	tr := NewUpstreamTransport(7 * time.Second)
	if tr.Proxy != nil {
		t.Error("upstream transport must not honor proxy environment variables")
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 7s", tr.ResponseHeaderTimeout)
	}
	if tr.DialContext == nil || tr.TLSHandshakeTimeout <= 0 || tr.IdleConnTimeout <= 0 {
		t.Error("dial, TLS handshake and idle connection must all be bounded")
	}
}

func TestForwardedRequestIsLoggedAsStructuredRecord(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	var out bytes.Buffer
	handler, err := New([]config.Route{{Host: "app.localhost", Target: upstream.URL}}, nil, nil,
		WithLogger(slog.New(slog.NewJSONHandler(&out, nil))))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/a/b?token=secret", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	line := out.String()
	for _, want := range []string{`"msg":"proxy.forward"`, `"host":"app.localhost"`, `"path":"/a/b"`, `"method":"GET"`} {
		if !strings.Contains(line, want) {
			t.Errorf("log record missing %s: %s", want, line)
		}
	}
	if strings.Contains(line, "secret") {
		t.Errorf("the query string must never be logged: %s", line)
	}
}

func TestForwardRecordCarriesTheResolvedClientIP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	cloudflare := []netip.Prefix{netip.MustParsePrefix("173.245.48.0/20"), netip.MustParsePrefix("2400:cb00::/32")}
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string][]string
		want       string
	}{
		{"trusted proxy asserts the visitor", "173.245.48.10:443", map[string][]string{"Cf-Connecting-Ip": {"190.104.190.61"}}, "190.104.190.61"},
		{"trusted IPv6 proxy asserts an IPv6 visitor", "[2400:cb00::1]:443", map[string][]string{"Cf-Connecting-Ip": {"2001:db8::7"}}, "2001:db8::7"},
		{"untrusted peer cannot choose its own address", "203.0.113.9:5555", map[string][]string{"Cf-Connecting-Ip": {"1.2.3.4"}}, "203.0.113.9"},
		{"forwarding headers never identify the visitor", "173.245.48.10:443", map[string][]string{"X-Forwarded-For": {"1.2.3.4"}}, "173.245.48.10"},
		{"duplicated assertion falls back to the peer", "173.245.48.10:443", map[string][]string{"Cf-Connecting-Ip": {"1.1.1.1", "2.2.2.2"}}, "173.245.48.10"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			handler, err := New([]config.Route{{Host: "app.localhost", Target: upstream.URL}}, nil, nil,
				WithTrustedProxies(cloudflare),
				WithLogger(slog.New(slog.NewJSONHandler(&out, nil))))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
			req.RemoteAddr = tc.remoteAddr
			maps.Copy(req.Header, tc.headers)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			var record struct {
				ClientIP string `json:"client_ip"`
			}
			if err := json.Unmarshal(out.Bytes(), &record); err != nil {
				t.Fatalf("log record is not JSON: %v: %s", err, out.String())
			}
			if record.ClientIP != tc.want {
				t.Errorf("client_ip = %q, want %q: %s", record.ClientIP, tc.want, out.String())
			}
		})
	}
}

func TestVisitorAssertionIsSentOnlyWhenAskedAndNeverFromTheClient(t *testing.T) {
	var gotAssertion, gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAssertion = r.Header.Get(pipeline.VisitorIPHeader)
		gotXFF = r.Header.Get("X-Forwarded-For")
	}))
	defer upstream.Close()

	for name, tc := range map[string]struct {
		opts          []Option
		wantAssertion string
	}{
		"toward an application":       {nil, ""},
		"toward the tunnel connector": {[]Option{WithVisitorAssertion()}, "198.51.100.7"},
	} {
		t.Run(name, func(t *testing.T) {
			gotAssertion, gotXFF = "", ""
			handler, err := New([]config.Route{{Host: "app.localhost", Target: upstream.URL}}, nil, nil, tc.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
			req.RemoteAddr = "198.51.100.7:4242"
			req.Header.Set(pipeline.VisitorIPHeader, "203.0.113.99") // forged by the visitor
			req.Header.Set("X-Forwarded-For", "203.0.113.99")
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if gotAssertion != tc.wantAssertion {
				t.Errorf("assertion = %q, want %q", gotAssertion, tc.wantAssertion)
			}
			if gotXFF != "198.51.100.7" {
				t.Errorf("X-Forwarded-For = %q, want the real peer only", gotXFF)
			}
		})
	}
}

func TestAuthenticatedTunnelTrustFeedsTheApplicationTheVisitorAddress(t *testing.T) {
	var gotXFF, gotAssertion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotAssertion = r.Header.Get(pipeline.VisitorIPHeader)
	}))
	defer upstream.Close()
	handler, err := New([]config.Route{{Host: "app.localhost", Target: upstream.URL}}, nil, nil, WithAuthenticatedTunnelTrust())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://app.localhost/", nil)
	req.RemoteAddr = "10.8.0.1:51000" // the edge, as seen through the tunnel
	req.Header.Set(pipeline.VisitorIPHeader, "198.51.100.23")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if gotXFF != "198.51.100.23" {
		t.Errorf("application saw X-Forwarded-For %q, want the visitor", gotXFF)
	}
	if gotAssertion != "" {
		t.Errorf("the internal assertion header reached the application: %q", gotAssertion)
	}
}

// Step-up: an allowed visitor with an old login is sent to log in again and
// never reaches the upstream; a recent login passes; a visitor the policy
// refuses gets 403, not a passkey prompt for nothing.
func TestMaxAuthAgeAsksForAFreshLogin(t *testing.T) {
	reached := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))
	defer upstream.Close()
	policy := &config.AccessPolicy{AllowedDomains: []string{"example.com"}, MaxAuthAgeMinutes: 15}
	for name, tc := range map[string]struct {
		principal auth.Principal
		want      int
		redirect  bool
		reach     bool
	}{
		"stale login":   {auth.Principal{Subject: "ana", Email: "ana@example.com", EmailVerified: true, AuthTime: time.Now().Add(-time.Hour), Method: auth.MethodPasskey}, http.StatusFound, true, false},
		"recent login":  {auth.Principal{Subject: "ana", Email: "ana@example.com", EmailVerified: true, AuthTime: time.Now(), Method: auth.MethodPasskey}, http.StatusOK, false, true},
		"refused first": {auth.Principal{Subject: "eve", Email: "eve@other.com", EmailVerified: true, AuthTime: time.Now().Add(-time.Hour), Method: auth.MethodPasskey}, http.StatusForbidden, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			reached = 0
			gate := &fakeGate{authenticated: true, principal: tc.principal}
			recorder := &events.Recorder{}
			handler, err := New([]config.Route{{Host: "admin.localhost", Target: upstream.URL, Access: policy}}, nil, gate, WithEventSink(recorder))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://admin.localhost/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want || gate.redirected != tc.redirect || (reached > 0) != tc.reach {
				t.Fatalf("status %d redirected %v reached %d", rec.Code, gate.redirected, reached)
			}
			if stepUp, ok := recorder.Find(events.AccessStepUp); ok != tc.redirect || (ok && stepUp.Code != string(auth.CodeReauthRequired)) {
				t.Fatalf("access.step_up event = %+v, %v", stepUp, ok)
			}
		})
	}
}

type stepUpGate struct {
	fakeGate
	code    auth.DecisionCode
	methods []string
}

func (g *stepUpGate) RedirectToStepUp(w http.ResponseWriter, r *http.Request, code auth.DecisionCode, methods []string) {
	g.code, g.methods = code, methods
	http.Redirect(w, r, "/_/auth/login?reauth=device", http.StatusFound)
}

// A synced passkey on a device-bound route goes to a login that can say why;
// a device-bound one passes.
func TestRequireDeviceBoundUsesTheStepUpRedirect(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer upstream.Close()
	for _, deviceBound := range []bool{false, true} {
		reached = false
		gate := &stepUpGate{fakeGate: fakeGate{authenticated: true, principal: auth.Principal{Subject: "ana", AuthTime: time.Now(), DeviceBound: deviceBound, Method: auth.MethodPasskey}}}
		handler, err := New([]config.Route{{Host: "admin.localhost", Target: upstream.URL, Access: &config.AccessPolicy{RequireDeviceBound: true}}}, nil, gate)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.localhost/", nil))
		if deviceBound != reached || (!deviceBound && (rec.Code != http.StatusFound || gate.code != auth.CodeDeviceBoundRequired)) {
			t.Fatalf("device bound %v: status %d, code %q, reached %v", deviceBound, rec.Code, gate.code, reached)
		}
	}
}

// ID-007: a session from a method the route does not accept goes to a login
// that is told which methods the route takes.
func TestMethodsRestrictionNamesTheMethods(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	gate := &stepUpGate{fakeGate: fakeGate{authenticated: true, principal: auth.Principal{Subject: "g-1", AuthTime: time.Now(), Method: auth.MethodOIDC}}}
	handler, err := New([]config.Route{{Host: "admin.localhost", Target: upstream.URL, Access: &config.AccessPolicy{Methods: []string{"passkey"}}}}, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.localhost/", nil))
	if rec.Code != http.StatusFound || gate.code != auth.CodeMethodNotAllowed || !slices.Equal(gate.methods, []string{"passkey"}) {
		t.Fatalf("status %d, code %q, methods %v", rec.Code, gate.code, gate.methods)
	}
}

// An OIDC session on a step-up route is sent to a login that offers only
// passkeys, even though the route lists no methods: otherwise the page would
// offer the provider again and the visitor would loop.
func TestStepUpTellsTheLoginPasskeyOnly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	gate := &stepUpGate{fakeGate: fakeGate{authenticated: true, principal: auth.Principal{Subject: "g-1", AuthTime: time.Now(), Method: auth.MethodOIDC}}}
	handler, err := New([]config.Route{{Host: "admin.localhost", Target: upstream.URL, Access: &config.AccessPolicy{MaxAuthAgeMinutes: 15}}}, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://admin.localhost/", nil))
	if gate.code != auth.CodeMethodNotAllowed || !slices.Equal(gate.methods, []string{auth.MethodPasskey}) {
		t.Fatalf("code %q, methods %v", gate.code, gate.methods)
	}
}

func TestContextRuleStepUpIsAuditedAndRequiresPasskey(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer upstream.Close()
	gate := &stepUpGate{fakeGate: fakeGate{authenticated: true, principal: auth.Principal{Subject: "g-1", AuthTime: time.Now(), Method: auth.MethodOIDC}}}
	recorder := &events.Recorder{}
	policy := &config.AccessPolicy{ContextRule: &config.ContextRule{
		When: config.ContextConditions{SessionMethod: auth.MethodOIDC, ResourceSensitivity: "restricted"},
		Then: "step_up",
	}}
	route := config.Route{
		Host:   "admin.localhost",
		Target: upstream.URL,
		Access: policy,
		Resource: &config.RouteResource{
			Owner: "platform", DataClass: "account_data", Sensitivity: "restricted",
		},
	}
	handler, err := New([]config.Route{route}, nil, gate, WithEventSink(recorder))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.localhost/", nil))
	if rec.Code != http.StatusFound || reached || gate.code != auth.CodeContextStepUpRequired || !slices.Equal(gate.methods, []string{auth.MethodPasskey}) {
		t.Fatalf("status %d, upstream reached %v, step-up code %q methods %v", rec.Code, reached, gate.code, gate.methods)
	}
	stepUp, ok := recorder.Find(events.AccessStepUp)
	if !ok || stepUp.Code != string(auth.CodeContextStepUpRequired) || !strings.Contains(stepUp.Reason, "resource.sensitivity=\"restricted\"") {
		t.Fatalf("context step-up event = %+v, found %t", stepUp, ok)
	}
}

func TestNewPasskeyContextStepUpIsAuditedBeforeForward(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer upstream.Close()
	firstSeen := true
	gate := &stepUpGate{fakeGate: fakeGate{authenticated: true, principal: auth.Principal{
		Subject: "sb-new-device", AuthTime: time.Now(), Method: auth.MethodPasskey, DeviceFirstSeen: &firstSeen,
	}}}
	recorder := &events.Recorder{}
	route := config.Route{
		Host:   "admin.localhost",
		Target: upstream.URL,
		Access: &config.AccessPolicy{ContextRule: &config.ContextRule{
			When: config.ContextConditions{DeviceFirstSeen: &firstSeen},
			Then: "step_up",
		}},
	}
	handler, err := New([]config.Route{route}, nil, gate, WithEventSink(recorder))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.localhost/private", nil))
	if rec.Code != http.StatusFound || reached || gate.code != auth.CodeContextStepUpRequired || !slices.Equal(gate.methods, []string{auth.MethodPasskey}) {
		t.Fatalf("status %d, upstream reached %v, step-up code %q methods %v", rec.Code, reached, gate.code, gate.methods)
	}
	stepUp, ok := recorder.Find(events.AccessStepUp)
	if !ok || stepUp.Code != string(auth.CodeContextStepUpRequired) || !strings.Contains(stepUp.Reason, "history.device_first_seen=true") {
		t.Fatalf("new-passkey context step-up event = %+v, found %t", stepUp, ok)
	}
}

// deniedGate is a gate that can show a refused, signed-in person a page.
type deniedGate struct {
	fakeGate
	served []auth.DecisionCode
}

func (g *deniedGate) ServeDenied(w http.ResponseWriter, r *http.Request, p auth.Principal, code auth.DecisionCode) bool {
	g.served = append(g.served, code)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("<h1>You can't open this</h1>"))
	return true
}

// A refused person in a browser gets the gate's page; anything that is not a
// browser navigation keeps the plain 403 it can parse.
func TestARefusedBrowserGetsThePageAndAnAPIClientGetsPlainText(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("a refused request reached the upstream") }))
	defer upstream.Close()
	route := []config.Route{{Host: "grafana.localhost", Target: upstream.URL, Access: &config.AccessPolicy{AllowedEmails: []string{"ana@acme.dev"}}}}
	person := auth.Principal{Subject: "sb:1", Email: "bob@other.org", EmailVerified: true, AuthTime: time.Now()}
	for name, tc := range map[string]struct {
		method, accept string
		wantPage       bool
	}{
		"browser navigation": {http.MethodGet, "text/html,application/xhtml+xml,*/*;q=0.8", true},
		"browser head":       {http.MethodHead, "text/html", true},
		"fetch for json":     {http.MethodGet, "application/json", false},
		"curl":               {http.MethodGet, "*/*", false},
		"no accept":          {http.MethodGet, "", false},
		"a form post":        {http.MethodPost, "text/html", false},
	} {
		t.Run(name, func(t *testing.T) {
			gate := &deniedGate{fakeGate: fakeGate{authenticated: true, principal: person}}
			handler, err := New(route, nil, gate)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.method, "http://grafana.localhost/", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if got := len(gate.served) == 1; got != tc.wantPage {
				t.Fatalf("page served = %v, want %v (body %q)", got, tc.wantPage, rec.Body)
			}
			if !tc.wantPage && strings.TrimSpace(rec.Body.String()) != "access denied" {
				t.Fatalf("body = %q, want the plain text", rec.Body)
			}
			if tc.wantPage && gate.served[0] != auth.CodeEmailNotAllowed {
				t.Fatalf("the page was told %q", gate.served[0])
			}
		})
	}
}

// A gate that cannot render a page (the OIDC gate) keeps the plain 403 even
// for a browser.
func TestAGateWithoutAPageKeepsThePlain403(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	gate := &fakeGate{authenticated: true, principal: auth.Principal{Subject: "x", Email: "bob@other.org", EmailVerified: true}}
	handler, err := New([]config.Route{{Host: "grafana.localhost", Target: upstream.URL, Access: &config.AccessPolicy{AllowedEmails: []string{"ana@acme.dev"}}}}, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://grafana.localhost/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != "access denied" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}
