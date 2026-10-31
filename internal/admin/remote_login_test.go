package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

const loginHostOrigin = "https://auth.example.com"

// loginHostHandler is an identity handler whose config turns on
// admin.login_host with a passkey login on auth.example.com (ID-021).
func loginHostHandler(t *testing.T, on bool) (*Handler, *RemoteLogin, *events.Recorder) {
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
	cfg := config.Config{
		Admin: config.Admin{Token: "secret", LoginHost: on},
		Auth:  config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: loginHostOrigin}},
	}
	recorder := &events.Recorder{}
	remote := NewRemoteLogin()
	h := New(&fakeManager{cfg: cfg}, recorder, WithIdentities(sqlite.NewAdmins(db)), WithRemoteLogin(remote))
	return h, remote, recorder
}

// verifier is the console tab's secret and the challenge it sends.
func verifier() (string, string) {
	v := "tab-verifier-" + strings.Repeat("v", 32)
	sum := sha256.Sum256([]byte(v))
	return v, base64.RawURLEncoding.EncodeToString(sum[:])
}

// atLoginHost sends a request the login host's page would send.
func atLoginHost(rl *RemoteLogin, method, target, ceremony string, body any) (bool, *httptest.ResponseRecorder) {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, loginHostOrigin+target, reader)
	if method == http.MethodPost {
		req.Header.Set("Origin", loginHostOrigin)
		req.Header.Set("Content-Type", "application/json")
	}
	if ceremony != "" {
		req.Header.Set(adminCeremony, ceremony)
	}
	rec := httptest.NewRecorder()
	return rl.ServeAdminLogin(rec, req), rec
}

// loginHostCeremony runs steps 1–2: the console starts, the login host runs the
// ceremony with device, and the code comes back in the console URL's fragment.
func loginHostCeremony(t *testing.T, h *Handler, rl *RemoteLogin, device *virtualAuthenticator, start map[string]any) (code, redirect string) {
	t.Helper()
	rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", start)
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	target, _ := url.Parse(decode(t, rec)["url"].(string))
	if target.Scheme+"://"+target.Host != loginHostOrigin || target.Path != remotePath {
		t.Fatalf("start sent the browser to %s", target)
	}
	tx := target.Query().Get("tx")
	if handled, page := atLoginHost(rl, http.MethodGet, remotePath+"?tx="+tx, "", nil); !handled || page.Code != http.StatusOK ||
		!strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("page: handled %v, %d", handled, page.Code)
	}
	_, begun := atLoginHost(rl, http.MethodPost, remotePath+"/begin?tx="+tx, "", map[string]any{})
	if begun.Code != http.StatusOK {
		t.Fatalf("begin = %d %s", begun.Code, begun.Body)
	}
	started := decode(t, begun)
	options := started["options"].(map[string]any)
	var credential map[string]any
	if start["token"] != nil {
		credential = device.create(t, options, loginHostOrigin)
	} else {
		credential = device.get(t, options, loginHostOrigin)
	}
	_, finished := atLoginHost(rl, http.MethodPost, remotePath+"/finish?tx="+tx, started["ceremony"].(string), credential)
	if finished.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", finished.Code, finished.Body)
	}
	redirect = decode(t, finished)["redirect"].(string)
	prefix := consoleOrigin + "/overview#admin_code="
	if !strings.HasPrefix(redirect, prefix) {
		t.Fatalf("redirect = %q, want the console origin with the code in the fragment", redirect)
	}
	code, _ = url.QueryUnescape(strings.TrimPrefix(redirect, prefix))
	return code, redirect
}

// ID-021: the first administrator enrols on the login host (where Bitwarden
// and every other authenticator work), the console receives its session, and
// the same passkey signs in again by the same route.
func TestLoginHostEnrolsTheFirstAdministratorAndSignsIn(t *testing.T) {
	h, rl, recorder := loginHostHandler(t, true)
	if state := decode(t, request(h, http.MethodGet, "/_/api/admin/state", "", "")); state["login_host"] != loginHostOrigin {
		t.Fatalf("state = %v, want the login host announced", state)
	}
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	device := newAuthenticator(t)
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, h, rl, device, map[string]any{
		"challenge": challenge, "token": token, "name": "Octavio Santacruz", "email": "octavio@example.com", "hints": []string{"security-key", "bogus"},
	})
	rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem = %d %s", rec.Code, rec.Body)
	}
	session := decode(t, rec)
	if session["name"] != "Octavio Santacruz" || len(session["tools"].([]any)) != len(operatorTools) {
		t.Fatalf("session = %v", session)
	}
	if status := request(h, http.MethodGet, "/_/api/status", session["token"].(string), ""); status.Code != http.StatusOK {
		t.Fatalf("the session does not work: %d", status.Code)
	}
	if !recorder.Has(events.Type("admin.login_host_ceremony")) {
		t.Fatal("no event for the login-host ceremony")
	}

	secret, challenge = verifier()
	code, _ = loginHostCeremony(t, h, rl, device, map[string]any{"challenge": challenge})
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": secret}); rec.Code != http.StatusOK {
		t.Fatalf("sign-in through the login host = %d %s", rec.Code, rec.Body)
	}
}

