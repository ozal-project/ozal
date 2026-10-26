package passkey_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/passkey"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

const (
	loginOrigin = "https://auth.example.test"
	appHost     = "app.example.test"
)

type recorder struct{ events []events.Event }

func (r *recorder) Emit(e events.Event) { r.events = append(r.events, e) }

func (r *recorder) has(kind events.Type, code string) bool {
	for _, e := range r.events {
		if e.Type == kind && e.Code == code {
			return true
		}
	}
	return false
}

type harness struct {
	db       *sql.DB
	gate     *passkey.Gate
	identity *sqlite.Identity
	events   *recorder
}

func newHarness(t testing.TB) *harness {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := sqlite.NewIdentity(db)
	cfg := config.Auth{
		CookieDomain: "example.test",
		Passkeys:     &config.Passkeys{RPID: "example.test", LoginURL: loginOrigin, DisplayName: "Switchboard"},
	}
	rec := &recorder{}
	gate, err := passkey.NewGate(cfg, []string{appHost}, identity, sqlite.NewSessions(db, auth.SessionLifetime(cfg)), rec)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{db: db, gate: gate, identity: identity, events: rec}
}

func (h *harness) invite(t *testing.T, email string, recover bool) string {
	t.Helper()
	inv, err := h.identity.Invite(context.Background(), email, "", []string{"ops"}, recover, 15*time.Minute)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	return inv.Token
}

// browser keeps cookies by name, as one browser profile would.
type browser struct {
	h       *harness
	cookies map[string]string
	origin  string       // the Origin header its fetches send
	session *http.Cookie // the last session cookie set, with its attributes
}

func (h *harness) browser() *browser {
	return &browser{h: h, cookies: map[string]string{}, origin: loginOrigin}
}

func (b *browser) post(t *testing.T, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, loginOrigin+path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.origin)
	for name, value := range b.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	if !b.h.gate.ServeAuthEndpoint(rec, req) {
		t.Fatalf("%s not handled", path)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge >= 0 {
			b.session = c
		}
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

// signedIn reports whether the browser's session cookie authenticates on
// the protected app host.
func (b *browser) signedIn() (auth.Principal, bool) {
	req := httptest.NewRequest(http.MethodGet, "https://"+appHost+"/", nil)
	if id, ok := b.cookies[auth.SessionCookieName]; ok {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: id})
	}
	return b.h.gate.Authenticate(req)
}

func (b *browser) enrol(t *testing.T, a *virtualAuthenticator, token string) int {
	t.Helper()
	code, _ := b.enrolTo(t, a, token, "")
	return code
}

func (b *browser) enrolTo(t *testing.T, a *virtualAuthenticator, token, returnTo string) (int, map[string]any) {
	t.Helper()
	rec, options := b.post(t, "/_/auth/enrol/begin", map[string]string{"token": token, "return_to": returnTo})
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	rec, result := b.post(t, "/_/auth/enrol/finish", a.create(t, options, loginOrigin))
	return rec.Code, result
}

func (b *browser) login(t *testing.T, a *virtualAuthenticator, returnTo, signedOrigin string) (int, map[string]any) {
	t.Helper()
	rec, options := b.post(t, "/_/auth/login/begin", map[string]any{"return_to": returnTo})
	if rec.Code != http.StatusOK {
		t.Fatalf("login begin: %d %s", rec.Code, rec.Body)
	}
	rec, result := b.post(t, "/_/auth/login/finish", a.get(t, options, signedOrigin))
	return rec.Code, result
}

