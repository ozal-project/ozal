package passkey_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

// Two applications on domains the central cookie (example.test) cannot
// reach, beside appHost, which it can.
const (
	docsHost = "docs.other.test"
	wikiHost = "wiki.another.test"
)

// jarBrowser keeps cookies the way a browser does (Domain, Path, host-only,
// Secure), so a cookie set for one host is never presented on another.
type jarBrowser struct {
	h   *harness
	jar *cookiejar.Jar
}

func (h *harness) jarBrowser(t *testing.T) *jarBrowser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &jarBrowser{h: h, jar: jar}
}

// signIn enrols a user on the login host and keeps the central cookie.
func (b *jarBrowser) signIn(t *testing.T, email string) auth.Principal {
	t.Helper()
	central := b.h.browser()
	if code := central.enrol(t, newAuthenticator(t), b.h.invite(t, email, false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	principal, ok := central.signedIn()
	if !ok {
		t.Fatal("central login did not authenticate")
	}
	b.keep(loginOrigin+"/", central.session)
	return principal
}

func (b *jarBrowser) keep(rawURL string, cookies ...*http.Cookie) {
	u, _ := url.Parse(rawURL)
	b.jar.SetCookies(u, cookies)
}

// get sends one request as the edge would handle it: /_/auth/ endpoints go
// to the gate; anything else is a protected route that answers "app" to an
// authenticated visitor and sends anyone else to sign in.
func (b *jarBrowser) get(t *testing.T, rawURL string) *httptest.ResponseRecorder {
	t.Helper()
	return b.do(t, http.MethodGet, rawURL)
}

func (b *jarBrowser) do(t *testing.T, method, rawURL string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, rawURL, nil)
	for _, c := range b.jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	if !b.h.gate.ServeAuthEndpoint(rec, req) {
		if _, ok := b.h.gate.Authenticate(req); ok {
			_, _ = rec.WriteString("app")
		} else {
			b.h.gate.RedirectToLogin(rec, req)
		}
	}
	b.jar.SetCookies(req.URL, rec.Result().Cookies())
	return rec
}

// follow requests rawURL and follows redirects until stop matches the next
// location or a page answers. It returns the last response and its URL.
func (b *jarBrowser) follow(t *testing.T, rawURL string, stop func(string) bool) (*httptest.ResponseRecorder, string) {
	t.Helper()
	for i := 0; i < 10; i++ {
		rec := b.get(t, rawURL)
		loc := rec.Header().Get("Location")
		if loc == "" || (stop != nil && stop(loc)) {
			return rec, rawURL
		}
		next, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		rawURL = next.ResolveReference(mustParse(t, loc)).String()
	}
	t.Fatal("too many redirects")
	return nil, ""
}

func mustParse(t *testing.T, raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (b *jarBrowser) authenticatedOn(host string) (auth.Principal, bool) {
	req := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	for _, c := range b.jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	return b.h.gate.Authenticate(req)
}

func (b *jarBrowser) cookie(rawURL, name string) *http.Cookie {
	for _, c := range b.jar.Cookies(mustParse(nil, rawURL)) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func isHandoff(loc string) bool { return strings.Contains(loc, "/_/auth/handoff?code=") }

// Two tabs can start before either callback completes. Cookie scope must
// isolate the transactions even when the callbacks return in reverse order.
func TestConcurrentHandoffsOnOneHost(t *testing.T) {
	h := newHarnessFor(t, docsHost)
	b := h.jarBrowser(t)
	b.signIn(t, "ana@example.com")
	first := b.get(t, "https://"+docsHost+"/first?q=1")
	second := b.get(t, "https://"+docsHost+"/second?q=2")
	one, two := first.Result().Cookies()[0], second.Result().Cookies()[0]
	if one.Name == two.Name || one.Value == two.Value {
		t.Fatal("parallel transactions share a binding cookie")
	}
	for _, binding := range []*http.Cookie{one, two} {
		if !strings.HasPrefix(binding.Name, "sb_handoff_") || binding.Path != "/_/auth/handoff" || binding.Domain != "" || !binding.Secure || !binding.HttpOnly || binding.SameSite != http.SameSiteLaxMode {
			t.Fatal("transaction cookie lost its scope or security flags")
		}
	}
	codeOne := b.get(t, first.Header().Get("Location")).Header().Get("Location")
	codeTwo := b.get(t, second.Header().Get("Location")).Header().Get("Location")
	for _, tc := range []struct{ code, path, binding string }{
		{codeTwo, "/second?q=2", two.Name}, {codeOne, "/first?q=1", one.Name},
	} {
		res := b.get(t, tc.code)
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != tc.path {
			t.Fatalf("parallel handoff to %s returned %d", tc.path, res.Code)
		}
		if b.cookie("https://"+docsHost+"/_/auth/handoff", tc.binding) != nil {
			t.Fatal("redeemed transaction left its binding cookie")
		}
		if tc.binding == two.Name && b.cookie("https://"+docsHost+"/_/auth/handoff", one.Name) == nil {
			t.Fatal("redeeming one transaction cleared the other tab's binding")
		}
	}
	if _, ok := b.authenticatedOn(docsHost); !ok {
		t.Fatal("parallel handoffs left the browser signed out")
	}
}

func TestFailedHandoffOnlyClearsItsOwnBinding(t *testing.T) {
	h := newHarnessFor(t, docsHost)
	b := h.jarBrowser(t)
	b.signIn(t, "ana@example.com")
	first := b.get(t, "https://"+docsHost+"/first")
	second := b.get(t, "https://"+docsHost+"/second")
	one, two := first.Result().Cookies()[0], second.Result().Cookies()[0]
	codeOne := b.get(t, first.Header().Get("Location")).Header().Get("Location")
	codeTwo := b.get(t, second.Header().Get("Location")).Header().Get("Location")
	one.Value = strings.Repeat("A", 43)
	b.keep("https://"+docsHost+"/_/auth/handoff", one)
	if res := b.get(t, codeOne); res.Code != http.StatusBadRequest {
		t.Fatalf("wrong binding returned %d", res.Code)
	}
	if b.cookie("https://"+docsHost+"/_/auth/handoff", one.Name) != nil {
		t.Fatal("failed redemption left its binding")
	}
	if b.cookie("https://"+docsHost+"/_/auth/handoff", two.Name) == nil {
		t.Fatal("failed redemption cleared another transaction's binding")
	}
	if res := b.get(t, codeTwo); res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/second" {
		t.Fatalf("other tab could not finish: %d", res.Code)
	}
}

// ID-015: a signed-in visitor opens a private app on another domain and
// lands where they were going with a host-only session; the central cookie
// never travels there.
func TestHandoffSignsIntoAnotherDomain(t *testing.T) {
	h := newHarnessFor(t, appHost, docsHost)
	b := h.jarBrowser(t)
	principal := b.signIn(t, "ana@example.com")

	first := b.get(t, "https://"+docsHost+"/reports?q=1")
	loc := first.Header().Get("Location")
	if first.Code != http.StatusFound || !strings.HasPrefix(loc, loginOrigin+"/_/auth/continue?tx=") {
		t.Fatalf("first visit = %d %q", first.Code, loc)
	}
	binding := first.Result().Cookies()[0]
	if binding.Name != auth.HandoffCookiePrefix+mustParse(t, loc).Query().Get("tx") || binding.Path != "/_/auth/handoff" || binding.Domain != "" || !binding.HttpOnly || !binding.Secure || binding.SameSite != http.SameSiteLaxMode {
		t.Fatalf("binding cookie = %+v", binding)
	}

	page, at := b.follow(t, "https://"+docsHost+"/reports?q=1", nil)
	if page.Body.String() != "app" || at != "https://"+docsHost+"/reports?q=1" {
		t.Fatalf("landed on %s with %d %q", at, page.Code, page.Body)
	}
	got, ok := b.authenticatedOn(docsHost)
	if !ok || got.Subject != principal.Subject || got.Email != "ana@example.com" {
		t.Fatalf("host session = %+v, %v", got, ok)
	}
	if !h.events.has(events.LoginSucceeded, "handoff") {
		t.Fatal("no login.succeeded handoff event")
	}
	central := b.cookie(loginOrigin+"/", auth.SessionCookieName)
	host := b.cookie("https://"+docsHost+"/", auth.SessionCookieName)
	if central == nil || host == nil || central.Value == host.Value {
		t.Fatal("the app host did not get a session of its own")
	}

	// Signed out centrally, the login host asks for a sign-in and comes back.
	fresh := h.jarBrowser(t)
	rec, _ := fresh.follow(t, "https://"+docsHost+"/", func(loc string) bool { return strings.Contains(loc, "/_/auth/login?") })
	want := loginOrigin + "/_/auth/login?return_to=" + url.QueryEscape(loginOrigin+"/_/auth/continue?tx=")
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, want) {
		t.Fatalf("signed-out continue = %q", loc)
	}
}

// ID-015: a code is worthless outside the browser that started its
// transaction, is consumed by the first attempt, and only opens its own host.
func TestHandoffRefusesStolenInjectedAndCrossHostCodes(t *testing.T) {
	h := newHarnessFor(t, appHost, docsHost, wikiHost)
	stopAtCode := func(b *jarBrowser, host string) (string, *http.Cookie) {
		begin := b.get(t, "https://"+host+"/")
		binding := begin.Result().Cookies()[0]
		rec, _ := b.follow(t, begin.Header().Get("Location"), isHandoff)
		return rec.Header().Get("Location"), binding
	}

	victim := h.jarBrowser(t)
	victim.signIn(t, "ana@example.com")
	stolen, _ := stopAtCode(victim, docsHost)

	// Stolen: another browser has no binding for this transaction, and its
	// attempt burns the code.
	thief := h.jarBrowser(t)
	if rec := thief.get(t, stolen); rec.Code != http.StatusBadRequest {
		t.Fatalf("stolen code = %d", rec.Code)
	}
	if !h.events.has(events.LoginFailed, "handoff_binding_missing") {
		t.Fatal("no event for a code without its binding")
	}
	if rec := victim.get(t, stolen); rec.Code != http.StatusBadRequest {
		t.Fatalf("a code was redeemed twice: %d", rec.Code)
	}
	if _, ok := victim.authenticatedOn(docsHost); ok {
		t.Fatal("a burnt code still signed in")
	}

	// Cross host: a code for docs, presented on wiki by the same browser.
	forDocs, forDocsBinding := stopAtCode(victim, docsHost)
	onWiki := strings.Replace(forDocs, docsHost, wikiHost, 1)
	victim.keep("https://"+wikiHost+"/_/auth/handoff", forDocsBinding)
	if rec := victim.get(t, onWiki); rec.Code != http.StatusBadRequest || !h.events.has(events.LoginFailed, "handoff_wrong_host") {
		t.Fatalf("cross-host code = %d", rec.Code)
	}

	// Login CSRF: the attacker signs in as themself and sends the victim
	// the resulting code; the victim's binding is not the attacker's.
	attacker := h.jarBrowser(t)
	attacker.signIn(t, "mallory@example.com")
	injected, _ := stopAtCode(attacker, docsHost)
	if rec := victim.get(t, injected); rec.Code != http.StatusBadRequest || !h.events.has(events.LoginFailed, "handoff_binding_missing") {
		t.Fatalf("injected code = %d", rec.Code)
	}
	if got, ok := victim.authenticatedOn(docsHost); ok {
		t.Fatalf("victim signed in as %s", got.Email)
	}
	// Knowing the transaction/cookie name is insufficient without its secret.
	another, wrongBinding := stopAtCode(attacker, docsHost)
	wrongBinding.Value = strings.Repeat("A", 43)
	victim.keep("https://"+docsHost+"/_/auth/handoff", wrongBinding)
	if rec := victim.get(t, another); rec.Code != http.StatusBadRequest || !h.events.has(events.LoginFailed, "handoff_binding_mismatch") {
		t.Fatalf("wrong binding = %d", rec.Code)
	}

	// Garbage and missing codes.
	for _, raw := range []string{"https://" + docsHost + "/_/auth/handoff", "https://" + docsHost + "/_/auth/handoff?code=nope"} {
		if rec := victim.get(t, raw); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", raw, rec.Code)
		}
	}
	// The continue endpoint lives on the login host only, and an unknown
	// transaction is refused.
	if rec := victim.get(t, "https://"+docsHost+"/_/auth/continue?tx=x"); rec.Code == http.StatusOK {
		t.Fatalf("continue on an app host = %d", rec.Code)
	}
	if rec := victim.get(t, loginOrigin+"/_/auth/continue?tx=unknown"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown transaction = %d", rec.Code)
	}
}

