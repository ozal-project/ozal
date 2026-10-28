package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/switchboard-project/switchboard/internal/config"
	"github.com/switchboard-project/switchboard/internal/events"
	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

const consoleOrigin = "http://switchboard.localhost:9091"

func identityHandler(t *testing.T, extra ...Option) (*Handler, *events.Recorder) {
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
	recorder := &events.Recorder{}
	return New(&fakeManager{cfg: config.Config{Admin: config.Admin{Token: "secret"}}}, recorder, append([]Option{WithIdentities(sqlite.NewAdmins(db))}, extra...)...), recorder
}

// browser sends a same-origin JSON request the way the console does.
func browser(h http.Handler, host, path, ceremony string, body any) *httptest.ResponseRecorder {
	return browserAs(h, host, path, ceremony, "", body)
}

// browserAs is browser with a signed-in operator session.
func browserAs(h http.Handler, host, path, ceremony, session string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "http://"+host+path, bytes.NewReader(raw))
	req.Host = host
	req.Header.Set("Origin", "http://"+host)
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set("Authorization", "Bearer "+session)
	}
	if ceremony != "" {
		req.Header.Set(adminCeremony, ceremony)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func hostEnrolment(t *testing.T, h *Handler, kind, email string) (string, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"kind": kind, "email": email})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9090/_/api/admin/enrolments", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Token, rec.Code
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return out
}

// enrol runs the whole browser side of a registration and returns the response.
func enrol(t *testing.T, h *Handler, device *virtualAuthenticator, token, name, email string) *httptest.ResponseRecorder {
	t.Helper()
	begin := browser(h, "switchboard.localhost:9091", "/_/api/admin/enrol/begin", "", map[string]string{"token": token, "name": name, "email": email})
	if begin.Code != http.StatusOK {
		return begin
	}
	started := decode(t, begin)
	credential := device.create(t, started["options"].(map[string]any), consoleOrigin)
	return browser(h, "switchboard.localhost:9091", "/_/api/admin/enrol/finish", started["ceremony"].(string), credential)
}

func signIn(t *testing.T, h *Handler, device *virtualAuthenticator) *httptest.ResponseRecorder {
	t.Helper()
	begin := browser(h, "switchboard.localhost:9091", "/_/api/admin/login/begin", "", map[string]string{})
	if begin.Code != http.StatusOK {
		return begin
	}
	started := decode(t, begin)
	assertion := device.get(t, started["options"].(map[string]any), consoleOrigin)
	return browser(h, "switchboard.localhost:9091", "/_/api/admin/login/finish", started["ceremony"].(string), assertion)
}

func TestFirstAdministratorEnrolsOnceAndGetsEveryTool(t *testing.T) {
	h, recorder := identityHandler(t)
	token, code := hostEnrolment(t, h, "bootstrap", "")
	if code != http.StatusCreated || token == "" {
		t.Fatalf("host enrolment = %d", code)
	}
	device := newAuthenticator(t)
	rec := enrol(t, h, device, token, "Octavio Santacruz", "octavio@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("enrol = %d %s", rec.Code, rec.Body)
	}
	session := decode(t, rec)
	if session["name"] != "Octavio Santacruz" || len(session["tools"].([]any)) != len(operatorTools) {
		t.Fatalf("session = %v, want the owner with every tool", session)
	}
	// The session works as an operator: it reads route state.
	status := request(h, http.MethodGet, "/_/api/status", session["token"].(string), "")
	if status.Code != http.StatusOK {
		t.Fatalf("status with the new session = %d", status.Code)
	}
	// The same link cannot enrol a second device or person.
	if again := enrol(t, h, newAuthenticator(t), token, "Mallory", "mallory@example.com"); again.Code == http.StatusOK {
		t.Fatal("an enrolment link was used twice")
	}
	// No second bootstrap once someone exists; the host must name who to recover.
	if _, code := hostEnrolment(t, h, "bootstrap", ""); code != http.StatusConflict {
		t.Fatalf("second bootstrap = %d, want 409", code)
	}
	if !recorder.Has(events.Type("admin.passkey_registered")) {
		t.Fatal("registration left no event")
	}
}

