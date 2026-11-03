package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

func groupsHandler(t *testing.T) (*Handler, *sqlite.Identity) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	migrations, _ := sqlite.LoadMigrations(sqlite.Migrations())
	if _, err := sqlite.Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	identity := sqlite.NewIdentity(db)
	manager := &fakeManager{cfg: config.Config{
		Listen: "127.0.0.1:8888",
		Admin:  config.Admin{Token: "secret"},
		Auth:   config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Routes: []config.Route{{Host: "wiki.example.com", Target: "http://127.0.0.1:3000", Access: &config.AccessPolicy{RequiredGroups: []string{"members"}}}},
	}}
	return New(manager, &events.Recorder{}, WithPeople(identity), WithGroupCatalog(sqlite.NewGroupCatalog(db))), identity
}

func groupNamed(t *testing.T, h *Handler, name string) map[string]any {
	t.Helper()
	var body struct {
		Groups []map[string]any `json:"groups"`
	}
	_ = json.Unmarshal(request(h, http.MethodGet, "/_/api/groups", "secret", "").Body.Bytes(), &body)
	for _, g := range body.Groups {
		if g["name"] == name {
			return g
		}
	}
	return nil
}

// A group can be created before anyone is in it, so invitations and
// applications pick it from a fixed list; only an unused one is deleted.
func TestGroupsAreCreatedEmptyAndOnlyUnusedOnesDeleted(t *testing.T) {
	h, identity := groupsHandler(t)
	if rec := request(h, http.MethodPost, "/_/api/groups", "secret", `{"name":" ops "}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if g := groupNamed(t, h, "ops"); g == nil || g["defined"] != true || len(g["members"].([]any)) != 0 {
		t.Fatalf("ops = %v, want an empty defined group", g)
	}
	for name, body := range map[string]string{
		"again":              `{"name":"ops"}`,
		"required by an app": `{"name":"members"}`,
	} {
		if rec := request(h, http.MethodPost, "/_/api/groups", "secret", body); rec.Code != http.StatusConflict {
			t.Errorf("%s = %d, want 409", name, rec.Code)
		}
	}
	for _, bad := range []string{`{"name":""}`, `{"name":"a,b"}`, `{"name":"tab\there"}`} {
		if rec := request(h, http.MethodPost, "/_/api/groups", "secret", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
	sites, _, _ := h.operators.issue("Sites operator", false, "sites")
	if rec := request(h, http.MethodPost, "/_/api/groups", sites, `{"name":"x"}`); rec.Code != http.StatusForbidden {
		t.Errorf("without the people tool = %d, want 403", rec.Code)
	}

	if _, err := identity.Invite(context.Background(), "ana@example.com", "", []string{"ops"}, false, time.Hour); err != nil {
		t.Fatal(err)
	}
	if rec := request(h, http.MethodDelete, "/_/api/groups/ops", "secret", ""); rec.Code != http.StatusConflict {
		t.Fatalf("deleting a group with members = %d, want 409", rec.Code)
	}
	if rec := request(h, http.MethodDelete, "/_/api/groups/members", "secret", ""); rec.Code != http.StatusConflict {
		t.Fatalf("deleting a group an app requires = %d, want 409", rec.Code)
	}
	if rec := request(h, http.MethodPost, "/_/api/groups", "secret", `{"name":"spare"}`); rec.Code != http.StatusCreated {
		t.Fatal(rec.Code)
	}
	if rec := request(h, http.MethodDelete, "/_/api/groups/spare", "secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("deleting an unused group = %d %s", rec.Code, rec.Body)
	}
	if groupNamed(t, h, "spare") != nil {
		t.Fatal("deleted group still listed")
	}
	if rec := request(h, http.MethodDelete, "/_/api/groups/nothing", "secret", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown group = %d, want 404", rec.Code)
	}
}
