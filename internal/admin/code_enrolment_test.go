package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/enrolcode"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

const chromeOnMac = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// onHost is the terminal talking to the admin socket.
func onHost(h *Handler, method, target string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:9090"+target, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), localKey{}, true))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// atCode is a request from the code page on the login host.
func atCode(rl *RemoteLogin, method, path, claim, ceremony string, body any) (bool, *httptest.ResponseRecorder) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, loginHostOrigin+path, bytes.NewReader(raw))
	req.Header.Set("User-Agent", chromeOnMac)
	if method == http.MethodPost {
		req.Header.Set("Origin", loginHostOrigin)
		req.Header.Set("Content-Type", "application/json")
	}
	if claim != "" {
		req.Header.Set(claimHeader, claim)
	}
	if ceremony != "" {
		req.Header.Set(adminCeremony, ceremony)
	}
	rec := httptest.NewRecorder()
	return rl.ServeAdminLogin(rec, req), rec
}

type issuedCode struct{ poll, code, url string }

func issueCode(t *testing.T, h *Handler, kind, email string) issuedCode {
	t.Helper()
	rec := onHost(h, http.MethodPost, "/_/api/admin/enrolments", map[string]string{"kind": kind, "email": email, "mode": "code", "operator": "saint", "from": "192.168.100.5"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue = %d %s", rec.Code, rec.Body)
	}
	out := decode(t, rec)
	if _, leaked := out["token"]; leaked {
		t.Fatal("the code answer carries the enrolment token")
	}
	return issuedCode{poll: out["poll"].(string), code: out["code"].(string), url: out["url"].(string)}
}

func claimCode(t *testing.T, rl *RemoteLogin, code string) map[string]any {
	t.Helper()
	handled, rec := atCode(rl, http.MethodPost, codePath+"/claim", "", "", map[string]string{"code": code})
	if !handled || rec.Code != http.StatusOK {
		t.Fatalf("claim: handled %v, %d %s", handled, rec.Code, rec.Body)
	}
	return decode(t, rec)
}

func watch(t *testing.T, h *Handler, poll string) map[string]any {
	t.Helper()
	rec := onHost(h, http.MethodGet, "/_/api/admin/enrolments/code/"+poll, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("watch = %d %s", rec.Code, rec.Body)
	}
	return decode(t, rec)
}

// createOnPage runs the passkey ceremony on the code page with device.
func createOnPage(t *testing.T, rl *RemoteLogin, claim string, who map[string]string, device *virtualAuthenticator) *httptest.ResponseRecorder {
	t.Helper()
	_, begun := atCode(rl, http.MethodPost, codePath+"/begin", claim, "", who)
	if begun.Code != http.StatusOK {
		t.Fatalf("begin = %d %s", begun.Code, begun.Body)
	}
	started := decode(t, begun)
	credential := device.create(t, started["options"].(map[string]any), loginHostOrigin)
	_, rec := atCode(rl, http.MethodPost, codePath+"/finish", claim, started["ceremony"].(string), credential)
	return rec
}

// The first administrator is created on the page from any device by typing
// the code. The page shows whose request it is; the terminal sees which
// browser claimed and who was enrolled; the passkey then signs in to the
// console through the login host.
func TestCodeEnrolmentCreatesTheFirstAdministratorOnThePage(t *testing.T) {
	h, rl, recorder := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	if issued.url != loginHostOrigin+codePath {
		t.Fatalf("url = %q", issued.url)
	}
	for _, asset := range []string{codePath, codePath + ".js", codePath + ".css"} {
		if handled, page := atCode(rl, http.MethodGet, asset, "", "", nil); !handled || page.Code != http.StatusOK ||
			!strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s: handled %v, %d", asset, handled, page.Code)
		}
	}
	claimed := claimCode(t, rl, strings.ToLower(issued.code))
	if claimed["operator"] != "saint" || claimed["host"] != "auth.example.com" || claimed["expires_in"].(float64) <= 0 ||
		claimed["server"] == "" || claimed["from"] != "192.168.100.5" || claimed["from_local"] != true || claimed["browser"] == "" {
		t.Fatalf("the page is not told whose request this is: %v", claimed)
	}
	seen := watch(t, h, issued.poll)
	if seen["state"] != "claimed" || seen["browser"] != "Chrome on macOS" || seen["address"] == "" {
		t.Fatalf("terminal sees %v", seen)
	}
	device := newAuthenticator(t)
	rec := createOnPage(t, rl, claimed["claim"].(string), map[string]string{"name": "Octavio Santacruz", "email": "octavio@example.com"}, device)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body)
	}
	if done := decode(t, rec); done["email"] != "octavio@example.com" || !strings.HasPrefix(done["console"].(string), "http://ozal.localhost:") {
		t.Fatalf("finish = %v", done)
	} else if _, named := done["provider"]; named {
		// The test authenticator reports no AAGUID: an unknown provider is
		// never named as the place the passkey lives.
		t.Fatalf("an unknown provider was named: %v", done)
	}
	if seen := watch(t, h, issued.poll); seen["state"] != "done" || seen["account"] != "Octavio Santacruz <octavio@example.com>" {
		t.Fatalf("terminal sees %v after the passkey", seen)
	}
	if state := decode(t, request(h, http.MethodGet, "/_/api/admin/state", "", "")); state["enrolled"] != true {
		t.Fatalf("state = %v", state)
	}
	for _, e := range []string{"admin.enrolment_code_issued", "admin.enrolment_code_claimed", "admin.enrolment_code_done"} {
		if !recorder.Has(events.Type(e)) {
			t.Errorf("no %s event", e)
		}
	}
	// The same passkey opens the console through the login host.
	secret, challenge := verifier()
	code, _ := loginHostCeremony(t, h, rl, device, map[string]any{"challenge": challenge})
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/finish", "", map[string]string{"code": code, "verifier": secret}); rec.Code != http.StatusOK {
		t.Fatalf("console sign-in with the new passkey = %d %s", rec.Code, rec.Body)
	}
}