func TestAdministratorSignsInWithTheirPasskey(t *testing.T) {
	h, _ := identityHandler(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	device := newAuthenticator(t)
	if rec := enrol(t, h, device, token, "Octavio", "octavio@example.com"); rec.Code != http.StatusOK {
		t.Fatalf("enrol = %d %s", rec.Code, rec.Body)
	}
	rec := signIn(t, h, device)
	if rec.Code != http.StatusOK || decode(t, rec)["token"] == "" {
		t.Fatalf("sign-in = %d %s", rec.Code, rec.Body)
	}
	// Someone else's passkey is not an administrator.
	if rec := signIn(t, h, newAuthenticator(t)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown passkey = %d, want 401", rec.Code)
	}
}

func TestAdminPasskeysRefuseAnIPAddressAndAForeignOrigin(t *testing.T) {
	h, _ := identityHandler(t)
	if rec := browser(h, "127.0.0.1:9091", "/_/api/admin/login/begin", "", map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("login on an IP address = %d, want 400", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "http://switchboard.localhost:9091/_/api/admin/login/begin", bytes.NewReader([]byte("{}")))
	req.Host = "switchboard.localhost:9091"
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("login from a foreign origin = %d, want 400", rec.Code)
	}
}

func TestEnrolmentIssuingIsHostOnly(t *testing.T) {
	h, _ := identityHandler(t)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9090/_/api/admin/enrolments", bytes.NewReader([]byte(`{"kind":"bootstrap"}`)))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin token issued an enrolment: %d, want 403", rec.Code)
	}
}

func TestUnknownEnrolmentTokenIsRefused(t *testing.T) {
	h, _ := identityHandler(t)
	begin := browser(h, "switchboard.localhost:9091", "/_/api/admin/enrol/begin", "", map[string]string{"token": "not-a-real-token", "name": "X", "email": "x@example.com"})
	if begin.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token = %d, want 401", begin.Code)
	}
}

