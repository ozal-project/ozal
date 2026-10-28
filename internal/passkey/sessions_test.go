package passkey_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/auth"
	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

func TestSessionHTTPBoundaryEnforcesOriginAndOwner(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ana, err := h.identity.Invite(ctx, "ana@example.test", "", nil, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := h.identity.Invite(ctx, "bob@example.test", "", nil, false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewSessions(h.db, auth.Lifetime{Absolute: time.Hour})
	anaID, _ := store.Create(ctx, auth.Principal{Subject: ana.User.Subject(), Method: auth.MethodPasskey})
	bobID, _ := store.Create(ctx, auth.Principal{Subject: bob.User.Subject(), Method: auth.MethodPasskey})
	sum := sha256.Sum256([]byte(bobID))
	bobHandle := hex.EncodeToString(sum[:])
	call := func(origin, cookie, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, loginOrigin+path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		if !h.gate.ServeAuthEndpoint(rec, req) {
			t.Fatal("endpoint missing")
		}
		return rec
	}
	if rec := call(loginOrigin, "", "/_/auth/me/sessions", `{}`); rec.Code < 400 {
		t.Fatal("anonymous session listing")
	}
	if rec := call("https://evil.example", anaID, "/_/auth/me/sessions", `{}`); rec.Code != 403 {
		t.Fatalf("wrong Origin: %d", rec.Code)
	}
	if rec := call(loginOrigin, anaID, "/_/auth/me/sessions/revoke", `{"id":"`+bobHandle+`"}`); rec.Code != 404 {
		t.Fatalf("wrong owner: %d %s", rec.Code, rec.Body)
	}
	if _, ok := store.Get(ctx, bobID); !ok {
		t.Fatal("another person's session ended")
	}
	if rec := call(loginOrigin, anaID, "/_/auth/me/sessions", `{}`); rec.Code != 200 || strings.Contains(rec.Body.String(), anaID) || strings.Contains(rec.Body.String(), bobHandle) {
		t.Fatal("session listing leaked bearer or other subject")
	}
	if rec := call(loginOrigin, anaID, "/_/auth/me/sessions/revoke", `{"all":true}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"signed_out":true`) {
		t.Fatalf("sign out all: %d %s", rec.Code, rec.Body)
	}
}