// While no code from the host is live, the code page does not exist; it
// disappears again once the enrolment ends.
func TestCodePathsExistOnlyWhileACodeIsLive(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	paths := []struct{ method, path string }{
		{http.MethodGet, codePath}, {http.MethodGet, codePath + ".js"}, {http.MethodGet, codePath + ".css"},
		{http.MethodPost, codePath + "/claim"}, {http.MethodPost, codePath + "/withdraw"},
		{http.MethodPost, codePath + "/begin"}, {http.MethodPost, codePath + "/finish"},
	}
	for _, p := range paths {
		if handled, _ := atCode(rl, p.method, p.path, "", "", map[string]any{}); handled {
			t.Errorf("%s %s answered with no code issued", p.method, p.path)
		}
	}
	issued := issueCode(t, h, "bootstrap", "")
	if handled, _ := atCode(rl, http.MethodGet, codePath, "", "", nil); !handled {
		t.Fatal("the page is missing while a code is live")
	}
	onHost(h, http.MethodDelete, "/_/api/admin/enrolments/code/"+issued.poll, nil)
	if handled, _ := atCode(rl, http.MethodGet, codePath, "", "", nil); handled {
		t.Fatal("the page outlived a cancelled code")
	}
}

// "This wasn't me" on the page ends the code: nothing can be created, the
// terminal sees the withdrawal and the page disappears.
func TestCodeWithdrawnOnThePageCreatesNothing(t *testing.T) {
	h, rl, recorder := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	claim := claimCode(t, rl, issued.code)["claim"].(string)
	if _, rec := atCode(rl, http.MethodPost, codePath+"/withdraw", "forged", "", map[string]any{}); rec.Code != http.StatusConflict {
		t.Fatalf("forged withdraw = %d", rec.Code)
	}
	if _, rec := atCode(rl, http.MethodPost, codePath+"/withdraw", claim, "", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("withdraw = %d %s", rec.Code, rec.Body)
	}
	if seen := watch(t, h, issued.poll); seen["state"] != "withdrawn" {
		t.Fatalf("terminal sees %v", seen)
	}
	if handled, rec := atCode(rl, http.MethodPost, codePath+"/begin", claim, "", map[string]string{"name": "Eve", "email": "eve@example.com"}); handled && rec.Code == http.StatusOK {
		t.Fatal("a withdrawn code began a ceremony")
	}
	if state := decode(t, request(h, http.MethodGet, "/_/api/admin/state", "", "")); state["enrolled"] != false {
		t.Fatalf("state = %v", state)
	}
	if !recorder.Has(events.Type("admin.enrolment_code_withdrawn")) {
		t.Error("no withdrawal event")
	}
}

