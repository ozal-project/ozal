package pages

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

func migratedDB(t *testing.T) *sql.DB {
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
	return db
}

// newStore returns a store with a clock that advances one second per call,
// so deployment order is deterministic.
func newStore(t *testing.T, db *sql.DB, dir string, keep int) *Store {
	t.Helper()
	s, err := NewStore(context.Background(), db, dir, keep, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return s
}

func site(t *testing.T, body string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "site.tar.gz")
	if err := os.WriteFile(path, archive(t, entry{name: "index.html", body: body}).Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func liveIndex(t *testing.T, s *Store, name string) string {
	t.Helper()
	live, ok := s.Live(name)
	if !ok {
		t.Fatalf("site %q has no live deployment", name)
	}
	got, err := os.ReadFile(filepath.Join(live.Dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestDeployRollbackAndRestart(t *testing.T) {
	ctx := context.Background()
	db, dir := migratedDB(t), t.TempDir()
	s := newStore(t, db, dir, DefaultKeep)

	if _, ok := s.Live("blog"); ok {
		t.Fatal("a site nobody deployed must not be live")
	}
	first, err := s.Deploy(ctx, "blog", site(t, "v1"))
	if err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	second, err := s.Deploy(ctx, "blog", site(t, "v2"))
	if err != nil {
		t.Fatalf("deploy v2: %v", err)
	}
	if first.ID == second.ID || !second.Live || liveIndex(t, s, "blog") != "v2" {
		t.Fatalf("after two deploys live = %q, first=%+v second=%+v", liveIndex(t, s, "blog"), first, second)
	}

	back, err := s.Rollback(ctx, "blog", first.ID)
	if err != nil || !back.Live || liveIndex(t, s, "blog") != "v1" {
		t.Fatalf("rollback = %+v, %v; live %q", back, err, liveIndex(t, s, "blog"))
	}

	// A restart reloads the live pointer from the database.
	restarted := newStore(t, db, dir, DefaultKeep)
	if liveIndex(t, restarted, "blog") != "v1" {
		t.Fatal("restart lost the rolled-back deployment")
	}

	sites, err := restarted.Sites(ctx)
	if err != nil || len(sites) != 1 || sites[0].Live != first.ID || len(sites[0].Deployments) != 2 {
		t.Fatalf("Sites = %+v, %v", sites, err)
	}
	// No staging directory survives a deploy.
	entries, _ := os.ReadDir(filepath.Join(dir, "sites", "blog"))
	if len(entries) != 2 {
		t.Fatalf("site directory holds %d entries, want the 2 deployments", len(entries))
	}
}

func TestDeploymentProjectMetadataRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := migratedDB(t)
	s := newStore(t, db, t.TempDir(), DefaultKeep)
	deployed, err := s.Deploy(ctx, "portfolio", site(t, "build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE deployments SET project = ?, commit_sha = ?, run_url = ? WHERE site = ? AND id = ?`,
		"portfolio", "0123456789abcdef", "https://github.com/acme/site/actions/runs/42", deployed.Site, deployed.ID); err != nil {
		t.Fatalf("write deployment metadata: %v", err)
	}

	sites, err := s.Sites(ctx)
	if err != nil || len(sites) != 1 || len(sites[0].Deployments) != 1 {
		t.Fatalf("Sites = %+v, %v", sites, err)
	}
	got := sites[0].Deployments[0]
	if got.Project != "portfolio" || got.CommitSHA != "0123456789abcdef" || got.RunURL != "https://github.com/acme/site/actions/runs/42" {
		t.Fatalf("deployment metadata = %+v", got)
	}

	rolledBack, err := s.Rollback(ctx, deployed.Site, deployed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Project != got.Project || rolledBack.CommitSHA != got.CommitSHA || rolledBack.RunURL != got.RunURL {
		t.Fatalf("rollback response metadata = %+v, want %+v", rolledBack, got)
	}
}

func TestRedeployingTheSameBuildReusesIt(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, migratedDB(t), t.TempDir(), DefaultKeep)
	a, err := s.Deploy(ctx, "blog", site(t, "same"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deploy(ctx, "blog", site(t, "other")); err != nil {
		t.Fatal(err)
	}
	again, err := s.Deploy(ctx, "blog", site(t, "same"))
	if err != nil || again.ID != a.ID || !again.Live || !again.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("redeploy = %+v, %v; want the first deployment live again", again, err)
	}
}

func TestRefusedDeployLeavesTheLiveSiteAlone(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, migratedDB(t), t.TempDir(), DefaultKeep)
	if _, err := s.Deploy(ctx, "blog", site(t, "good")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Deploy(ctx, "blog", archive(t, entry{name: "index.html", body: "x"}, entry{name: "../evil", body: "x"}))
	var refusal *RefusedError
	if !errors.As(err, &refusal) || refusal.Code != UnsafePath {
		t.Fatalf("hostile deploy error = %v", err)
	}
	if liveIndex(t, s, "blog") != "good" {
		t.Fatal("a refused deploy changed the live site")
	}
}

func TestPruneKeepsTheNewestAndTheLive(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := newStore(t, migratedDB(t), dir, 2)
	var ids []string
	for _, body := range []string{"1", "2", "3", "4"} {
		d, err := s.Deploy(ctx, "blog", site(t, body))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	sites, _ := s.Sites(ctx)
	if len(sites[0].Deployments) != 2 || sites[0].Deployments[0].ID != ids[3] || sites[0].Deployments[1].ID != ids[2] {
		t.Fatalf("retained %+v, want the two newest", sites[0].Deployments)
	}
	if _, err := os.Stat(filepath.Join(dir, "sites", "blog", ids[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a pruned deployment's files are still on disk")
	}
	if _, err := s.Rollback(ctx, "blog", ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rollback to a pruned deployment: %v", err)
	}

	// Retention counts by age, not by what was live last: after a rollback,
	// the next deploy still keeps the newest previous deployment.
	if _, err := s.Rollback(ctx, "blog", ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deploy(ctx, "blog", site(t, "5")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rollback(ctx, "blog", ids[3]); err != nil {
		t.Fatalf("the previous newest should still be retained: %v", err)
	}
}

func TestSiteNamesAreDirectoryNames(t *testing.T) {
	s := newStore(t, migratedDB(t), t.TempDir(), DefaultKeep)
	for _, name := range []string{"", "..", "a/b", "Blog", "-blog", "blog-", "x.y"} {
		if _, err := s.Deploy(context.Background(), name, site(t, "x")); !errors.Is(err, ErrBadSiteName) {
			t.Errorf("Deploy(%q) error = %v, want ErrBadSiteName", name, err)
		}
	}
}

func TestSameSecondDeploysKeepTheirOrder(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, migratedDB(t), t.TempDir(), DefaultKeep)
	frozen := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return frozen }
	var ids []string
	for _, body := range []string{"zzz", "aaa", "mmm"} {
		d, err := s.Deploy(ctx, "blog", site(t, body))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	sites, err := s.Sites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range sites[0].Deployments {
		if d.ID != ids[len(ids)-1-i] {
			t.Fatalf("position %d is %s, want newest-first by upload order", i, d.ID[:8])
		}
	}
}

func TestDeployCarriesItsRules(t *testing.T) {
	ctx := context.Background()
	db, dir := migratedDB(t), t.TempDir()
	s := newStore(t, db, dir, DefaultKeep)

	// A typo in _headers fails the deploy and leaves the live site alone.
	bad := archive(t, entry{name: "index.html", body: "bad"}, entry{name: "_headers", body: "/*\n  Set-Cookie: s=1\n"})
	if _, err := s.Deploy(ctx, "blog", bad); !isRefusal(err, RulesInvalid) {
		t.Fatalf("deploy with invalid rules: %v, want %s", err, RulesInvalid)
	}
	if _, ok := s.Live("blog"); ok {
		t.Fatal("a refused deploy must not become live")
	}

	withRules := archive(t, entry{name: "index.html", body: "v1"}, entry{name: "_redirects", body: "/old /new\n"})
	first, err := s.Deploy(ctx, "blog", withRules)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deploy(ctx, "blog", site(t, "v2")); err != nil {
		t.Fatal(err)
	}
	if live, _ := s.Live("blog"); hasRedirect(live, "/old") {
		t.Fatal("v2 has no _redirects: the v1 rule must be gone")
	}
	if _, err := s.Rollback(ctx, "blog", first.ID); err != nil {
		t.Fatal(err)
	}
	if live, _ := s.Live("blog"); !hasRedirect(live, "/old") {
		t.Fatal("rolling back to v1 must bring its rules back")
	}
	// After a restart the rules are read from disk again.
	if live, _ := newStore(t, db, dir, DefaultKeep).Live("blog"); !hasRedirect(live, "/old") {
		t.Fatal("rules must survive a restart")
	}
}

func hasRedirect(l Live, path string) bool {
	_, _, ok := l.Rules.Redirect(path)
	return ok
}

func isRefusal(err error, code Code) bool {
	var refusal *RefusedError
	return errors.As(err, &refusal) && refusal.Code == code
}

// BUG-012: once a deploy or rollback commits, the build is live, and the
// answer must say so even if the request context ends right then (a client
// that drops the connection, a deadline).
func TestASwitchThatCommittedNeverReportsAnError(t *testing.T) {
	db, dir := migratedDB(t), t.TempDir()
	s := newStore(t, db, dir, 5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.afterCommit = cancel

	first, err := s.Deploy(ctx, "blog", site(t, "one"))
	if err != nil {
		t.Fatalf("deploy committed but reported %v", err)
	}
	if !first.Live || first.Files != 1 || first.CreatedAt.IsZero() || liveIndex(t, s, "blog") != "one" {
		t.Fatalf("deploy answer %+v, live %q", first, liveIndex(t, s, "blog"))
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	s.afterCommit = cancel
	if _, err := s.Deploy(ctx, "blog", site(t, "two")); err != nil {
		t.Fatalf("second deploy committed but reported %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	s.afterCommit = cancel
	back, err := s.Rollback(ctx, "blog", first.ID)
	if err != nil {
		t.Fatalf("rollback committed but reported %v", err)
	}
	if back.ID != first.ID || !back.Live || back.CreatedAt != first.CreatedAt || liveIndex(t, s, "blog") != "one" {
		t.Fatalf("rollback answer %+v, live %q", back, liveIndex(t, s, "blog"))
	}
}
