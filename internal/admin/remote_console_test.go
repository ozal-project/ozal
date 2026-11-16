package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

const (
	remoteHost       = "console.example.com"
	testRemoteOrigin = "https://" + remoteHost
)

// remoteConsoleHandler is a login-host handler with admin.remote turned on,
// its remote entry, and an administrator enrolled on the login host.
type remoteFixture struct {
	h        *Handler
	rl       *RemoteLogin
	rc       *RemoteConsole
	device   *virtualAuthenticator
	recorder *events.Recorder
}

func newRemoteFixture(t *testing.T) remoteFixture {
	t.Helper()
	// The broker turns on the console app itself, as on a real edge.
	h, rl, recorder := loginHostHandler(t, true, WithEvents(events.NewBroker()))
	h.manager.(*fakeManager).cfg.Admin.Remote = &config.AdminRemote{Host: remoteHost}
	rc := NewRemoteConsole()
	WithRemoteConsole(rc)(h)
	rc.Configured(h.manager.Config())
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	device := newAuthenticator(t)
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, h, rl, device, map[string]any{
		"challenge": challenge, "token": token, "name": "Ana", "email": "ana@example.com",
	})
	redeem(t, h, code, secret)
	return remoteFixture{h: h, rl: rl, rc: rc, device: device, recorder: recorder}
}

// atRemote sends a request to the remote console host as its page would:
// HTTPS, with the console's Origin on anything that is not a read.
func atRemote(rc *RemoteConsole, method, path, session string, body any, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, testRemoteOrigin+path, reader)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", testRemoteOrigin)
		req.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		req.Header.Set("Authorization", "Bearer "+session)
	}
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	rc.ServeHTTP(rec, req)
	return rec
}

// signInRemotely runs the whole remote sign-in: start on the remote host, the
// passkey ceremony on the login host, and the code redeemed on the remote host.
func (f remoteFixture) signInRemotely(t *testing.T) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	secret, challenge := verifier()
	started := atRemote(f.rc, http.MethodPost, "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge})
	if started.Code != http.StatusOK {
		t.Fatalf("remote start = %d %s", started.Code, started.Body)
	}
	target, _ := url.Parse(decode(t, started)["url"].(string))
	tx := target.Query().Get("tx")
	_, begun := atLoginHost(f.rl, http.MethodPost, remotePath+"/begin?tx="+tx, "", map[string]any{})
	if begun.Code != http.StatusOK {
		t.Fatalf("begin = %d %s", begun.Code, begun.Body)
	}
	options := decode(t, begun)
	credential := f.device.get(t, options["options"].(map[string]any), loginHostOrigin)
	_, finished := atLoginHost(f.rl, http.MethodPost, remotePath+"/finish?tx="+tx, options["ceremony"].(string), credential)
	if finished.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", finished.Code, finished.Body)
	}
	redirect := decode(t, finished)["redirect"].(string)
	prefix := testRemoteOrigin + "/overview#admin_code="
	if !strings.HasPrefix(redirect, prefix) {
		t.Fatalf("the login host returned to %q, want the remote console", redirect)
	}
	code, _ := url.QueryUnescape(strings.TrimPrefix(redirect, prefix))
	rec := atRemote(f.rc, http.MethodPost, "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("remote finish = %d %s", rec.Code, rec.Body)
	}
	return decode(t, rec), rec
}

func TestRemoteConsoleSignsInWithAPasskeyOnTheLoginHost(t *testing.T) {
	f := newRemoteFixture(t)
	session, rec := f.signInRemotely(t)
	token := session["token"].(string)
	if status := atRemote(f.rc, http.MethodGet, "/_/api/status", token, nil); status.Code != http.StatusOK {
		t.Fatalf("the remote session does not work there: %d %s", status.Code, status.Body)
	}
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Fatal("the remote console must send HSTS")
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == remoteResumeCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("resume cookie = %+v, want __Host-, Secure, HttpOnly, Path=/, host-only, SameSite=Strict", cookie)
	}
	grant, ok := f.h.operators.authenticate(token)
	if !ok || !grant.Remote || !grant.loginHost {
		t.Fatalf("grant = %+v, want a remote login-host session", grant)
	}
	if idle := time.Until(grant.Expires); idle > remoteIdle+time.Second {
		t.Fatalf("remote session idles for %s, want at most %s", idle, remoteIdle)
	}
	signedIn := false
	for _, e := range f.recorder.Events() {
		if e.Type == events.Type("operator.signed_in") && strings.Contains(e.Reason, "remote console") {
			signedIn = true
		}
	}
	if !signedIn {
		t.Fatal("a remote sign-in must be in the journal as such")
	}
}