func TestRecoveryReplacesLostPasskeys(t *testing.T) {
	h, _ := identityHandler(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	lost := newAuthenticator(t)
	first := enrol(t, h, lost, token, "Octavio", "octavio@example.com")
	if first.Code != http.StatusOK {
		t.Fatalf("enrol = %d %s", first.Code, first.Body)
	}
	oldSession := decode(t, first)["token"].(string)
	if _, code := hostEnrolment(t, h, "recover", "nobody@example.com"); code != http.StatusNotFound {
		t.Fatalf("recover for an unknown email = %d, want 404", code)
	}
	recoverToken, code := hostEnrolment(t, h, "recover", "octavio@example.com")
	if code != http.StatusCreated {
		t.Fatalf("recover = %d", code)
	}
	replacement := newAuthenticator(t)
	if rec := enrol(t, h, replacement, recoverToken, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("recovery enrol = %d %s", rec.Code, rec.Body)
	}
	if rec := request(h, http.MethodGet, "/_/api/status", oldSession, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session opened before recovery survived: %d", rec.Code)
	}
	if rec := signIn(t, h, lost); rec.Code == http.StatusOK {
		t.Fatal("a passkey that recovery replaced still signs in")
	}
	if rec := signIn(t, h, replacement); rec.Code != http.StatusOK {
		t.Fatalf("replacement passkey sign-in = %d %s", rec.Code, rec.Body)
	}
}

func api(h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	raw := ""
	if body != nil {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	return request(h, method, path, token, raw)
}

func ownerSession(t *testing.T, h *Handler) string {
	t.Helper()
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	rec := enrol(t, h, newAuthenticator(t), token, "Octavio", "octavio@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner enrol = %d %s", rec.Code, rec.Body)
	}
	return decode(t, rec)["token"].(string)
}

func invite(t *testing.T, h *Handler, session, email string, tools ...string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	rec := api(h, http.MethodPost, "/_/api/admins/invite", session, map[string]any{"email": email, "tools": tools})
	if rec.Code != http.StatusCreated {
		return "", rec
	}
	return decode(t, rec)["token"].(string), rec
}

func TestAdministratorInvitesAnotherWithOnlyTheChosenTools(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	token, rec := invite(t, h, owner, "ana@example.com", "people")
	if token == "" {
		t.Fatalf("invite = %d %s", rec.Code, rec.Body)
	}
	device := newAuthenticator(t)
	joined := enrol(t, h, device, token, "Ana Perez", "someone-else@example.com") // email in the form is ignored
	if joined.Code != http.StatusOK {
		t.Fatalf("invitee enrol = %d %s", joined.Code, joined.Body)
	}
	session := decode(t, joined)
	if tools := session["tools"].([]any); len(tools) != 1 || tools[0] != "people" {
		t.Fatalf("invitee tools = %v, want only people", tools)
	}
	ana := session["token"].(string)
	if rec := api(h, http.MethodPost, "/_/api/people/invite", ana, map[string]any{"email": "x@example.com"}); rec.Code == http.StatusForbidden {
		t.Fatalf("a people administrator was refused the people tool: %s", rec.Body)
	}
	if rec := api(h, http.MethodPost, "/_/api/apps/plan", ana, map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("a people-only administrator reached applications: %d", rec.Code)
	}
	if _, rec := invite(t, h, ana, "eve@example.com", "people"); rec.Code != http.StatusForbidden {
		t.Fatalf("an administrator without the administrators tool invited someone: %d", rec.Code)
	}
	// The same link cannot be used twice, and the email stays taken.
	if again := enrol(t, h, newAuthenticator(t), token, "Mallory", "m@example.com"); again.Code == http.StatusOK {
		t.Fatal("an invitation was used twice")
	}
	if _, rec := invite(t, h, owner, "ana@example.com", "people"); rec.Code != http.StatusConflict {
		t.Fatalf("inviting an existing administrator = %d, want 409", rec.Code)
	}
	if rec := signIn(t, h, device); rec.Code != http.StatusOK {
		t.Fatalf("invitee sign-in = %d %s", rec.Code, rec.Body)
	}
}

func TestAnInviterCannotGrantMoreThanTheyHold(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	token, _ := invite(t, h, owner, "bob@example.com", "administrators", "people")
	joined := enrol(t, h, newAuthenticator(t), token, "Bob", "")
	if joined.Code != http.StatusOK {
		t.Fatalf("enrol = %d %s", joined.Code, joined.Body)
	}
	bob := decode(t, joined)["token"].(string)
	if _, rec := invite(t, h, bob, "carol@example.com", "sites"); rec.Code != http.StatusForbidden {
		t.Fatalf("bob granted a tool he lacks: %d, want 403", rec.Code)
	}
	if _, rec := invite(t, h, bob, "carol@example.com", "people"); rec.Code != http.StatusCreated {
		t.Fatalf("bob could not grant a tool he holds: %d %s", rec.Code, rec.Body)
	}
}

func TestDisablingAnAdministratorEndsTheirSessionAndGuardsTheLastManager(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	token, _ := invite(t, h, owner, "ana@example.com", "people")
	device := newAuthenticator(t)
	joined := enrol(t, h, device, token, "Ana", "")
	ana := decode(t, joined)["token"].(string)
	var anaID string
	list := api(h, http.MethodGet, "/_/api/admins", owner, nil)
	for _, entry := range decode(t, list)["admins"].([]any) {
		if entry.(map[string]any)["email"] == "ana@example.com" {
			anaID = entry.(map[string]any)["id"].(string)
		}
	}
	if anaID == "" {
		t.Fatalf("invitee missing from the list: %s", list.Body)
	}
	if rec := api(h, http.MethodGet, "/_/api/status", ana, nil); rec.Code != http.StatusOK {
		t.Fatalf("session before disabling = %d", rec.Code)
	}
	if rec := api(h, http.MethodPost, "/_/api/admins/"+anaID+"/disable", owner, nil); rec.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	if rec := api(h, http.MethodGet, "/_/api/status", ana, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled administrator's open session still works: %d", rec.Code)
	}
	if rec := signIn(t, h, device); rec.Code == http.StatusOK {
		t.Fatal("a disabled administrator signed in")
	}
	// Nobody disables themselves, and the last manager cannot be removed.
	var ownerID string
	for _, entry := range decode(t, api(h, http.MethodGet, "/_/api/admins", owner, nil))["admins"].([]any) {
		if entry.(map[string]any)["you"] == true {
			ownerID = entry.(map[string]any)["id"].(string)
		}
	}
	if rec := api(h, http.MethodPost, "/_/api/admins/"+ownerID+"/disable", owner, nil); rec.Code != http.StatusConflict {
		t.Fatalf("self-disable = %d, want 409", rec.Code)
	}
	if rec := api(h, http.MethodPost, "/_/api/admins/"+ownerID+"/disable", "secret", nil); rec.Code != http.StatusConflict {
		t.Fatalf("disabling the last manager through the admin token = %d, want 409", rec.Code)
	}
	if rec := api(h, http.MethodPost, "/_/api/admins/"+anaID+"/enable", owner, nil); rec.Code != http.StatusOK {
		t.Fatalf("enable = %d", rec.Code)
	}
}

func TestStateShowsTheHostCommandOnlyBeforeTheFirstAdministrator(t *testing.T) {
	h, _ := identityHandler(t, WithEnrolCommand("sudo switchboard console enrol -config /etc/x/routing.json"))
	before := decode(t, requestWithHost(h, "switchboard.localhost:9091", http.MethodGet, "/_/api/admin/state", "", ""))
	if before["enrol_command"] != "sudo switchboard console enrol -config /etc/x/routing.json" {
		t.Fatalf("command missing before enrolment: %v", before)
	}
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	if rec := enrol(t, h, newAuthenticator(t), token, "Octavio", "octavio@example.com"); rec.Code != http.StatusOK {
		t.Fatalf("enrol = %d", rec.Code)
	}
	after := decode(t, requestWithHost(h, "switchboard.localhost:9091", http.MethodGet, "/_/api/admin/state", "", ""))
	if _, leaked := after["enrol_command"]; leaked {
		t.Fatalf("host paths still shown after enrolment: %v", after)
	}
}

// addPasskey runs the browser side of adding another passkey to the signed-in
// administrator.
func addPasskey(t *testing.T, h *Handler, session string, device *virtualAuthenticator) *httptest.ResponseRecorder {
	t.Helper()
	begin := browserAs(h, "switchboard.localhost:9091", "/_/api/admin/passkeys/begin", "", session, map[string]string{})
	if begin.Code != http.StatusOK {
		return begin
	}
	started := decode(t, begin)
	credential := device.create(t, started["options"].(map[string]any), consoleOrigin)
	return browserAs(h, "switchboard.localhost:9091", "/_/api/admin/passkeys/finish", started["ceremony"].(string), session, credential)
}

func TestAdministratorAddsASecondPasskeyAndEitherSignsIn(t *testing.T) {
	h, _ := identityHandler(t)
	first := newAuthenticator(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	rec := enrol(t, h, first, token, "Octavio", "octavio@example.com")
	session := decode(t, rec)["token"].(string)

	if rec := browserAs(h, "switchboard.localhost:9091", "/_/api/admin/passkeys/begin", "", "", map[string]string{}); rec.Code == http.StatusOK {
		t.Fatal("a passkey was added without a session")
	}
	second := newAuthenticator(t)
	if rec := addPasskey(t, h, session, second); rec.Code != http.StatusOK {
		t.Fatalf("add passkey = %d %s", rec.Code, rec.Body)
	}
	for name, device := range map[string]*virtualAuthenticator{"first": first, "second": second} {
		if rec := signIn(t, h, device); rec.Code != http.StatusOK {
			t.Fatalf("%s passkey cannot sign in: %d %s", name, rec.Code, rec.Body)
		}
	}
	// A session that did not come from a passkey (the admin token) has no account to add to.
	if rec := browserAs(h, "switchboard.localhost:9091", "/_/api/admin/passkeys/begin", "", "secret", map[string]string{}); rec.Code != http.StatusForbidden {
		t.Fatalf("admin token added a passkey: %d", rec.Code)
	}
}

func TestAdministratorRecoversAnotherFromTheConsoleButNeverUpward(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	adaOld := newAuthenticator(t)
	inviteToken, _ := invite(t, h, owner, "ana@example.com", "people")
	anaSession := decode(t, enrol(t, h, adaOld, inviteToken, "Ana", "ana@example.com"))["token"].(string)
	bobToken, _ := invite(t, h, owner, "bob@example.com", "administrators", "people")
	bob := decode(t, enrol(t, h, newAuthenticator(t), bobToken, "Bob", "bob@example.com"))["token"].(string)

	list := decode(t, api(h, http.MethodGet, "/_/api/admins", owner, nil))["admins"].([]any)
	ids := map[string]string{}
	for _, a := range list {
		m := a.(map[string]any)
		ids[m["email"].(string)] = m["id"].(string)
	}
	// Bob holds administrators+people: he can recover Ana (people) but not the owner (every tool).
	if rec := api(h, http.MethodPost, "/_/api/admins/"+ids["octavio@example.com"]+"/recover", bob, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("recovery climbed to the owner: %d", rec.Code)
	}
	// Ana has no administrators tool.
	if rec := api(h, http.MethodPost, "/_/api/admins/"+ids["ana@example.com"]+"/recover", anaSession, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("an administrator without the tool recovered someone: %d", rec.Code)
	}
	// Nobody recovers themselves here: they are signed in.
	if rec := api(h, http.MethodPost, "/_/api/admins/"+ids["bob@example.com"]+"/recover", bob, nil); rec.Code != http.StatusConflict {
		t.Fatalf("self recovery = %d, want 409", rec.Code)
	}
	rec := api(h, http.MethodPost, "/_/api/admins/"+ids["ana@example.com"]+"/recover", bob, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("recover Ana = %d %s", rec.Code, rec.Body)
	}
	link := decode(t, rec)["token"].(string)
	adaNew := newAuthenticator(t)
	if rec := enrol(t, h, adaNew, link, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("Ana's recovery enrol = %d %s", rec.Code, rec.Body)
	}
	if rec := request(h, http.MethodGet, "/_/api/status", anaSession, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("Ana's session survived recovery: %d", rec.Code)
	}
	if rec := signIn(t, h, adaOld); rec.Code == http.StatusOK {
		t.Fatal("the replaced passkey still signs in")
	}
	if rec := signIn(t, h, adaNew); rec.Code != http.StatusOK {
		t.Fatalf("new passkey sign-in = %d", rec.Code)
	}
}

func TestFirstRunLinkEnrolsOnceAndIsSilentAfterwards(t *testing.T) {
	h, _ := identityHandler(t)
	token, expires, err := IssueFirstRun(context.Background(), h.identities)
	if err != nil || token == "" || time.Until(expires) < 10*time.Minute || time.Until(expires) > FirstRunTTL {
		t.Fatalf("first-run link: %q %v %v", token, expires, err)
	}
	if rec := enrol(t, h, newAuthenticator(t), token, "Octavio", "octavio@example.com"); rec.Code != http.StatusOK {
		t.Fatalf("enrol with the first-run link = %d %s", rec.Code, rec.Body)
	}
	if again, _, err := IssueFirstRun(context.Background(), h.identities); err != nil || again != "" {
		t.Fatalf("a link was issued after the first administrator: %q %v", again, err)
	}
}

func TestOnlyTheLatestRecoveryLinkWorksAndNoneSurviveRecovery(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	invitation, _ := invite(t, h, owner, "ana@example.com", "people")
	enrol(t, h, newAuthenticator(t), invitation, "Ana", "ana@example.com")
	list := decode(t, api(h, http.MethodGet, "/_/api/admins", owner, nil))["admins"].([]any)
	var ana string
	for _, a := range list {
		if m := a.(map[string]any); m["email"] == "ana@example.com" {
			ana = m["id"].(string)
		}
	}
	issue := func() string {
		rec := api(h, http.MethodPost, "/_/api/admins/"+ana+"/recover", owner, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("recover = %d", rec.Code)
		}
		return decode(t, rec)["token"].(string)
	}
	first, second, third := issue(), issue(), issue()
	// Issuing again replaced the older links.
	if rec := enrol(t, h, newAuthenticator(t), first, "", ""); rec.Code == http.StatusOK {
		t.Fatal("an older recovery link still worked after a newer one was issued")
	}
	// A host-issued link (the other door) is retired by the same completion.
	hostLink, _ := hostEnrolment(t, h, "recover", "ana@example.com")
	if rec := enrol(t, h, newAuthenticator(t), hostLink, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("latest link = %d %s", rec.Code, rec.Body)
	}
	for name, link := range map[string]string{"second": second, "third": third} {
		if rec := enrol(t, h, newAuthenticator(t), link, "", ""); rec.Code == http.StatusOK {
			t.Fatalf("the %s recovery link replaced the account again after it was recovered", name)
		}
	}
}

// A passkey request that passed the session check just before a recovery
// revoked the session must not be saved after the recovery.
func TestPasskeyAddedAfterTheSessionWasRevokedIsRefused(t *testing.T) {
	h, _ := identityHandler(t)
	device := newAuthenticator(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	session := decode(t, enrol(t, h, device, token, "Octavio", "octavio@example.com"))["token"].(string)

	grant, ok := h.operators.authenticate(session)
	if !ok {
		t.Fatal("session not recognised")
	}
	begin := browserAs(h, "switchboard.localhost:9091", "/_/api/admin/passkeys/begin", "", session, map[string]string{})
	started := decode(t, begin)
	credential := newAuthenticator(t).create(t, started["options"].(map[string]any), consoleOrigin)

	// The request passes every check and waits for the identity lock, which a
	// recovery holds while it revokes the account's sessions.
	raw, _ := json.Marshal(credential)
	req := httptest.NewRequest(http.MethodPost, "http://switchboard.localhost:9091/_/api/admin/passkeys/finish", bytes.NewReader(raw))
	req.Host = "switchboard.localhost:9091"
	req.Header.Set("Origin", "http://switchboard.localhost:9091")
	req.Header.Set(adminCeremony, started["ceremony"].(string))
	req = req.WithContext(context.WithValue(req.Context(), operatorKey{}, grant))
	rec := httptest.NewRecorder()
	h.identityMu.Lock()
	done := make(chan struct{})
	go func() { h.addPasskeyFinish(rec, req); close(done) }()
	time.Sleep(300 * time.Millisecond)
	h.operators.revokeAccount(h.operators.account(grant.ID))
	h.identityMu.Unlock()
	<-done
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("late add = %d %s, want 401", rec.Code, rec.Body)
	}
}

func TestEnrolInfoTellsTheFormWhatTheLinkAlreadyFixes(t *testing.T) {
	h, _ := identityHandler(t)
	first, _ := hostEnrolment(t, h, "bootstrap", "")
	info := func(token string) (int, map[string]any) {
		rec := browser(h, "switchboard.localhost:9091", "/_/api/admin/enrol/info", "", map[string]string{"token": token})
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, got := info(first); code != http.StatusOK || got["kind"] != "bootstrap" || got["email"] != nil {
		t.Fatalf("bootstrap info = %d %v", code, got)
	}
	owner := decode(t, enrol(t, h, newAuthenticator(t), first, "Octavio", "octavio@example.com"))["token"].(string)
	invitation, _ := invite(t, h, owner, "ana@example.com", "people")
	if code, got := info(invitation); code != http.StatusOK || got["kind"] != "invite" || got["email"] != "ana@example.com" {
		t.Fatalf("invite info = %d %v", code, got)
	}
	enrol(t, h, newAuthenticator(t), invitation, "Ana", "ana@example.com")
	recovery, _ := hostEnrolment(t, h, "recover", "ana@example.com")
	if code, got := info(recovery); code != http.StatusOK || got["kind"] != "recover" || got["email"] != "ana@example.com" {
		t.Fatalf("recover info = %d %v", code, got)
	}
	// Asking does not use the link up, and an unknown one learns nothing.
	if code, _ := info(recovery); code != http.StatusOK {
		t.Fatal("info consumed the link")
	}
	if code, got := info("nope"); code != http.StatusUnauthorized || got["email"] != nil {
		t.Fatalf("unknown token = %d %v", code, got)
	}
}

func TestPasskeySessionsSlideOnlyWhenRenewedAndNeverPastTheLimit(t *testing.T) {
	s := newOperatorSessions()
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	token, g, err := s.issueSliding("Octavio")
	if err != nil || !g.Expires.Equal(now.Add(passkeyIdle)) {
		t.Fatalf("issue: %v %v", g.Expires, err)
	}
	now = now.Add(20 * time.Minute)
	if got, _ := s.authenticate(token); !got.Expires.Equal(g.Expires) {
		t.Fatal("an ordinary request extended the session; only renew may")
	}
	renewed, ok := s.renew(token)
	if !ok || !renewed.Expires.Equal(now.Add(passkeyIdle)) {
		t.Fatalf("renew = %v %v", renewed.Expires, ok)
	}
	// Keep renewing: the session still ends at the absolute limit.
	for i := 0; i < 30; i++ {
		now = now.Add(25 * time.Minute)
		s.renew(token)
	}
	if _, ok := s.authenticate(token); ok {
		t.Fatal("a renewed session outlived its absolute limit")
	}
	// Idle: no renewal for longer than the idle window ends it.
	token, _, _ = s.issueSliding("Octavio")
	now = now.Add(passkeyIdle + time.Second)
	if _, ok := s.renew(token); ok {
		t.Fatal("an idle session was renewed after it expired")
	}
	// Host logins do not slide.
	host, hg, _ := s.issue("host", false)
	now = now.Add(time.Minute)
	if got, _ := s.renew(host); !got.Expires.Equal(hg.Expires) {
		t.Fatal("a host login was extended")
	}
}

func TestAnotherBrowserAddsAPasskeyThroughAOneUseLink(t *testing.T) {
	h, _ := identityHandler(t)
	chrome := newAuthenticator(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	session := decode(t, enrol(t, h, chrome, token, "Octavio", "octavio@example.com"))["token"].(string)

	if rec := api(h, http.MethodPost, "/_/api/admin/devices", "secret", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("admin token issued a device link: %d", rec.Code)
	}
	rec := api(h, http.MethodPost, "/_/api/admin/devices", session, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("device link = %d %s", rec.Code, rec.Body)
	}
	link := decode(t, rec)["token"].(string)
	info := decode(t, browser(h, "switchboard.localhost:9091", "/_/api/admin/enrol/info", "", map[string]string{"token": link}))
	if info["kind"] != "device" || info["email"] != "octavio@example.com" {
		t.Fatalf("info = %v", info)
	}
	firefox := newAuthenticator(t)
	joined := enrol(t, h, firefox, link, "", "")
	if joined.Code != http.StatusOK || decode(t, joined)["token"] == "" {
		t.Fatalf("device enrol = %d %s", joined.Code, joined.Body)
	}
	for name, device := range map[string]*virtualAuthenticator{"chrome": chrome, "firefox": firefox} {
		if rec := signIn(t, h, device); rec.Code != http.StatusOK {
			t.Fatalf("%s passkey sign-in = %d", name, rec.Code)
		}
	}
	if rec := enrol(t, h, newAuthenticator(t), link, "", ""); rec.Code == http.StatusOK {
		t.Fatal("a device link was used twice")
	}
	// Recovery ends a link that was issued before it.
	again := decode(t, api(h, http.MethodPost, "/_/api/admin/devices", session, nil))["token"].(string)
	recover, _ := hostEnrolment(t, h, "recover", "octavio@example.com")
	if rec := enrol(t, h, newAuthenticator(t), recover, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("recovery = %d", rec.Code)
	}
	if rec := enrol(t, h, newAuthenticator(t), again, "", ""); rec.Code == http.StatusOK {
		t.Fatal("a device link survived the account's recovery")
	}
}

type passkeyEntry struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Current bool   `json:"current"`
}

func listPasskeys(t *testing.T, h *Handler, session string) (int, []passkeyEntry) {
	t.Helper()
	rec := api(h, http.MethodGet, "/_/api/admin/passkeys", session, nil)
	var out struct {
		Passkeys []passkeyEntry `json:"passkeys"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Passkeys
}

func TestAdministratorRemovesALostPasskeyAndItsSessionsEnd(t *testing.T) {
	h, recorder := identityHandler(t)
	laptop, phone := newAuthenticator(t), newAuthenticator(t)
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	desk := decode(t, enrol(t, h, laptop, token, "Octavio", "octavio@example.com"))["token"].(string)
	if rec := addPasskey(t, h, desk, phone); rec.Code != http.StatusOK {
		t.Fatalf("add passkey = %d %s", rec.Code, rec.Body)
	}
	onPhone := decode(t, signIn(t, h, phone))["token"].(string)

	code, passkeys := listPasskeys(t, h, desk)
	if code != http.StatusOK || len(passkeys) != 2 {
		t.Fatalf("list = %d %v", code, passkeys)
	}
	var lost string
	for _, p := range passkeys {
		if !p.Current {
			lost = p.ID
		}
	}
	if lost == "" || passkeys[0].Current == passkeys[1].Current {
		t.Fatalf("exactly one passkey must be this session's: %v", passkeys)
	}
	for _, p := range passkeys {
		if p.Current {
			if rec := api(h, http.MethodDelete, "/_/api/admin/passkeys/"+p.ID, desk, nil); rec.Code != http.StatusConflict {
				t.Fatalf("removed the passkey that opened this session while another exists: %d", rec.Code)
			}
		}
	}
	if rec := api(h, http.MethodDelete, "/_/api/admin/passkeys/"+lost, desk, nil); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if code, _ := listPasskeys(t, h, onPhone); code != http.StatusUnauthorized {
		t.Fatalf("a session opened with the removed passkey still works: %d", code)
	}
	if rec := signIn(t, h, phone); rec.Code == http.StatusOK {
		t.Fatal("the removed passkey still signs in")
	}
	if code, passkeys := listPasskeys(t, h, desk); code != http.StatusOK || len(passkeys) != 1 || !passkeys[0].Current {
		t.Fatalf("the remaining session or passkey was affected: %d %v", code, passkeys)
	}
	if rec := signIn(t, h, laptop); rec.Code != http.StatusOK {
		t.Fatalf("the kept passkey stopped signing in: %d", rec.Code)
	}
	if !recorder.Has("admin.passkey_removed") {
		t.Fatal("removal was not recorded")
	}
}

func TestPasskeyRemovalIsOnlyForYourOtherPasskeys(t *testing.T) {
	h, _ := identityHandler(t)
	owner := ownerSession(t, h)
	link, _ := invite(t, h, owner, "bea@example.com", "people")
	bea := decode(t, enrol(t, h, newAuthenticator(t), link, "Bea", ""))["token"].(string)
	_, mine := listPasskeys(t, h, owner)
	_, hers := listPasskeys(t, h, bea)
	if len(mine) != 1 || len(hers) != 1 {
		t.Fatalf("passkeys = %v / %v", mine, hers)
	}

	if rec := api(h, http.MethodDelete, "/_/api/admin/passkeys/"+mine[0].ID, owner, nil); rec.Code != http.StatusConflict {
		t.Fatalf("removed the passkey that opened this session: %d", rec.Code)
	}
	if rec := api(h, http.MethodDelete, "/_/api/admin/passkeys/"+hers[0].ID, owner, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("removed another administrator's passkey: %d", rec.Code)
	}
	if rec := api(h, http.MethodDelete, "/_/api/admin/passkeys/not%20base64", owner, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id = %d", rec.Code)
	}
	host, _, err := h.operators.issue("Host operator", false)
	if err != nil {
		t.Fatal(err)
	}
	for name, session := range map[string]string{"admin token": "secret", "host login": host} {
		if code, _ := listPasskeys(t, h, session); code != http.StatusForbidden {
			t.Fatalf("%s listed passkeys: %d", name, code)
		}
		if rec := api(h, http.MethodDelete, "/_/api/admin/passkeys/"+hers[0].ID, session, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s removed a passkey: %d", name, rec.Code)
		}
	}
	if code, hers := listPasskeys(t, h, bea); code != http.StatusOK || len(hers) != 1 {
		t.Fatalf("Bea's passkey changed: %d %v", code, hers)
	}
}

func TestDeviceLabelNamesBrowserAndSystem(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (X11; Linux x86_64; rv:142.0) Gecko/20100101 Firefox/142.0":                                                                  "Firefox on Linux",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36":                                   "Chrome on Linux",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Mobile Safari/537.36":                            "Chrome on Android",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Mobile/15E148 Safari/604.1": "Safari on iPhone",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36 Edg/156.0.0.0":           "Edge on Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15":                   "Safari on macOS",
		"curl/8.0": "",
		"":         "",
	} {
		if got := deviceLabel(ua); got != want {
			t.Errorf("deviceLabel(%q) = %q, want %q", ua, got, want)
		}
	}
}