// ID-015: a session is valid only on its audience. A host session copied to
// another app or to a cookie_domain host is refused, and so is a central
// session presented on an app's own host.
func TestSessionsAreValidOnlyOnTheirAudience(t *testing.T) {
	h := newHarnessFor(t, appHost, docsHost, wikiHost)
	b := h.jarBrowser(t)
	b.signIn(t, "ana@example.com")
	b.follow(t, "https://"+docsHost+"/", nil)
	hostSession := b.cookie("https://"+docsHost+"/", auth.SessionCookieName)
	centralSession := b.cookie(loginOrigin+"/", auth.SessionCookieName)

	present := func(host string, c *http.Cookie) bool {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.Value})
		_, ok := h.gate.Authenticate(req)
		return ok
	}
	if !present(docsHost, hostSession) || !present(appHost, centralSession) {
		t.Fatal("sessions refused on their own audience")
	}
	for _, tc := range []struct {
		host   string
		cookie *http.Cookie
	}{{wikiHost, hostSession}, {appHost, hostSession}, {"auth.example.test", hostSession}, {docsHost, centralSession}} {
		if present(tc.host, tc.cookie) {
			t.Fatalf("session accepted on %s outside its audience", tc.host)
		}
	}
}

// ID-015: ending the login ends the host sessions it approved, from either
// side, and so do the existing per-user revocations.
func TestHostSessionsEndWithTheLogin(t *testing.T) {
	signedInBoth := func(t *testing.T, h *harness, email string) *jarBrowser {
		b := h.jarBrowser(t)
		b.signIn(t, email)
		b.follow(t, "https://"+docsHost+"/", nil)
		b.follow(t, "https://"+wikiHost+"/", nil)
		if _, ok := b.authenticatedOn(docsHost); !ok {
			t.Fatal("no host session")
		}
		return b
	}
	gone := func(b *jarBrowser) bool {
		_, docs := b.authenticatedOn(docsHost)
		_, wiki := b.authenticatedOn(wikiHost)
		_, central := b.authenticatedOn("auth.example.test")
		return !docs && !wiki && !central
	}

	t.Run("central logout", func(t *testing.T) {
		h := newHarnessFor(t, docsHost, wikiHost)
		b := signedInBoth(t, h, "ana@example.com")
		if rec := b.do(t, http.MethodPost, loginOrigin+"/_/auth/logout"); rec.Code != http.StatusSeeOther {
			t.Fatalf("logout = %d", rec.Code)
		}
		if !gone(signedInWithOldCookies(b)) {
			t.Fatal("a host session outlived the central logout")
		}
	})
	t.Run("logout on an app", func(t *testing.T) {
		h := newHarnessFor(t, docsHost, wikiHost)
		b := signedInBoth(t, h, "ana@example.com")
		old := signedInWithOldCookies(b)
		rec := b.do(t, http.MethodPost, "https://"+docsHost+"/_/auth/logout")
		cleared := rec.Result().Cookies()[0]
		if rec.Code != http.StatusSeeOther || cleared.Domain != "" || cleared.MaxAge >= 0 {
			t.Fatalf("app logout = %d, cookie %+v", rec.Code, cleared)
		}
		if !gone(old) {
			t.Fatal("logging out on an app left the login or a sibling app signed in")
		}
	})
	t.Run("user disabled", func(t *testing.T) {
		h := newHarnessFor(t, docsHost, wikiHost)
		b := signedInBoth(t, h, "ana@example.com")
		if err := h.identity.SetDisabled(context.Background(), "ana@example.com", true); err != nil {
			t.Fatal(err)
		}
		if !gone(b) {
			t.Fatal("a disabled user kept a host session")
		}
	})
	t.Run("new central login", func(t *testing.T) {
		h := newHarnessFor(t, docsHost, wikiHost)
		b := signedInBoth(t, h, "ana@example.com")
		old := signedInWithOldCookies(b)
		sessions := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: time.Hour})
		sessions.Delete(context.Background(), b.cookie(loginOrigin+"/", auth.SessionCookieName).Value)
		if !gone(old) {
			t.Fatal("replacing the central session left its host sessions")
		}
	})
}