// Without a session opened on the remote host, the console's API does not
// exist there, whatever credential the request carries.
func TestRemoteConsoleWithoutSessionAnswersOnlyTheSignIn(t *testing.T) {
	f := newRemoteFixture(t)
	loopback := redeemLoopback(t, f)
	paths := []struct{ method, path string }{
		{http.MethodGet, "/_/api/status"}, {http.MethodGet, "/_/status"}, {http.MethodGet, "/_/events"},
		{http.MethodGet, "/_/overview"}, {http.MethodGet, "/_/api/admins"}, {http.MethodGet, "/_/api/access"},
		{http.MethodGet, "/_/api/operators/session"}, {http.MethodGet, "/_/healthz"}, {http.MethodGet, "/_/api/zones"},
		{http.MethodPost, "/_/api/apps"}, {http.MethodPost, "/routes"}, {http.MethodPost, "/_/reload"},
		{http.MethodPost, "/_/api/admins/invite"}, {http.MethodPost, "/_/api/admin/devices"},
		{http.MethodPost, "/_/api/operators/renew"}, {http.MethodPost, "/_/api/operators/logout"},
		{http.MethodDelete, "/routes/app.example.com"}, {http.MethodPut, "/_/api/github"},
	}
	for _, credential := range []struct{ name, token string }{{"nothing", ""}, {"admin token", "secret"}, {"loopback passkey session", loopback}} {
		for _, p := range paths {
			if rec := atRemote(f.rc, p.method, p.path, credential.token, map[string]any{}); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s with %s = %d, want 404", p.method, p.path, credential.name, rec.Code)
			}
		}
	}
	if rec := atRemote(f.rc, http.MethodGet, "/", "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("console page = %d, CSP %q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
	state := decode(t, atRemote(f.rc, http.MethodGet, "/_/api/admin/state", "", nil))
	if state["remote"] != true || state["login_host"] != loginHostOrigin || state["enrol_command"] != nil || state["access_requests"] != nil {
		t.Fatalf("remote sign-in state = %v", state)
	}
}

// redeemLoopback signs the enrolled administrator in on the loopback console.
func redeemLoopback(t *testing.T, f remoteFixture) string {
	t.Helper()
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, f.h, f.rl, f.device, map[string]any{"challenge": challenge})
	return redeem(t, f.h, code, secret)["token"].(string)
}

// Even with a remote session, the endpoints that only make sense on the
// host do not answer, and host-only operations stay refused.
func TestRemoteConsoleKeepsHostOnlyOperationsOnTheHost(t *testing.T) {
	f := newRemoteFixture(t)
	session, _ := f.signInRemotely(t)
	token := session["token"].(string)
	for _, path := range []string{"/_/api/admin/enrol/info", "/_/api/admin/login/begin", "/_/api/operators/exchange", "/_/api/admin/requests", "/_/api/admin/enrolments"} {
		if rec := atRemote(f.rc, http.MethodPost, path, token, map[string]any{}); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404 on the remote host", path, rec.Code)
		}
	}
	for _, p := range []struct{ method, path string }{
		{http.MethodPost, "/_/api/operators"}, {http.MethodGet, "/_/api/operators"}, {http.MethodDelete, "/_/api/operators"},
		{http.MethodPut, "/_/api/github"},
	} {
		if rec := atRemote(f.rc, p.method, p.path, token, map[string]any{"name": "x"}); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403: host-only", p.method, p.path, rec.Code)
		}
	}
	// A link that creates a passkey is used on the host's console only.
	_, challenge := verifier()
	if rec := atRemote(f.rc, http.MethodPost, "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge, "token": strings.Repeat("t", 43)}); rec.Code != http.StatusForbidden {
		t.Fatalf("enrolment through the remote console = %d, want 403", rec.Code)
	}
}