func TestEnrolThenLoginOnAnotherBrowser(t *testing.T) {
	h := newHarness(t)
	device := newAuthenticator(t)
	device.backupEligible = true // a synced passkey

	first := h.browser()
	if code := first.enrol(t, device, h.invite(t, "Ana@Example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	// The session outlives the browser session (ID-005): Max-Age is the
	// absolute lifetime; the server enforces the idle limit.
	if c := first.session; c == nil || c.MaxAge != int((7*24*time.Hour)/time.Second) || !c.HttpOnly || !c.Secure {
		t.Fatalf("session cookie = %+v", c)
	}
	principal, ok := first.signedIn()
	if !ok || principal.Email != "ana@example.com" || !principal.EmailVerified || !strings.HasPrefix(principal.Subject, passkey.SubjectPrefix) {
		t.Fatalf("after enrolment: %+v, %v", principal, ok)
	}
	if principal.DeviceFirstSeen == nil || !*principal.DeviceFirstSeen {
		t.Fatalf("newly enrolled passkey was not marked first seen: %+v", principal.DeviceFirstSeen)
	}
	if len(principal.Groups) != 1 || principal.Groups[0] != "ops" {
		t.Fatalf("groups = %v", principal.Groups)
	}
	if !h.events.has(events.PasskeyRegistered, "synced") {
		t.Fatal("no passkey.registered event")
	}

	second := h.browser()
	code, result := second.login(t, device, "https://"+appHost+"/reports?q=1", loginOrigin)
	if code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	if result["redirect"] != "https://"+appHost+"/reports?q=1" {
		t.Fatalf("redirect = %v", result["redirect"])
	}
	if again, ok := second.signedIn(); !ok || again.Subject != principal.Subject || again.DeviceFirstSeen == nil || *again.DeviceFirstSeen {
		t.Fatalf("second browser: %+v, %v", again, ok)
	}
	if !h.events.has(events.LoginSucceeded, "passkey") {
		t.Fatal("no login.succeeded event")
	}
}

// The destination survives the invitation and the enrolment, validated
// like a login's: an allowed host is returned to, anything else is not, and
// without one the person lands on the portal (ID-012), never on a foreign site.
func TestEnrolmentReturnsToAValidatedDestination(t *testing.T) {
	portal := loginOrigin + "/_/auth/me"
	for name, tc := range map[string]struct {
		returnTo, redirect string
		sites              bool
	}{
		"protected host":    {"https://" + appHost + "/reports", "https://" + appHost + "/reports", false},
		"other site":        {"https://evil.example/", portal, true},
		"scheme trick":      {"javascript:alert(1)", portal, true},
		"no destination":    {"", portal, true},
		"userinfo trick":    {"https://" + appHost + "@evil.example/", portal, true},
		"protocol-relative": {"//evil.example/", portal, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			code, result := h.browser().enrolTo(t, newAuthenticator(t), h.invite(t, "ana@example.com", false), tc.returnTo)
			if code != http.StatusOK {
				t.Fatalf("enrol = %d", code)
			}
			if result["redirect"] != tc.redirect {
				t.Fatalf("redirect = %v, want %q", result["redirect"], tc.redirect)
			}
			if sites, _ := result["sites"].([]any); (len(sites) == 1) != tc.sites {
				t.Fatalf("sites = %v, want links=%v", result["sites"], tc.sites)
			}
		})
	}
}

func TestInvitationIsSingleUseAndExpires(t *testing.T) {
	h := newHarness(t)
	token := h.invite(t, "ana@example.com", false)
	if code := h.browser().enrol(t, newAuthenticator(t), token); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	if code := h.browser().enrol(t, newAuthenticator(t), token); code != http.StatusBadRequest {
		t.Fatalf("reused link = %d, want 400", code)
	}

	expired, err := h.identity.Invite(context.Background(), "bob@example.com", "", nil, false, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code := h.browser().enrol(t, newAuthenticator(t), expired.Token); code != http.StatusBadRequest {
		t.Fatalf("expired link = %d, want 400", code)
	}
	// A second passkey needs an explicit recovery invitation.
	if _, err := h.identity.Invite(context.Background(), "ana@example.com", "", nil, false, time.Minute); err == nil {
		t.Fatal("second enrolment invitation issued without -recover")
	}
	if code := h.browser().enrol(t, newAuthenticator(t), h.invite(t, "ana@example.com", true)); code != http.StatusOK {
		t.Fatalf("recovery enrol = %d", code)
	}
}

func TestLoginRefusals(t *testing.T) {
	setup := func(t *testing.T) (*harness, *virtualAuthenticator) {
		h := newHarness(t)
		device := newAuthenticator(t)
		if code := h.browser().enrol(t, device, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
			t.Fatalf("enrol = %d", code)
		}
		return h, device
	}

	t.Run("look-alike origin (phishing)", func(t *testing.T) {
		h, device := setup(t)
		b := h.browser()
		if code, _ := b.login(t, device, "", "https://auth.example.test.evil.example"); code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", code)
		}
		if _, ok := b.signedIn(); ok {
			t.Fatal("signed in from another origin")
		}
	})

	t.Run("request from another site", func(t *testing.T) {
		h, _ := setup(t)
		b := h.browser()
		b.origin = "https://evil.example"
		if rec, _ := b.post(t, "/_/auth/login/begin", map[string]any{}); rec.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", rec.Code)
		}
	})

	t.Run("replayed response", func(t *testing.T) {
		h, device := setup(t)
		b := h.browser()
		rec, options := b.post(t, "/_/auth/login/begin", map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
		ceremony := b.cookies["sb_passkey"]
		response := device.get(t, options, loginOrigin)
		if rec, _ := b.post(t, "/_/auth/login/finish", response); rec.Code != http.StatusOK {
			t.Fatalf("first finish = %d", rec.Code)
		}
		attacker := h.browser()
		attacker.cookies["sb_passkey"] = ceremony
		if rec, _ := attacker.post(t, "/_/auth/login/finish", response); rec.Code != http.StatusBadRequest {
			t.Fatalf("replay = %d, want 400", rec.Code)
		}
		if _, ok := attacker.signedIn(); ok {
			t.Fatal("replay signed in")
		}
	})

	t.Run("no user verification", func(t *testing.T) {
		h, device := setup(t)
		device.userVerified = false
		if code, _ := h.browser().login(t, device, "", loginOrigin); code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", code)
		}
	})

	t.Run("cloned authenticator", func(t *testing.T) {
		h, device := setup(t)
		device.counter = 10
		if code, _ := h.browser().login(t, device, "", loginOrigin); code != http.StatusOK {
			t.Fatalf("first login = %d", code)
		}
		device.counter = 3 // a copy whose counter is behind
		if code, _ := h.browser().login(t, device, "", loginOrigin); code != http.StatusUnauthorized {
			t.Fatalf("clone = %d, want 401", code)
		}
		if !h.events.has(events.LoginFailed, "passkey_clone_warning") {
			t.Fatal("no clone warning event")
		}
	})

	t.Run("open redirect", func(t *testing.T) {
		h, device := setup(t)
		code, result := h.browser().login(t, device, "https://evil.example/", loginOrigin)
		// A foreign destination is dropped; the person lands on the portal of
		// the login host, never on the host they asked for.
		if code != http.StatusOK || result["redirect"] != loginOrigin+"/_/auth/me" {
			t.Fatalf("code = %d, redirect = %v; want 200 and the portal, not evil.example", code, result["redirect"])
		}
		if sites, _ := result["sites"].([]any); len(sites) != 1 || sites[0] != "https://"+appHost+"/" {
			t.Fatalf("sites = %v, want the protected host as an explicit link", result["sites"])
		}
	})
}

func TestRevocationAndDisablingEndSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	device := newAuthenticator(t)
	b := h.browser()
	if code := b.enrol(t, device, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}

	if err := h.identity.SetDisabled(ctx, "ana@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.signedIn(); ok {
		t.Fatal("disabled user still signed in")
	}
	if code, _ := h.browser().login(t, device, "", loginOrigin); code != http.StatusUnauthorized {
		t.Fatalf("disabled login = %d, want 401", code)
	}
	if err := h.identity.SetDisabled(ctx, "ana@example.com", false); err != nil {
		t.Fatal(err)
	}

	b2 := h.browser()
	if code, _ := b2.login(t, device, "", loginOrigin); code != http.StatusOK {
		t.Fatalf("re-enabled login = %d", code)
	}
	keys, err := h.identity.Passkeys(ctx, "ana@example.com")
	if err != nil || len(keys) != 1 {
		t.Fatalf("passkeys: %v %v", keys, err)
	}
	if err := h.identity.RevokePasskey(ctx, "ana@example.com", hexPrefix(keys[0].Credential.ID)); err != nil {
		t.Fatal(err)
	}
	if _, ok := b2.signedIn(); ok {
		t.Fatal("session survived passkey revocation")
	}
	if code, _ := h.browser().login(t, device, "", loginOrigin); code != http.StatusUnauthorized {
		t.Fatalf("revoked login = %d, want 401", code)
	}
}

