package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
)

// hostSignIn exchanges a host login over HTTP, as the console does, and returns
// the response so tests can read both the body and the resume cookie.
func hostSignIn(t *testing.T, handler *Handler) *httptest.ResponseRecorder {
	t.Helper()
	code, _, err := handler.operators.issue("Ana", true)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/_/api/operators/exchange", strings.NewReader(`{"token":"`+code+`"}`))
	req.Header.Set("Origin", "http://localhost")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body)
	}
	return rec
}

func resumeCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == resumeCookie {
			return c
		}
	}
	t.Fatal("no resume cookie set")
	return nil
}

func resume(handler *Handler, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+resumePath, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func newResumeHandler() *Handler {
	return New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "global"}}}, nil)
}

// The cookie must be unreadable by page script, never sent cross-site, and
// presented only to the resume endpoint.
func TestSignInSetsAConfinedResumeCookie(t *testing.T) {
	cookie := resumeCookieOf(t, hostSignIn(t, newResumeHandler()))
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != resumePath {
		t.Fatalf("cookie not confined: httponly=%v samesite=%v path=%q", cookie.HttpOnly, cookie.SameSite, cookie.Path)
	}
	if cookie.MaxAge <= 0 || cookie.MaxAge > int((15*time.Minute).Seconds()) {
		t.Fatalf("a host login cookie must not outlive its 15 minutes: max-age %d", cookie.MaxAge)
	}
}

func TestAReloadResumesTheSameSession(t *testing.T) {
	handler := newResumeHandler()
	signedIn := hostSignIn(t, handler)
	var first struct{ Token, ID string }
	if err := json.Unmarshal(signedIn.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	rec := resume(handler, "http://localhost", resumeCookieOf(t, signedIn))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("resume: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var got struct {
		Token, ID, Name string
		Passkey         bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Token != first.Token || got.ID != first.ID || got.Name != "Ana" || got.Passkey {
		t.Fatalf("resumed a different session: %+v", got)
	}
}

func TestResumeRefusesAnotherOriginEvenWithTheCookie(t *testing.T) {
	handler := newResumeHandler()
	cookie := resumeCookieOf(t, hostSignIn(t, handler))
	for _, origin := range []string{"", "http://evil.example", "https://localhost", "null"} {
		if rec := resume(handler, origin, cookie); rec.Code != 403 || strings.Contains(rec.Body.String(), cookie.Value) {
			t.Fatalf("origin %q: %d %s", origin, rec.Code, rec.Body)
		}
	}
}

func TestResumeWithoutACookieIsRefused(t *testing.T) {
	if rec := resume(newResumeHandler(), "http://localhost", nil); rec.Code != 401 {
		t.Fatalf("resume without cookie: %d", rec.Code)
	}
}

func TestSignOutEndsTheResumableSession(t *testing.T) {
	handler := newResumeHandler()
	signedIn := hostSignIn(t, handler)
	cookie := resumeCookieOf(t, signedIn)
	logout := request(handler, http.MethodPost, "/_/api/operators/logout", cookie.Value, "")
	if logout.Code != 200 {
		t.Fatalf("logout: %d", logout.Code)
	}
	if cleared := resumeCookieOf(t, logout); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("logout must delete the cookie: %+v", cleared)
	}
	rec := resume(handler, "http://localhost", cookie)
	if rec.Code != 401 {
		t.Fatalf("a signed-out session was resumed: %d", rec.Code)
	}
	if cleared := resumeCookieOf(t, rec); cleared.MaxAge >= 0 {
		t.Fatal("a dead cookie must be deleted when it is presented")
	}
}

func TestResumeRefusesAnExpiredOrRevokedSession(t *testing.T) {
	handler := newResumeHandler()
	now := time.Now()
	handler.operators.now = func() time.Time { return now }
	signedIn := hostSignIn(t, handler)
	cookie := resumeCookieOf(t, signedIn)
	handler.operators.now = func() time.Time { return now.Add(16 * time.Minute) }
	if rec := resume(handler, "http://localhost", cookie); rec.Code != 401 {
		t.Fatalf("expired session resumed: %d", rec.Code)
	}

	handler.operators.now = func() time.Time { return now }
	signedIn = hostSignIn(t, handler)
	var body struct{ ID string }
	_ = json.Unmarshal(signedIn.Body.Bytes(), &body)
	handler.operators.revoke(body.ID)
	if rec := resume(handler, "http://localhost", resumeCookieOf(t, signedIn)); rec.Code != 401 {
		t.Fatalf("revoked session resumed: %d", rec.Code)
	}
}

// A reload is not activity: resuming must not push a sliding session's end.
func TestResumeDoesNotExtendAPasskeySession(t *testing.T) {
	handler := newResumeHandler()
	now := time.Now()
	handler.operators.now = func() time.Time { return now }
	token, grant, err := handler.operators.issueSliding("Ana", "people")
	if err != nil {
		t.Fatal(err)
	}
	handler.operators.bind(grant.ID, "account-1", []byte("credential"))
	handler.operators.now = func() time.Time { return now.Add(20 * time.Minute) }
	rec := resume(handler, "http://localhost", &http.Cookie{Name: resumeCookie, Value: token})
	var got struct {
		Expires   time.Time
		Renewable bool
		Passkey   bool
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body)
	}
	if !got.Expires.Equal(grant.Expires) || !got.Renewable || !got.Passkey {
		t.Fatalf("resume changed the session: %+v, issued expiry %v", got, grant.Expires)
	}
	handler.operators.now = func() time.Time { return now.Add(31 * time.Minute) }
	if rec := resume(handler, "http://localhost", &http.Cookie{Name: resumeCookie, Value: token}); rec.Code != 401 {
		t.Fatalf("idle passkey session resumed after 31 minutes: %d", rec.Code)
	}
}
