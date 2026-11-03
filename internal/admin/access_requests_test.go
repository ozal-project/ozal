package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/events"
)

const consoleHost = "ozal.localhost:9091"

func askForAccess(h *Handler, name, email string) *httptest.ResponseRecorder {
	return browser(h, consoleHost, "/_/api/admin/requests", "", map[string]string{"name": name, "email": email})
}

func pollRequest(h *Handler, id, secret string) *httptest.ResponseRecorder {
	return browser(h, consoleHost, "/_/api/admin/requests/status", "", map[string]string{"id": id, "secret": secret})
}

func switchRequests(t *testing.T, h *Handler, session string, on bool) {
	t.Helper()
	if rec := api(h, http.MethodPut, "/_/api/admins/requests/settings", session, map[string]bool{"enabled": on}); rec.Code != http.StatusOK {
		t.Fatalf("switch requests = %d %s", rec.Code, rec.Body)
	}
}

func pendingIDs(t *testing.T, h *Handler, session string) []map[string]any {
	t.Helper()
	rec := api(h, http.MethodGet, "/_/api/admins/requests", session, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list requests = %d %s", rec.Code, rec.Body)
	}
	var out []map[string]any
	for _, item := range decode(t, rec)["requests"].([]any) {
		out = append(out, item.(map[string]any))
	}
	return out
}

func TestAccessRequestsAreOffUntilAnAdministratorTurnsThemOn(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	if rec := askForAccess(h, "Ana Perez", "ana@example.com"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "requests_off") {
		t.Fatalf("request while off = %d %s, want 403 requests_off", rec.Code, rec.Body)
	}
	state := request(h, http.MethodGet, "/_/api/admin/state", "", "")
	if _, on := decode(t, state)["access_requests"]; on {
		t.Fatal("the sign-in page advertised requests that are off")
	}
	switchRequests(t, h, owner, true)
	if _, on := decode(t, request(h, http.MethodGet, "/_/api/admin/state", "", ""))["access_requests"]; !on {
		t.Fatal("the sign-in page does not show requests that are on")
	}
}

