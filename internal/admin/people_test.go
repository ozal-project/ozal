package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

func peopleHandler(t *testing.T) (http.Handler, *sqlite.Identity, *events.Recorder) {
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
	manager := &fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: "secret"},
		Auth:  config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Routes: []config.Route{
			{Host: "private.example.com", Access: &config.AccessPolicy{}},
			{Host: "www.example.com"},
		},
	}}
	recorder := &events.Recorder{}
	return New(manager, recorder, WithPeople(identity)), identity, recorder
}

// The console does what `switchboard user` does: invite with a link that
// only the admin token can obtain, list, disable and enable.
func TestPeopleInviteListDisable(t *testing.T) {
	handler, identity, recorder := peopleHandler(t)

	invite := `{"email":"Ana@Example.com","name":"Ana","groups":["owner"," "],"return_to":"https://private.example.com/notes"}`
	if rec := request(handler, http.MethodPost, "/_/api/people/invite", "", invite); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invite without token = %d", rec.Code)
	}
	rec := request(handler, http.MethodPost, "/_/api/people/invite", "secret", invite)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Email string `json:"email"`
		Link  string `json:"link"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Email != "ana@example.com" || !strings.HasPrefix(created.Link, "https://auth.example.com/_/auth/enrol?return_to=https%3A%2F%2Fprivate.example.com%2Fnotes#") {
		t.Fatalf("created = %+v", created)
	}
	token := created.Link[strings.Index(created.Link, "#")+1:]
	if len(token) < 20 {
		t.Fatalf("link token %q", token)
	}
	for _, e := range recorder.Events() {
		if strings.Contains(e.Reason+e.Host+e.Code+e.Actor, token) {
			t.Fatal("the invitation token reached an event")
		}
	}
	if _, ok := recorder.Find(events.UserInvited); !ok {
		t.Fatal("no user.invited event")
	}

	bad := `{"email":"eve@example.com","return_to":"https://evil.example/"}`
	if rec := request(handler, http.MethodPost, "/_/api/people/invite", "secret", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("invite to an unprotected destination = %d", rec.Code)
	}
	if users, _ := identity.Users(context.Background()); len(users) != 1 {
		t.Fatalf("a refused invitation created a user: %d users", len(users))
	}

	rec = request(handler, http.MethodGet, "/_/api/people", "", "")
	var listing struct {
		Enabled bool `json:"enabled"`
		Users   []struct {
			Email  string   `json:"email"`
			Groups []string `json:"groups"`
		} `json:"users"`
		Protected []string `json:"protected_hosts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listing)
	if rec.Code != http.StatusOK || !listing.Enabled || len(listing.Users) != 1 || listing.Users[0].Email != "ana@example.com" ||
		len(listing.Users[0].Groups) != 1 || len(listing.Protected) != 1 || listing.Protected[0] != "private.example.com" {
		t.Fatalf("listing = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("the listing exposes the invitation token")
	}

	if rec := request(handler, http.MethodPost, "/_/api/people/ana@example.com/disable", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disable without token = %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/people/ana@example.com/disable", "secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	if users, _ := identity.Users(context.Background()); users[0].DisabledAt.IsZero() {
		t.Fatal("user not disabled")
	}
	if rec := request(handler, http.MethodPost, "/_/api/people/nobody@example.com/disable", "secret", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("disable unknown = %d", rec.Code)
	}
	if rec := request(handler, http.MethodPost, "/_/api/people/ana@example.com/passkeys/abcd/revoke", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoke without token = %d", rec.Code)
	}
}

// Recovery adds a passkey to an existing account, the path a takeover would
// take: it stays on the host's CLI, never on the admin bearer.
func TestRecoveryIsRefusedOnTheAdminAPI(t *testing.T) {
	handler, identity, recorder := peopleHandler(t)
	ctx := context.Background()
	for _, body := range []string{
		`{"email":"ana@example.com","recover":true}`,
		`{"email":"nobody@example.com","name":"X","groups":["owner"],"recover":true}`,
	} {
		rec := request(handler, http.MethodPost, "/_/api/people/invite", "secret", body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "switchboard user invite -recover") {
			t.Fatalf("recover over the API = %d %s, want 403 pointing at the host CLI", rec.Code, rec.Body)
		}
	}
	if users, err := identity.Users(ctx); err != nil || len(users) != 0 {
		t.Fatalf("users = %v, %v: a refused recovery must create nothing", users, err)
	}
	if len(recorder.Events()) != 0 {
		t.Fatalf("events = %+v, want none", recorder.Events())
	}
}

// On the admin unix socket the shell is already the credential: recovery
// is accepted there and its event says so.
func TestRecoveryIsAcceptedOnTheSocket(t *testing.T) {
	handler, _, recorder := peopleHandler(t)
	if rec := request(handler, http.MethodPost, "/_/api/people/invite", "secret", `{"email":"ana@example.com","groups":["owner"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("first invite = %d %s", rec.Code, rec.Body)
	}
	req := localRequest(http.MethodPost, "/_/api/people/invite")
	req.Body = io.NopCloser(strings.NewReader(`{"email":"ana@example.com","recover":true}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "/_/auth/enrol") {
		t.Fatalf("recover on the socket = %d %s", rec.Code, rec.Body)
	}
	got := recorder.Events()
	last := got[len(got)-1]
	if last.Type != events.UserInvited || last.Code != "recover" || last.Actor != "local" {
		t.Fatalf("event = %+v, want user.invited/recover by local", last)
	}
}

func TestInviteTTLIsBounded(t *testing.T) {
	handler, _, _ := peopleHandler(t)
	for _, ttl := range []string{"-1", "1441"} {
		rec := request(handler, http.MethodPost, "/_/api/people/invite", "secret", `{"email":"ana@example.com","ttl_minutes":`+ttl+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("ttl_minutes %s = %d, want 400", ttl, rec.Code)
		}
	}
}