// The per-request check stands on its own: a user disabled by any path,
// even one that left the session rows in place, is no longer signed in.
func TestDisabledUserIsRefusedEvenWithALiveSession(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	if code := b.enrol(t, newAuthenticator(t), h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	if _, err := h.db.Exec(`UPDATE users SET disabled_at = 1`); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.signedIn(); ok {
		t.Fatal("session of a disabled user still authenticates")
	}
}

func TestPagesAreLockedDown(t *testing.T) {
	h := newHarness(t)
	rec := httptest.NewRecorder()
	h.gate.ServeAuthEndpoint(rec, httptest.NewRequest(http.MethodGet, loginOrigin+"/_/auth/login", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Code != http.StatusOK || !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("login page: %d, CSP %q", rec.Code, csp)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("login page leaks referrers")
	}

	rec = httptest.NewRecorder()
	h.gate.ServeAuthEndpoint(rec, httptest.NewRequest(http.MethodGet, "https://"+appHost+"/_/auth/enrol", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != loginOrigin+"/_/auth/enrol" {
		t.Fatalf("page on app host: %d → %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	h.gate.RedirectToLogin(rec, httptest.NewRequest(http.MethodGet, "https://"+appHost+"/x?y=1", nil))
	if want := loginOrigin + "/_/auth/login?return_to=https%3A%2F%2Fapp.example.test%2Fx%3Fy%3D1"; rec.Header().Get("Location") != want {
		t.Fatalf("redirect to %q, want %q", rec.Header().Get("Location"), want)
	}
}

func hexPrefix(id []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 8)
	for _, b := range id[:4] {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}

// Step-up relies on two properties of the login: the session records when
// and how the identity was proven, and a new login replaces the session
// (the old cookie stops working), so a fresh proof can never be grafted
// onto an old session ID.
func TestLoginRecordsAuthTimeAndRotatesTheSession(t *testing.T) {
	h := newHarness(t)
	device := newAuthenticator(t)
	b := h.browser()
	if code := b.enrol(t, device, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	first, ok := b.signedIn()
	if !ok || first.Method != auth.MethodPasskey || time.Since(first.AuthTime) > time.Minute {
		t.Fatalf("after enrolment: method %q, auth time %v", first.Method, first.AuthTime)
	}
	oldID := b.cookies[auth.SessionCookieName]

	if code, _ := b.login(t, device, "https://"+appHost+"/admin", loginOrigin); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	if b.cookies[auth.SessionCookieName] == oldID {
		t.Fatal("a new login kept the old session ID")
	}
	if _, ok := b.signedIn(); !ok {
		t.Fatal("new session does not authenticate")
	}
	req := httptest.NewRequest(http.MethodGet, "https://"+appHost+"/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: oldID})
	if _, ok := h.gate.Authenticate(req); ok {
		t.Fatal("the replaced session still authenticates")
	}
}

// The login redirect says "step-up" only to a visitor who is signed in.
func TestRedirectToLoginMarksStepUp(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	if code := b.enrol(t, newAuthenticator(t), h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	redirect := func(withSession bool) string {
		req := httptest.NewRequest(http.MethodGet, "https://"+appHost+"/admin", nil)
		if withSession {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: b.cookies[auth.SessionCookieName]})
		}
		rec := httptest.NewRecorder()
		h.gate.RedirectToLogin(rec, req)
		return rec.Header().Get("Location")
	}
	if got := redirect(true); !strings.HasSuffix(got, "&reauth=1") {
		t.Fatalf("signed-in redirect = %q", got)
	}
	if got := redirect(false); strings.Contains(got, "reauth") {
		t.Fatalf("anonymous redirect = %q", got)
	}
}

// The session records whether the passkey behind it can be synced, from the
// authenticator's backup-eligible flag, and a device-bound step-up names
// itself in the login redirect.
func TestSessionRecordsDeviceBoundPasskeys(t *testing.T) {
	h := newHarness(t)
	for _, synced := range []bool{false, true} {
		device := newAuthenticator(t)
		device.backupEligible = synced
		b := h.browser()
		if code := b.enrol(t, device, h.invite(t, fmt.Sprintf("user-%v@example.com", synced), false)); code != http.StatusOK {
			t.Fatalf("enrol = %d", code)
		}
		if p, ok := b.signedIn(); !ok || p.DeviceBound == synced || p.UserVerified == nil || !*p.UserVerified || p.DeviceFirstSeen == nil || !*p.DeviceFirstSeen {
			t.Fatalf("synced %v: after enrolment session evidence = %+v", synced, p)
		}
		if code, _ := b.login(t, device, "", loginOrigin); code != http.StatusOK {
			t.Fatalf("login = %d", code)
		}
		if p, ok := b.signedIn(); !ok || p.DeviceBound == synced || p.UserVerified == nil || !*p.UserVerified || p.DeviceFirstSeen == nil || *p.DeviceFirstSeen {
			t.Fatalf("synced %v: after login session evidence = %+v", synced, p)
		}
	}
	rec := httptest.NewRecorder()
	h.gate.RedirectToStepUp(rec, httptest.NewRequest(http.MethodGet, "https://"+appHost+"/admin", nil), auth.CodeDeviceBoundRequired, nil)
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "&reauth=device") {
		t.Fatalf("Location = %q", loc)
	}
	contextStepUp := httptest.NewRecorder()
	h.gate.RedirectToStepUp(contextStepUp, httptest.NewRequest(http.MethodGet, "https://"+appHost+"/admin", nil), auth.CodeContextStepUpRequired, []string{auth.MethodOIDC})
	if loc := contextStepUp.Header().Get("Location"); !strings.HasSuffix(loc, "&reauth=method&methods=passkey") {
		t.Fatalf("context rule Location = %q", loc)
	}
}

// stubOIDC stands in for the upstream provider: its callback creates an OIDC
// session in the shared store, as auth.Gate does.
type stubOIDC struct {
	sessions auth.SessionBackend
	started  int
}

func (s *stubOIDC) HandleLogin(w http.ResponseWriter, r *http.Request) {
	s.started++
	http.Redirect(w, r, "https://idp.example/authorize", http.StatusFound)
}

func (s *stubOIDC) HandleCallback(w http.ResponseWriter, r *http.Request) {
	id, _ := s.sessions.Create(r.Context(), auth.Principal{Subject: auth.OIDCSubject("https://idp.example", "google-123"), Email: "ana@example.com", EmailVerified: true, AuthTime: time.Now(), Method: auth.MethodOIDC})
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: id})
}

// ID-007: with an upstream provider the gate routes its endpoints, accepts
// its sessions, shows its button, and never accepts a non-passkey session
// without one.
func TestPasskeyGateWithOIDC(t *testing.T) {
	h := newHarness(t)
	sessions := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: time.Hour})
	oidcSession, _ := sessions.Create(context.Background(), auth.Principal{Subject: auth.OIDCSubject("https://idp.example", "google-123"), AuthTime: time.Now(), Method: auth.MethodOIDC})
	request := func(method, path string) (*httptest.ResponseRecorder, bool) {
		req := httptest.NewRequest(method, loginOrigin+path, nil)
		rec := httptest.NewRecorder()
		return rec, h.gate.ServeAuthEndpoint(rec, req)
	}
	authenticated := func() bool {
		req := httptest.NewRequest(http.MethodGet, "https://"+appHost+"/", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: oidcSession})
		_, ok := h.gate.Authenticate(req)
		return ok
	}

	if _, handled := request(http.MethodGet, "/_/auth/oidc/start"); handled {
		t.Fatal("OIDC start handled without a provider")
	}
	if authenticated() {
		t.Fatal("an OIDC session authenticated without a provider configured")
	}
	if rec, _ := request(http.MethodGet, "/_/auth/login"); strings.Contains(rec.Body.String(), `data-oidc="G`) {
		t.Fatal("login page shows a provider that is not configured")
	}

	stub := &stubOIDC{sessions: sessions}
	h.gate.WithOIDC(stub, `Google <b>"`)
	if rec, handled := request(http.MethodGet, "/_/auth/oidc/start"); !handled || rec.Code != http.StatusFound || stub.started != 1 {
		t.Fatalf("OIDC start: handled %v, code %d", handled, rec.Code)
	}
	if rec, handled := request(http.MethodPost, "/_/auth/oidc/start"); !handled || rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST to OIDC start: %d", rec.Code)
	}
	if !authenticated() {
		t.Fatal("an OIDC session was refused with the provider configured")
	}
	legacy, _ := sessions.Create(context.Background(), auth.Principal{Subject: auth.OIDCSubject("https://idp.example", "google-123"), AuthTime: time.Now()})
	req := httptest.NewRequest(http.MethodGet, "https://"+appHost+"/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: legacy})
	if _, ok := h.gate.Authenticate(req); ok {
		t.Fatal("a session without a login method was accepted as OIDC")
	}
	rec, _ := request(http.MethodGet, "/_/auth/login")
	if !strings.Contains(rec.Body.String(), `data-oidc="Google &lt;b&gt;&#34;"`) {
		t.Fatalf("provider name not escaped into the page: %s", rec.Body.String()[:400])
	}

	stepUp := httptest.NewRecorder()
	h.gate.RedirectToStepUp(stepUp, httptest.NewRequest(http.MethodGet, "https://"+appHost+"/admin", nil), auth.CodeMethodNotAllowed, []string{"passkey"})
	if loc := stepUp.Header().Get("Location"); !strings.HasSuffix(loc, "&reauth=method&methods=passkey") {
		t.Fatalf("Location = %q", loc)
	}
}

// ID-008: a fresh passkey session lists, adds and removes its own passkeys;
// an old session, an OIDC session or no session cannot; the only passkey
// cannot be removed; a removal ends every session of the user.
func TestSelfServiceDevices(t *testing.T) {
	h := newHarness(t)
	phone := newAuthenticator(t)
	b := h.browser()
	if code := b.enrol(t, phone, h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	principal, _ := b.signedIn()
	sessions := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: time.Hour})
	cookieFor := func(p auth.Principal) *browser {
		other := h.browser()
		id, err := sessions.Create(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		other.cookies[auth.SessionCookieName] = id
		return other
	}
	devices := func(br *browser) []any {
		rec, body := br.post(t, "/_/auth/devices/list", map[string]any{})
		if rec.Code != http.StatusOK {
			return nil
		}
		list, _ := body["passkeys"].([]any)
		return list
	}

	if got := devices(b); len(got) != 1 {
		t.Fatalf("fresh session lists %v", got)
	}
	stale := principal
	stale.AuthTime = time.Now().Add(-10 * time.Minute)
	oidc := auth.Principal{Subject: "google-1", AuthTime: time.Now(), Method: auth.MethodOIDC}
	// A session from before migration 006: a passkey user, recent, but no
	// recorded method, so no proof that a passkey made it.
	noMethod := principal
	noMethod.Method = ""
	for name, br := range map[string]*browser{"stale": cookieFor(stale), "oidc": cookieFor(oidc), "no method": cookieFor(noMethod), "anonymous": h.browser()} {
		if rec, _ := br.post(t, "/_/auth/devices/list", map[string]any{}); rec.Code != http.StatusForbidden {
			t.Fatalf("%s session listed passkeys: %d", name, rec.Code)
		}
		if rec, _ := br.post(t, "/_/auth/devices/add/begin", map[string]any{}); rec.Code != http.StatusForbidden {
			t.Fatalf("%s session began adding a passkey: %d", name, rec.Code)
		}
	}

	// Only passkey: refused.
	only := devices(b)[0].(map[string]any)["id"].(string)
	if rec, _ := b.post(t, "/_/auth/devices/revoke", map[string]string{"id": only}); rec.Code != http.StatusConflict {
		t.Fatalf("revoking the only passkey = %d", rec.Code)
	}

	// Add a second device.
	laptop := newAuthenticator(t)
	rec, options := b.post(t, "/_/auth/devices/add/begin", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("add begin = %d %s", rec.Code, rec.Body)
	}
	if rec, _ := b.post(t, "/_/auth/devices/add/finish", laptop.create(t, options, loginOrigin)); rec.Code != http.StatusOK {
		t.Fatalf("add finish = %d %s", rec.Code, rec.Body)
	}
	if got := devices(b); len(got) != 2 {
		t.Fatalf("after adding: %v", got)
	}
	added := false
	for _, e := range h.events.events {
		added = added || (e.Type == events.PasskeyRegistered && strings.Contains(e.Reason, "signed-in session"))
	}
	if !added {
		t.Fatal("adding a passkey sent no passkey.registered alert")
	}
	firstLogin := h.browser()
	if code, _ := firstLogin.login(t, laptop, "", loginOrigin); code != http.StatusOK {
		t.Fatalf("login with the added passkey = %d", code)
	}
	if p, ok := firstLogin.signedIn(); !ok || p.DeviceFirstSeen == nil || !*p.DeviceFirstSeen {
		t.Fatalf("newly added passkey first login evidence = %+v, %v", p.DeviceFirstSeen, ok)
	}
	repeatLogin := h.browser()
	if code, _ := repeatLogin.login(t, laptop, "", loginOrigin); code != http.StatusOK {
		t.Fatalf("repeat login with the added passkey = %d", code)
	}
	if p, ok := repeatLogin.signedIn(); !ok || p.DeviceFirstSeen == nil || *p.DeviceFirstSeen {
		t.Fatalf("previously seen passkey evidence = %+v, %v", p.DeviceFirstSeen, ok)
	}

	// Someone else's passkey is not found.
	eve := h.browser()
	if code := eve.enrol(t, newAuthenticator(t), h.invite(t, "eve@example.com", false)); code != http.StatusOK {
		t.Fatal("enrol eve")
	}
	if rec, _ := eve.post(t, "/_/auth/devices/revoke", map[string]string{"id": only}); rec.Code != http.StatusNotFound {
		t.Fatalf("revoking another user's passkey = %d", rec.Code)
	}

	// Removing one ends every session of the user, this one included.
	other := cookieFor(principal)
	if rec, body := b.post(t, "/_/auth/devices/revoke", map[string]string{"id": only}); rec.Code != http.StatusOK || body["signed_out"] != true {
		t.Fatalf("revoke = %d %v", rec.Code, body)
	}
	if _, ok := b.signedIn(); ok {
		t.Fatal("the session that revoked is still signed in")
	}
	if _, ok := other.signedIn(); ok {
		t.Fatal("another session of the user survived the revocation")
	}
	if !h.events.has(events.PasskeyRevoked, "") {
		t.Fatal("no passkey.revoked event")
	}
	if code, _ := h.browser().login(t, phone, "", loginOrigin); code == http.StatusOK {
		t.Fatal("the revoked passkey still signs in")
	}
}

func TestDevicesPageNeedsAFreshPasskeySession(t *testing.T) {
	h := newHarness(t)
	b := h.browser()
	if code := b.enrol(t, newAuthenticator(t), h.invite(t, "ana@example.com", false)); code != http.StatusOK {
		t.Fatalf("enrol = %d", code)
	}
	principal, _ := b.signedIn()
	page := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, loginOrigin+"/_/auth/devices", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		h.gate.ServeAuthEndpoint(rec, req)
		return rec
	}
	if rec := page(b.cookies[auth.SessionCookieName]); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-page="devices"`) {
		t.Fatalf("fresh session: %d", rec.Code)
	}
	if rec := page(""); rec.Code != http.StatusFound || strings.Contains(rec.Header().Get("Location"), "reauth") {
		t.Fatalf("anonymous: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	stale := principal
	stale.AuthTime = time.Now().Add(-10 * time.Minute)
	id, _ := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: time.Hour}).Create(context.Background(), stale)
	if rec := page(id); rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "&reauth=1&methods=passkey") {
		t.Fatalf("stale session: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// Scripts and styles are linked by content hash and cacheable forever only
// at that exact URL; the page itself is never cached.
func TestAssetsAreVersionedAndCacheable(t *testing.T) {
	h := newHarness(t)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.gate.ServeAuthEndpoint(rec, httptest.NewRequest(http.MethodGet, loginOrigin+path, nil))
		return rec
	}
	page := get("/_/auth/login")
	if page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("page Cache-Control = %q", page.Header().Get("Cache-Control"))
	}
	m := regexp.MustCompile(`"/_/auth/passkey\.js\?v=([0-9a-f]{12})"`).FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("page does not link a versioned script")
	}
	if cc := get("/_/auth/passkey.js?v=" + m[1]).Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("current version Cache-Control = %q", cc)
	}
	for _, path := range []string{"/_/auth/passkey.js", "/_/auth/passkey.js?v=000000000000"} {
		if cc := get(path).Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s Cache-Control = %q", path, cc)
		}
	}
}

// Provider icons are served as SVG from their own directory and nothing else
// leaks out of it: not another asset, not a file that is not an icon.
func TestProviderIconsAreServed(t *testing.T) {
	h := newHarness(t)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		if !h.gate.ServeAuthEndpoint(rec, httptest.NewRequest(http.MethodGet, loginOrigin+path, nil)) {
			rec.Code = 0
		}
		return rec
	}
	icon := get("/_/auth/icons/apple.svg")
	if icon.Code != http.StatusOK || icon.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("apple.svg: %d %q", icon.Code, icon.Header().Get("Content-Type"))
	}
	if !strings.Contains(icon.Body.String(), "<svg") {
		t.Fatal("apple.svg is not an SVG")
	}
	for _, path := range []string{"/_/auth/icons/LICENSE.md", "/_/auth/icons/passkey.js", "/_/auth/icons/nope.svg"} {
		if rec := get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
	if rec := get("/_/auth/iconsx/apple.svg"); rec.Code != 0 {
		t.Errorf("a path outside /_/auth/icons/ was handled: %d", rec.Code)
	}
}

func (s *stubOIDC) AcceptsPrincipal(p auth.Principal) bool {
	return p.Method == auth.MethodOIDC && auth.IsOIDCSubject("https://idp.example", p.Subject)
}

// A concurrent login may have read membership just before a removal. Its new
// cookie must not keep that membership, even after the update deleted old rows.
func TestNativeSessionRefreshesAuthoritativeGroupsAndRejectsMethodConfusion(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	invited, err := h.identity.Invite(ctx, "ana@example.test", "", []string{"staff"}, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: time.Hour})
	for _, method := range []string{auth.MethodPasskey, auth.MethodOIDC, ""} {
		id, err := sessions.Create(ctx, auth.Principal{Subject: invited.User.Subject(), Groups: []string{"owner"}, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, loginOrigin+"/", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: id})
		principal, ok := h.gate.Authenticate(req)
		if method != auth.MethodPasskey {
			if ok {
				t.Fatalf("native subject with %q accepted", method)
			}
			continue
		}
		if !ok || len(principal.Groups) != 1 || principal.Groups[0] != "staff" {
			t.Fatalf("stale groups: %+v %v", principal, ok)
		}
	}
}