func TestApprovedRequestGivesTheChosenToolsToTheBrowserThatAsked(t *testing.T) {
	h, recorder := identityHandler(t)
	owner := ownerSession(t, h)
	switchRequests(t, h, owner, true)

	asked := askForAccess(h, "Ana Perez", "Ana@Example.com")
	if asked.Code != http.StatusCreated {
		t.Fatalf("request = %d %s", asked.Code, asked.Body)
	}
	made := decode(t, asked)
	id, secret := made["id"].(string), made["secret"].(string)

	waiting := pendingIDs(t, h, owner)
	if len(waiting) != 1 || waiting[0]["email"] != "ana@example.com" || waiting[0]["id"] != id {
		t.Fatalf("pending = %v", waiting)
	}
	if strings.Contains(asked.Body.String(), "token") || strings.Contains(request(h, http.MethodGet, "/_/api/admins/requests", owner, "").Body.String(), secret) {
		t.Fatal("a secret leaked into the list or a request carries a token")
	}
	if state := decode(t, pollRequest(h, id, secret))["state"]; state != "pending" {
		t.Fatalf("state before a decision = %v", state)
	}

	// Nothing is granted by asking: the requester cannot sign in.
	if rec := approve(h, owner, id, "people"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	if rec := pollRequest(h, id, "not-the-secret"); rec.Code != http.StatusNotFound {
		t.Fatalf("poll with a wrong secret = %d, want 404", rec.Code)
	}
	collected := decode(t, pollRequest(h, id, secret))
	token, _ := collected["token"].(string)
	if collected["state"] != "approved" || token == "" {
		t.Fatalf("approved poll = %v", collected)
	}
	if rec := pollRequest(h, id, secret); rec.Code != http.StatusNotFound {
		t.Fatal("the invitation was delivered twice")
	}

	joined := enrol(t, h, newAuthenticator(t), token, "", "")
	if joined.Code != http.StatusOK {
		t.Fatalf("enrol with the approved link = %d %s", joined.Code, joined.Body)
	}
	session := decode(t, joined)
	if tools := session["tools"].([]any); len(tools) != 1 || tools[0] != "people" {
		t.Fatalf("tools = %v, want only the people tool the approver chose", tools)
	}
	if session["name"] != "Ana Perez" {
		t.Fatalf("name = %v, want the name from the request", session["name"])
	}
	for _, want := range []string{"admin.access_requested", "admin.access_approved"} {
		if !recorder.Has(events.Type(want)) {
			t.Fatalf("no %s event", want)
		}
	}
}

func approve(h *Handler, session, id string, tools ...string) *httptest.ResponseRecorder {
	return api(h, http.MethodPost, "/_/api/admins/requests/"+id+"/approve", session, map[string]any{"tools": tools})
}

func TestDeniedRequestIsAnsweredOnceAndNeverGetsALink(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	switchRequests(t, h, owner, true)
	made := decode(t, askForAccess(h, "Eve", "eve@example.com"))
	id, secret := made["id"].(string), made["secret"].(string)
	if rec := api(h, http.MethodPost, "/_/api/admins/requests/"+id+"/deny", owner, nil); rec.Code != http.StatusOK {
		t.Fatalf("deny = %d %s", rec.Code, rec.Body)
	}
	if rec := approve(h, owner, id, "people"); rec.Code != http.StatusNotFound {
		t.Fatalf("approving a denied request = %d, want 404", rec.Code)
	}
	answer := decode(t, pollRequest(h, id, secret))
	if answer["state"] != "denied" || answer["token"] != nil {
		t.Fatalf("denied poll = %v", answer)
	}
	if len(pendingIDs(t, h, owner)) != 0 {
		t.Fatal("a denied request is still listed")
	}
}

func TestApprovalNeedsAFreshPasskeyAndAnAccountNotTheHostOrToken(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	switchRequests(t, h, owner, true)
	id := decode(t, askForAccess(h, "Ana", "ana@example.com"))["id"].(string)

	// The admin token is not an administrator account.
	if rec := approve(h, "secret", id, "people"); rec.Code != http.StatusForbidden {
		t.Fatalf("approve with the admin token = %d, want 403", rec.Code)
	}
	// The host socket is not one either.
	local := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9090/_/api/admins/requests/"+id+"/approve", strings.NewReader(`{"tools":["people"]}`))
	local = local.WithContext(context.WithValue(local.Context(), localKey{}, true))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, local)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("approve from the host socket = %d, want 403", rec.Code)
	}
	// A passkey proof older than five minutes is stale.
	h.operators.now = func() time.Time { return time.Now().Add(passkeyStepUpMaxAge + time.Minute) }
	stale := approve(h, owner, id, "people")
	h.operators.now = time.Now
	if stale.Code != http.StatusForbidden || !strings.Contains(stale.Body.String(), "passkey_required") {
		t.Fatalf("a stale passkey proof approved a request: %d %s", stale.Code, stale.Body)
	}
	if len(pendingIDs(t, h, owner)) != 1 {
		t.Fatal("a refused approval changed the request")
	}
}