// A code is claimed by one browser; a second browser, a cross-origin page or
// a forged claim token gets nothing.
func TestCodeBelongsToTheFirstBrowserThatTypesIt(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	req := httptest.NewRequest(http.MethodPost, loginHostOrigin+codePath+"/claim", strings.NewReader(`{"code":"`+issued.code+`"}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if handled := rl.ServeAdminLogin(rec, req); !handled || rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin claim: handled %v, %d", handled, rec.Code)
	}
	claimCode(t, rl, issued.code)
	if _, rec := atCode(rl, http.MethodPost, codePath+"/claim", "", "", map[string]string{"code": issued.code}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("second browser = %d", rec.Code)
	}
	if _, rec := atCode(rl, http.MethodPost, codePath+"/begin", "forged-claim", "", map[string]string{"name": "Eve", "email": "eve@example.com"}); rec.Code != http.StatusForbidden {
		t.Fatalf("forged claim = %d", rec.Code)
	}
}

// Guessing from one address refuses that address but leaves the code alive
// for its owner, and the terminal is told how many wrong codes were tried.
func TestGuessingDoesNotEndSomeoneElsesEnrolment(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	for range enrolcode.MaxAddressFailures {
		atCode(rl, http.MethodPost, codePath+"/claim", "", "", map[string]string{"code": "ZZZZ-ZZZZ"})
	}
	if _, rec := atCode(rl, http.MethodPost, codePath+"/claim", "", "", map[string]string{"code": issued.code}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the guessing address = %d", rec.Code)
	}
	if seen := watch(t, h, issued.poll); seen["state"] != "pending" || seen["failures"].(float64) != enrolcode.MaxAddressFailures {
		t.Fatalf("terminal sees %v", seen)
	}
}

// A withdrawal or a host cancellation that arrives while the passkey is being
// stored is refused instead of reported as done.
func TestCancellingWhileStoringIsRefused(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	claim := claimCode(t, rl, issued.code)["claim"].(string)
	if d, _ := h.codes.Reserve(claim); !d.Allowed {
		t.Fatalf("reserve = %+v", d)
	}
	_, rec := atCode(rl, http.MethodPost, codePath+"/withdraw", claim, "", map[string]any{})
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != enrolcode.CodeSaving {
		t.Fatalf("withdraw while storing = %d %s", rec.Code, rec.Body)
	}
	if rec := onHost(h, http.MethodDelete, "/_/api/admin/enrolments/code/"+issued.poll, nil); rec.Code != http.StatusConflict {
		t.Fatalf("host cancel while storing = %d", rec.Code)
	}
}

// A store that fails (here: a passkey that does not verify) releases the
// enrolment, so the browser can try again or withdraw.
func TestAFailedStoreCanBeRetried(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	claim := claimCode(t, rl, issued.code)["claim"].(string)
	who := map[string]string{"name": "Ana", "email": "ana@example.com"}
	_, begun := atCode(rl, http.MethodPost, codePath+"/begin", claim, "", who)
	started := decode(t, begun)
	credential := newAuthenticator(t).create(t, started["options"].(map[string]any), "https://evil.example")
	if _, rec := atCode(rl, http.MethodPost, codePath+"/finish", claim, started["ceremony"].(string), credential); rec.Code == http.StatusOK {
		t.Fatal("a passkey for another origin was stored")
	}
	if seen := watch(t, h, issued.poll); seen["state"] != "claimed" {
		t.Fatalf("after a failed store the terminal sees %v", seen)
	}
	if rec := createOnPage(t, rl, claim, who, newAuthenticator(t)); rec.Code != http.StatusOK {
		t.Fatalf("retry = %d %s", rec.Code, rec.Body)
	}
}

// A code that cannot be issued changes nothing: an earlier recovery link for
// the same administrator keeps working.
func TestAFailedCodeLeavesAnEarlierRecoveryIntact(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	first := issueCode(t, h, "bootstrap", "")
	createOnPage(t, rl, claimCode(t, rl, first.code)["claim"].(string), map[string]string{"name": "Ana", "email": "ana@example.com"}, newAuthenticator(t))
	link, status := hostEnrolment(t, h, "recover", "ana@example.com")
	if status != http.StatusCreated {
		t.Fatalf("recovery link = %d", status)
	}
	for range enrolcode.MaxPending {
		if _, err := h.codes.Issue(enrolcode.Request{Kind: "bootstrap"}); err != nil {
			t.Fatal(err)
		}
	}
	if rec := onHost(h, http.MethodPost, "/_/api/admin/enrolments", map[string]string{"kind": "recover", "email": "ana@example.com", "mode": "code"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("code with a full book = %d", rec.Code)
	}
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/enrol/info", "", map[string]string{"token": link}); rec.Code != http.StatusOK {
		t.Fatalf("the earlier recovery link stopped working: %d %s", rec.Code, rec.Body)
	}
}

// The page never takes a code from its address, so a link cannot enrol on
// someone else's behalf.
func TestCodeIsNeverReadFromTheURL(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	_, page := atCode(rl, http.MethodGet, codePath+"?code="+issued.code, "", "", nil)
	if strings.Contains(page.Body.String(), issued.code) {
		t.Fatal("the page echoes a code from its URL")
	}
	script, err := loginHostAssets.ReadFile("loginhost/admin-code.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range []string{"location.search", "URLSearchParams", "location.hash", "location.href"} {
		if bytes.Contains(script, []byte(reader)) {
			t.Errorf("the code page script reads %s", reader)
		}
	}
}

// A ceremony begun for a code cannot be finished on the console's own
// enrolment path, and the terminal endpoints answer only on the socket.
func TestCodeCeremonyStaysOnItsPathAndTheTerminalAPIIsHostOnly(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	issued := issueCode(t, h, "bootstrap", "")
	claim := claimCode(t, rl, issued.code)["claim"].(string)
	_, begun := atCode(rl, http.MethodPost, codePath+"/begin", claim, "", map[string]string{"name": "Ana", "email": "ana@example.com"})
	started := decode(t, begun)
	credential := newAuthenticator(t).create(t, started["options"].(map[string]any), loginHostOrigin)
	if rec := browser(h, "ozal.localhost:9091", "/_/api/admin/enrol/finish", started["ceremony"].(string), credential); rec.Code == http.StatusOK {
		t.Fatal("a code ceremony finished on the console path")
	}
	for _, c := range []struct{ method, target string }{
		{http.MethodPost, "/_/api/admin/enrolments"},
		{http.MethodGet, "/_/api/admin/enrolments/code/" + issued.poll},
		{http.MethodDelete, "/_/api/admin/enrolments/code/" + issued.poll},
	} {
		if rec := request(h, c.method, c.target, "secret", `{"kind":"bootstrap","mode":"code"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s off the socket = %d", c.method, c.target, rec.Code)
		}
	}
}

// Without admin.login_host there is no public page to type a code on.
func TestCodeEnrolmentNeedsTheLoginHost(t *testing.T) {
	h, _, _ := loginHostHandler(t, false)
	rec := onHost(h, http.MethodPost, "/_/api/admin/enrolments", map[string]string{"kind": "bootstrap", "mode": "code"})
	if rec.Code != http.StatusPreconditionFailed || decode(t, rec)["code"] != "login_host_off" {
		t.Fatalf("issue without login host = %d %s", rec.Code, rec.Body)
	}
}

// Recovery by code replaces the passkeys of the named administrator.
func TestCodeRecoveryReplacesThePasskeys(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	first := issueCode(t, h, "bootstrap", "")
	old := newAuthenticator(t)
	createOnPage(t, rl, claimCode(t, rl, first.code)["claim"].(string), map[string]string{"name": "Ana", "email": "ana@example.com"}, old)

	again := issueCode(t, h, "recover", "ana@example.com")
	claimed := claimCode(t, rl, again.code)
	if claimed["kind"] != "recover" || claimed["email"] != "ana@example.com" {
		t.Fatalf("recover claim = %v", claimed)
	}
	if rec := createOnPage(t, rl, claimed["claim"].(string), map[string]string{}, newAuthenticator(t)); rec.Code != http.StatusOK {
		t.Fatalf("recover finish = %d %s", rec.Code, rec.Body)
	}
	_, challenge := verifier()
	start := browser(h, "ozal.localhost:9091", "/_/api/admin/remote/start", "", map[string]any{"challenge": challenge})
	tx := strings.SplitN(decode(t, start)["url"].(string), "tx=", 2)[1]
	_, signIn := atLoginHost(rl, http.MethodPost, remotePath+"/begin?tx="+tx, "", map[string]any{})
	options := decode(t, signIn)
	assertion := old.get(t, options["options"].(map[string]any), loginHostOrigin)
	if _, rec := atLoginHost(rl, http.MethodPost, remotePath+"/finish?tx="+tx, options["ceremony"].(string), assertion); rec.Code == http.StatusOK {
		t.Fatal("the replaced passkey still signs in")
	}
}

// pausingStore holds CompleteEnrolment until released, to put a withdrawal in
// the middle of a real store.
type pausingStore struct {
	identities
	entered, release chan struct{}
}

func (s *pausingStore) CompleteEnrolment(ctx context.Context, tokenHash []byte, account adminid.Account, credential webauthn.Credential, label string) (adminid.Account, error) {
	close(s.entered)
	<-s.release
	return s.identities.CompleteEnrolment(ctx, tokenHash, account, credential, label)
}

// The race the reservation exists for: "This wasn't me" pressed while the
// passkey is being stored is refused, and the store completes. Without the
// reservation the withdrawal would answer success and the account would be
// created anyway.
func TestWithdrawDuringARealStoreIsRefused(t *testing.T) {
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
	store := &pausingStore{identities: sqlite.NewAdmins(db), entered: make(chan struct{}), release: make(chan struct{})}
	cfg := config.Config{
		Admin: config.Admin{Token: "secret", LoginHost: true},
		Auth:  config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: loginHostOrigin}},
	}
	rl := NewRemoteLogin()
	h := New(&fakeManager{cfg: cfg}, &events.Recorder{}, WithIdentities(store), WithRemoteLogin(rl))

	issued := issueCode(t, h, "bootstrap", "")
	claim := claimCode(t, rl, issued.code)["claim"].(string)
	_, begun := atCode(rl, http.MethodPost, codePath+"/begin", claim, "", map[string]string{"name": "Ana", "email": "ana@example.com"})
	started := decode(t, begun)
	credential := newAuthenticator(t).create(t, started["options"].(map[string]any), loginHostOrigin)
	finished := make(chan int, 1)
	go func() {
		_, rec := atCode(rl, http.MethodPost, codePath+"/finish", claim, started["ceremony"].(string), credential)
		finished <- rec.Code
	}()
	<-store.entered
	_, withdraw := atCode(rl, http.MethodPost, codePath+"/withdraw", claim, "", map[string]any{})
	close(store.release)
	if code := <-finished; code != http.StatusOK {
		t.Fatalf("finish = %d", code)
	}
	if withdraw.Code == http.StatusOK {
		t.Fatal("a withdrawal reported success while the passkey was being stored")
	}
	if seen := watch(t, h, issued.poll); seen["state"] != "done" {
		t.Fatalf("terminal sees %v", seen)
	}
}