// The code is worthless without the verifier of the tab that started, on
// another console origin, or a second time.
func TestLoginHostCodeNeedsTheVerifierAndIsUsedOnce(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	secret, challenge := verifier()
	start := map[string]any{"challenge": challenge, "token": token, "name": "Ana", "email": "ana@example.com"}

	code, _ := loginHostCeremony(t, h, rl, newAuthenticator(t), start)
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": "someone-else"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong verifier = %d", rec.Code)
	}
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": secret}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a code survived a failed attempt: %d", rec.Code)
	}

	token, _ = hostEnrolment(t, h, "recover", "ana@example.com")
	start["token"] = token
	code, _ = loginHostCeremony(t, h, rl, newAuthenticator(t), start)
	if rec := browser(h, "ozal.localhost:9092", "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": secret}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a code was redeemed on another console origin: %d", rec.Code)
	}
}

// Without a transaction started over loopback the login host serves nothing
// here, so the console's existence is not announced to the public.
func TestLoginHostWithoutTransactionIsAnUnknownPath(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, remotePath},
		{http.MethodGet, remotePath + "?tx=forged"},
		{http.MethodGet, remotePath + ".js"},
		{http.MethodPost, remotePath + "/begin?tx=forged"},
		{http.MethodPost, remotePath + "/finish?tx=forged"},
	} {
		if handled, _ := atLoginHost(rl, c.method, c.target, "", map[string]any{}); handled {
			t.Errorf("%s %s was answered without a transaction", c.method, c.target)
		}
	}
	// A transaction exists only for the request that names it.
	_, challenge := verifier()
	rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge})
	target, _ := url.Parse(decode(t, rec)["url"].(string))
	if handled, _ := atLoginHost(rl, http.MethodGet, remotePath+"?tx="+target.Query().Get("tx")+"x", "", nil); handled {
		t.Error("a near-miss transaction was answered")
	}
	// A POST from another origin is refused even with a live transaction.
	req := httptest.NewRequest(http.MethodPost, loginHostOrigin+remotePath+"/begin?"+target.RawQuery, strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	out := httptest.NewRecorder()
	if !rl.ServeAdminLogin(out, req) || out.Code != http.StatusForbidden {
		t.Fatalf("cross-origin begin = %d", out.Code)
	}
}

// Off by default: no transaction can start and the login host answers nothing.
func TestLoginHostIsOffUnlessConfigured(t *testing.T) {
	h, rl, _ := loginHostHandler(t, false)
	_, challenge := verifier()
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge}); rec.Code != http.StatusNotFound {
		t.Fatalf("start with admin.login_host off = %d", rec.Code)
	}
	if state := decode(t, request(h, http.MethodGet, "/_/api/admin/state", "", "")); state["login_host"] != nil {
		t.Fatalf("state announces a login host that is off: %v", state)
	}
	if handled, _ := atLoginHost(rl, http.MethodGet, remotePath+"?tx=x", "", nil); handled {
		t.Fatal("the login host answered with the feature off")
	}
}

// Only the private console page on a loopback name may start or redeem, so a
// ceremony can only return to the console and never to an outside host.
func TestLoginHostStartsAndEndsOnlyInTheConsole(t *testing.T) {
	h, _, _ := loginHostHandler(t, true)
	_, challenge := verifier()
	for _, host := range []string{"127.0.0.1:9091", "evil.example"} {
		// 421 is the edge's own refusal of a foreign Host (DNS rebinding).
		if rec := browser(h, host, "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge}); rec.Code != http.StatusForbidden && rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("start from %s = %d", host, rec.Code)
		}
	}
	raw, _ := json.Marshal(map[string]any{"challenge": challenge})
	req := httptest.NewRequest(http.MethodPost, "http://ozal.localhost:9091/_/api/admin/remote/start", bytes.NewReader(raw))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site start = %d", rec.Code)
	}
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", map[string]any{"challenge": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("start without a full challenge = %d", rec.Code)
	}
}

// A ceremony begun on the login host cannot be finished on the console's own
// endpoints, which would skip the code and the verifier.
func TestLoginHostCeremonyCannotFinishOnTheConsole(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	_, challenge := verifier()
	rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge, "token": token, "name": "Ana", "email": "ana@example.com"})
	target, _ := url.Parse(decode(t, rec)["url"].(string))
	_, begun := atLoginHost(rl, http.MethodPost, remotePath+"/begin?"+target.RawQuery, "", map[string]any{})
	started := decode(t, begun)
	credential := newAuthenticator(t).create(t, started["options"].(map[string]any), consoleOrigin)
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/enrol/finish", started["ceremony"].(string), credential); rec.Code == http.StatusOK {
		t.Fatal("a login-host ceremony opened a console session directly")
	}
}