func TestApproverCannotGrantMoreThanTheirOwnToolsOrApproveWithoutTheAdministratorsTool(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	invitation, rec := invite(t, h, owner, "mod@example.com", "administrators", "people")
	if invitation == "" {
		t.Fatalf("invite = %d %s", rec.Code, rec.Body)
	}
	mod := decode(t, enrol(t, h, newAuthenticator(t), invitation, "Mod", "mod@example.com"))["token"].(string)
	peopleOnly, _ := invite(t, h, owner, "p@example.com", "people")
	pOnly := decode(t, enrol(t, h, newAuthenticator(t), peopleOnly, "P", "p@example.com"))["token"].(string)

	switchRequests(t, h, owner, true)
	id := decode(t, askForAccess(h, "Ana", "ana@example.com"))["id"].(string)

	if rec := approve(h, mod, id, "applications"); rec.Code != http.StatusForbidden {
		t.Fatalf("granting a tool the approver lacks = %d, want 403", rec.Code)
	}
	if rec := approve(h, mod, id); rec.Code != http.StatusBadRequest {
		t.Fatalf("approving with no tools = %d, want 400", rec.Code)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		approve(h, pOnly, id, "people"),
		api(h, http.MethodGet, "/_/api/admins/requests", pOnly, nil),
		api(h, http.MethodPost, "/_/api/admins/requests/"+id+"/deny", pOnly, nil),
		api(h, http.MethodPut, "/_/api/admins/requests/settings", pOnly, map[string]bool{"enabled": false}),
	} {
		if rec.Code != http.StatusForbidden {
			t.Fatalf("an administrator without the administrators tool got %d, want 403", rec.Code)
		}
	}
	if rec := approve(h, mod, id, "people"); rec.Code != http.StatusOK {
		t.Fatalf("approving within the approver's tools = %d %s", rec.Code, rec.Body)
	}
}

func TestExistingAdministratorEmailIsIndistinguishableFromAnyOther(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	switchRequests(t, h, owner, true)
	rec := askForAccess(h, "Someone", "octavio@example.com")
	if rec.Code != http.StatusCreated {
		t.Fatalf("request by an existing administrator's email = %d, want the same 201 as any other", rec.Code)
	}
	made := decode(t, rec)
	if len(pendingIDs(t, h, owner)) != 0 {
		t.Fatal("a request for an existing administrator reached the approver")
	}
	if state := decode(t, pollRequest(h, made["id"].(string), made["secret"].(string)))["state"]; state != "pending" {
		t.Fatalf("state = %v, want pending like any other", state)
	}
	if rec := approve(h, owner, made["id"].(string), "people"); rec.Code != http.StatusNotFound {
		t.Fatalf("approving it = %d, want 404", rec.Code)
	}
}

func TestTurningRequestsOffEndsWhatWasWaitingAndForeignOriginsAreRefused(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	switchRequests(t, h, owner, true)
	made := decode(t, askForAccess(h, "Ana", "ana@example.com"))
	id, secret := made["id"].(string), made["secret"].(string)

	// Another site cannot post on a visitor's behalf.
	foreign := httptest.NewRequest(http.MethodPost, "http://"+consoleHost+"/_/api/admin/requests", strings.NewReader(`{"name":"X","email":"x@example.com"}`))
	foreign.Host = consoleHost
	foreign.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, foreign)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-origin request = %d, want 400", rec.Code)
	}

	switchRequests(t, h, owner, false)
	if rec := approve(h, owner, id, "people"); rec.Code != http.StatusNotFound {
		t.Fatalf("approving after turning requests off = %d, want 404", rec.Code)
	}
	if rec := pollRequest(h, id, secret); rec.Code != http.StatusNotFound {
		t.Fatalf("poll after turning requests off = %d, want 404", rec.Code)
	}
	if rec := askForAccess(h, "Ana", "ana@example.com"); rec.Code != http.StatusForbidden {
		t.Fatalf("new request after turning off = %d, want 403", rec.Code)
	}
}

func TestPublicRequestFormValidatesAndIsRateLimited(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	switchRequests(t, h, owner, true)
	for _, bad := range [][2]string{{"", "a@example.com"}, {"Ana", "not-an-email"}, {strings.Repeat("n", 81), "a@example.com"}, {"A\nna", "a@example.com"}} {
		if rec := askForAccess(h, bad[0], bad[1]); rec.Code != http.StatusBadRequest {
			t.Fatalf("request %q %q = %d, want 400", bad[0], bad[1], rec.Code)
		}
	}
	limited := false
	for i := 0; i < 20 && !limited; i++ {
		rec := askForAccess(h, "Spam", string(rune('a'+i))+"@example.com")
		limited = rec.Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("a flood of requests was never refused")
	}
}
