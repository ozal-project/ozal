package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

const consoleOrigin = "http://localhost:9091"

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
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "http://"+host+path, bytes.NewReader(raw))
	req.Host = host
	req.Header.Set("Origin", "http://"+host)
	req.Header.Set("Content-Type", "application/json")
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
	begin := browser(h, "localhost:9091", "/_/api/admin/enrol/begin", "", map[string]string{"token": token, "name": name, "email": email})
	if begin.Code != http.StatusOK {
		return begin
	}
	started := decode(t, begin)
	credential := device.create(t, started["options"].(map[string]any), consoleOrigin)
	return browser(h, "localhost:9091", "/_/api/admin/enrol/finish", started["ceremony"].(string), credential)
}

func signIn(t *testing.T, h *Handler, device *virtualAuthenticator) *httptest.ResponseRecorder {
	t.Helper()
	begin := browser(h, "localhost:9091", "/_/api/admin/login/begin", "", map[string]string{})
	if begin.Code != http.StatusOK {
		return begin
	}
	started := decode(t, begin)
	assertion := device.get(t, started["options"].(map[string]any), consoleOrigin)
	return browser(h, "localhost:9091", "/_/api/admin/login/finish", started["ceremony"].(string), assertion)
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
	req := httptest.NewRequest(http.MethodPost, "http://localhost:9091/_/api/admin/login/begin", bytes.NewReader([]byte("{}")))
	req.Host = "localhost:9091"
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
	begin := browser(h, "localhost:9091", "/_/api/admin/enrol/begin", "", map[string]string{"token": "not-a-real-token", "name": "X", "email": "x@example.com"})
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
	before := decode(t, requestWithHost(h, "localhost:9091", http.MethodGet, "/_/api/admin/state", "", ""))
	if before["enrol_command"] != "sudo switchboard console enrol -config /etc/x/routing.json" {
		t.Fatalf("command missing before enrolment: %v", before)
	}
	token, _ := hostEnrolment(t, h, "bootstrap", "")
	if rec := enrol(t, h, newAuthenticator(t), token, "Octavio", "octavio@example.com"); rec.Code != http.StatusOK {
		t.Fatalf("enrol = %d", rec.Code)
	}
	after := decode(t, requestWithHost(h, "localhost:9091", http.MethodGet, "/_/api/admin/state", "", ""))
	if _, leaked := after["enrol_command"]; leaked {
		t.Fatalf("host paths still shown after enrolment: %v", after)
	}
}
