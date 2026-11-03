package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

// removingManager also removes applications, as the route manager does.
type removingManager struct{ *fakeManager }

func (m removingManager) RemoveApplication(host, _ string) error {
	routes := m.cfg.Routes[:0]
	for _, rt := range m.cfg.Routes {
		if rt.Host != host {
			routes = append(routes, rt)
		}
	}
	m.cfg.Routes = routes
	return nil
}

func namesHandler(t *testing.T) (*Handler, *sqlite.AppNames) {
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
	names := sqlite.NewAppNames(db)
	manager := removingManager{&fakeManager{cfg: config.Config{
		Listen: "127.0.0.1:8888",
		Admin:  config.Admin{Token: "secret"},
		Auth:   config.Auth{Passkeys: &config.Passkeys{RPID: "example.com", LoginURL: "https://auth.example.com"}},
		Routes: []config.Route{
			{Host: "wiki.example.com", Target: "http://127.0.0.1:3000", Access: &config.AccessPolicy{}},
			{Host: "www.example.com", Target: "http://127.0.0.1:3001"},
		},
	}}}
	return New(manager, &events.Recorder{}, WithPeople(sqlite.NewIdentity(db)), WithAppNames(names)), names
}

// An operator names an application; the name shows wherever the console
// lists it, only for configured hosts, and leaves with its route.
func TestApplicationNamesAreSetShownAndRemovedWithTheRoute(t *testing.T) {
	h, names := namesHandler(t)
	if rec := request(h, http.MethodPut, "/_/api/apps/WIKI.example.com/name", "secret", `{"name":" Team wiki "}`); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	if got, _ := names.Names(context.Background()); got["wiki.example.com"] != "Team wiki" {
		t.Fatalf("stored = %v, want the trimmed name under the normalized host", got)
	}
	for _, tc := range []struct {
		name, path, token, body string
		want                    int
	}{
		{"unknown host", "/_/api/apps/other.example.com/name", "secret", `{"name":"x"}`, http.StatusNotFound},
		{"line break", "/_/api/apps/wiki.example.com/name", "secret", `{"name":"two\nlines"}`, http.StatusBadRequest},
		{"no token", "/_/api/apps/wiki.example.com/name", "", `{"name":"x"}`, http.StatusUnauthorized},
	} {
		if rec := request(h, http.MethodPut, tc.path, tc.token, tc.body); rec.Code != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}

	var status struct {
		AppNames map[string]string `json:"app_names"`
	}
	_ = json.Unmarshal(request(h, http.MethodGet, "/_/api/setup", "secret", "").Body.Bytes(), &status)
	if status.AppNames["wiki.example.com"] != "Team wiki" {
		t.Fatalf("setup app_names = %v", status.AppNames)
	}
	var people struct {
		AppNames map[string]string `json:"app_names"`
		Scheme   string            `json:"scheme"`
	}
	_ = json.Unmarshal(request(h, http.MethodGet, "/_/api/people", "secret", "").Body.Bytes(), &people)
	if people.AppNames["wiki.example.com"] != "Team wiki" || len(people.AppNames) != 1 {
		t.Fatalf("people app_names = %v; want the protected app's name only", people.AppNames)
	}
	if people.Scheme != "http" {
		t.Fatalf("scheme = %q; an edge without TLS links its apps over http", people.Scheme)
	}

	rec := request(h, http.MethodDelete, "/_/api/apps/wiki.example.com", "secret", `{"expected":"`+h.manager.ConfigDigest()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if got, _ := names.Names(context.Background()); len(got) != 0 {
		t.Fatalf("names after removal = %v; the name leaves with its route", got)
	}
}