// signedInWithOldCookies snapshots b's cookies, so a check after a logout
// presents the values the browser had (a copied cookie), not the cleared
// ones.
func signedInWithOldCookies(b *jarBrowser) *jarBrowser {
	copied, _ := cookiejar.New(nil)
	for _, raw := range []string{loginOrigin + "/", "https://" + docsHost + "/", "https://" + wikiHost + "/"} {
		u := mustParse(nil, raw)
		var kept []*http.Cookie
		for _, c := range b.jar.Cookies(u) {
			kept = append(kept, &http.Cookie{Name: c.Name, Value: c.Value, Path: "/"})
		}
		copied.SetCookies(u, kept)
	}
	return &jarBrowser{h: b.h, jar: copied}
}

// ID-015: a step-up on an app host is approved only by a central login
// newer than its transaction; a plain visit is approved by the current one.
func TestHandoffStepUpNeedsANewerLogin(t *testing.T) {
	h := newHarnessFor(t, docsHost)
	b := h.jarBrowser(t)
	principal := b.signIn(t, "ana@example.com")
	// Replace the central session with one signed in an hour ago.
	principal.AuthTime = time.Now().Add(-time.Hour)
	old, err := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: 2 * time.Hour}).Create(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	b.keep(loginOrigin+"/", &http.Cookie{Name: auth.SessionCookieName, Value: old, Domain: "example.test", Path: "/", Secure: true, HttpOnly: true})

	req := httptest.NewRequest(http.MethodGet, "https://"+docsHost+"/admin", nil)
	rec := httptest.NewRecorder()
	h.gate.RedirectToStepUp(rec, req, auth.CodeDeviceBoundRequired, nil)
	b.jar.SetCookies(req.URL, rec.Result().Cookies())
	cont := b.get(t, rec.Header().Get("Location"))
	loc := cont.Header().Get("Location")
	if !strings.Contains(loc, "/_/auth/login?return_to=") || !strings.HasSuffix(loc, "&reauth=device") {
		t.Fatalf("an old login approved a step-up: %q", loc)
	}

	// A plain visit (no reason) is approved by the same old login.
	page, _ := b.follow(t, "https://"+docsHost+"/", nil)
	if page.Body.String() != "app" {
		t.Fatalf("plain visit = %d %q", page.Code, page.Header().Get("Location"))
	}
}

// ID-015: on an app's host the gate serves only the handoff and logout;
// the login pages redirect to the login host and its API is not there.
func TestHandoffHostServesNoCeremony(t *testing.T) {
	h := newHarnessFor(t, docsHost)
	b := h.jarBrowser(t)
	if rec := b.get(t, "https://"+docsHost+"/_/auth/login?x=1"); rec.Header().Get("Location") != loginOrigin+"/_/auth/login?x=1" {
		t.Fatalf("login page on an app host = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := b.do(t, http.MethodPost, "https://"+docsHost+"/_/auth/login/begin"); rec.Code != http.StatusNotFound {
		t.Fatalf("ceremony API on an app host = %d", rec.Code)
	}
	if rec := b.do(t, http.MethodPost, "https://"+docsHost+"/_/auth/handoff?code=x"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST handoff = %d", rec.Code)
	}
}
