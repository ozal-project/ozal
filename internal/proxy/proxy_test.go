package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/pipeline"
)

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
		req.Header.Set("Cookie", session+"=edge-session; theme=dark")
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