type fakeCountries map[string]string

func (f fakeCountries) Country(addr netip.Addr) string { return f[addr.String()] }

// With a local country table the page and the terminal get the country of
// the addresses; without one, nothing changes.
func TestCodeClaimNamesCountriesFromALocalTable(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	WithCountries(fakeCountries{"192.0.2.1": "PY", "192.168.100.5": "AR"}, CountryCredit{Text: "IP Geolocation by DB-IP", URL: "https://db-ip.com"})(h)
	issued := issueCode(t, h, "bootstrap", "")
	claimed := claimCode(t, rl, issued.code)
	if claimed["browser_country"] != "PY" || claimed["from_country"] != "AR" || claimed["country_credit"] != "IP Geolocation by DB-IP" {
		t.Fatalf("claim = %v, want the countries from the table", claimed)
	}
	if seen := watch(t, h, issued.poll); seen["country"] != "PY" {
		t.Fatalf("terminal sees %v", seen)
	}

	h, rl, _ = loginHostHandler(t, true)
	claimed = claimCode(t, rl, issueCode(t, h, "bootstrap", "").code)
	if _, ok := claimed["browser_country"]; ok {
		t.Fatalf("a country appeared without a table: %v", claimed)
	}
}

// Flags come from the login host itself while a code is live, and only a
// two-letter name reaches the flag files. The files are not in Git, so the
// test supplies its own.
func TestFlagsAreServedLocallyByTwoLetterName(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	h.flags = fstest.MapFS{
		"py.svg":  {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"PY.svg":  {Data: []byte(`<svg/>`)},
		"pry.svg": {Data: []byte(`<svg/>`)},
		"LICENSE": {Data: []byte(`MIT`)},
	}
	if handled, _ := atCode(rl, http.MethodGet, codePath+"/flags/py.svg", "", "", nil); handled {
		t.Fatal("a flag was served with no code issued")
	}
	issueCode(t, h, "bootstrap", "")
	handled, rec := atCode(rl, http.MethodGet, codePath+"/flags/py.svg", "", "", nil)
	if !handled || rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" ||
		!strings.Contains(rec.Body.String(), "<svg") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("py.svg: handled %v, %d %q", handled, rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, name := range []string{"PY.svg", "zz.svg", "pry.svg", "py.png", "../admin-code.js", "..%2fadmin-code.js", "LICENSE", "py"} {
		if _, rec := atCode(rl, http.MethodGet, codePath+"/flags/"+name, "", "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("flags/%s = %d, want 404", name, rec.Code)
		}
	}
}

// A newer recovery for the same administrator, by code or by link, ends the
// earlier recovery code; and once the first administrator exists, other
// first-run codes end too. Their terminals see why instead of waiting.
func TestCodesThatCanNoLongerSucceedAreReplaced(t *testing.T) {
	h, rl, _ := loginHostHandler(t, true)
	spare := issueCode(t, h, "bootstrap", "")
	first := issueCode(t, h, "bootstrap", "")
	createOnPage(t, rl, claimCode(t, rl, first.code)["claim"].(string), map[string]string{"name": "Ana", "email": "ana@example.com"}, newAuthenticator(t))
	if seen := watch(t, h, spare.poll); seen["state"] != "replaced" {
		t.Fatalf("a spare first-run code is %v after the first administrator", seen["state"])
	}

	older := issueCode(t, h, "recover", "ana@example.com")
	newer := issueCode(t, h, "recover", "ana@example.com")
	if seen := watch(t, h, older.poll); seen["state"] != "replaced" {
		t.Fatalf("the older recovery code is %v", seen["state"])
	}
	if seen := watch(t, h, newer.poll); seen["state"] != "pending" {
		t.Fatalf("the newer recovery code is %v", seen["state"])
	}
	if _, status := hostEnrolment(t, h, "recover", "ana@example.com"); status != http.StatusCreated {
		t.Fatalf("recovery link = %d", status)
	}
	if seen := watch(t, h, newer.poll); seen["state"] != "replaced" {
		t.Fatalf("a recovery link left the code %v", seen["state"])
	}
}