// Every change must come from the console's own page; a sibling host under
// the same site, or a request with no Origin, is refused before any handler.
func TestRemoteConsoleRefusesChangesFromAnotherOrigin(t *testing.T) {
	f := newRemoteFixture(t)
	session, _ := f.signInRemotely(t)
	token := session["token"].(string)
	for name, origin := range map[string]string{"sibling": "https://app.example.com", "none": "", "http": "http://" + remoteHost, "null": "null"} {
		rec := atRemote(f.rc, http.MethodPost, "/_/api/operators/logout", token, map[string]any{}, func(r *http.Request) {
			if origin == "" {
				r.Header.Del("Origin")
			} else {
				r.Header.Set("Origin", origin)
			}
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s origin = %d, want 403", name, rec.Code)
		}
	}
	if _, ok := f.h.operators.authenticate(token); !ok {
		t.Fatal("a refused cross-origin logout ended the session")
	}
	if !f.recorder.Has(events.Type("admin.remote_denied")) {
		t.Fatal("a cross-origin change must be recorded")
	}
	if rec := atRemote(f.rc, http.MethodPost, "/_/api/operators/logout", token, map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("same-origin logout = %d", rec.Code)
	}
}

// Over plain HTTP nothing answers, not even the sign-in.
func TestRemoteConsoleRefusesPlainHTTP(t *testing.T) {
	f := newRemoteFixture(t)
	req := httptest.NewRequest(http.MethodGet, "http://"+remoteHost+"/_/api/admin/state", nil)
	rec := httptest.NewRecorder()
	f.rc.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain HTTP = %d, want 403", rec.Code)
	}
}

// A remote session works only on the remote host's page; it resumes there
// through its __Host- cookie, and a loopback session never resumes there.
func TestRemoteConsoleResumesOnlyItsOwnSessions(t *testing.T) {
	f := newRemoteFixture(t)
	session, _ := f.signInRemotely(t)
	token := session["token"].(string)
	resume := func(name, value string) *httptest.ResponseRecorder {
		return atRemote(f.rc, http.MethodPost, resumePath, "", nil, func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: name, Value: value})
		})
	}
	if rec := resume(remoteResumeCookie, token); rec.Code != http.StatusOK || decode(t, rec)["token"] != token {
		t.Fatalf("remote resume = %d", rec.Code)
	}
	loopback := redeemLoopback(t, f)
	if rec := resume(remoteResumeCookie, loopback); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a loopback session resumed on the remote host: %d", rec.Code)
	}
	if rec := resume(resumeCookie, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the loopback cookie name was read on the remote host: %d", rec.Code)
	}
}

// The kill switch: removing admin.remote makes the host unknown, and the
// sessions opened there end, so turning it on again does not revive them.
func TestRemoteConsoleKillSwitchEndsItsSessions(t *testing.T) {
	f := newRemoteFixture(t)
	session, _ := f.signInRemotely(t)
	token := session["token"].(string)
	loopback := redeemLoopback(t, f)

	manager := f.h.manager.(*fakeManager)
	manager.cfg.Admin.Remote = nil
	f.rc.Configured(manager.Config())
	if rec := atRemote(f.rc, http.MethodGet, "/", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("console page after removal = %d, want 404", rec.Code)
	}
	manager.cfg.Admin.Remote = &config.AdminRemote{Host: remoteHost}
	f.rc.Configured(manager.Config())
	if rec := atRemote(f.rc, http.MethodGet, "/_/api/status", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a session from before the kill switch still works: %d", rec.Code)
	}
	if _, ok := f.h.operators.authenticate(loopback); !ok {
		t.Fatal("the kill switch ended a loopback session")
	}
	if !f.recorder.Has(events.Type("admin.remote_console_closed")) {
		t.Fatal("closing the remote console must be recorded")
	}
}

// `ozal console revoke -all` ends every session, remote ones included.
func TestRevokeAllEndsRemoteSessions(t *testing.T) {
	f := newRemoteFixture(t)
	session, _ := f.signInRemotely(t)
	req := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:9090/_/api/operators", nil)
	req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || decode(t, rec)["revoked"].(float64) < 1 {
		t.Fatalf("revoke all = %d %s", rec.Code, rec.Body)
	}
	if got := atRemote(f.rc, http.MethodGet, "/_/api/status", session["token"].(string), nil); got.Code != http.StatusNotFound {
		t.Fatalf("remote session after revoke -all = %d", got.Code)
	}
}

// Confirming a sensitive action from the remote console is a fresh sign-in on
// the login host, never a ceremony on this host.
func TestRemoteConsoleStepUpGoesThroughTheLoginHost(t *testing.T) {
	f := newRemoteFixture(t)
	session, _ := f.signInRemotely(t)
	rec := atRemote(f.rc, http.MethodPost, "/_/api/admin/step-up/begin", session["token"].(string), map[string]any{})
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "login_host_required" {
		t.Fatalf("step-up on the remote console = %d %s", rec.Code, rec.Body)
	}
}
