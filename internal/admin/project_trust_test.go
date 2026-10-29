package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

type trustMigrationManager struct {
	*fakeManager
	writes int
	fail   bool
}

func (m *trustMigrationManager) PutProject(p config.Project, expected string, remove bool) error {
	if m.fail {
		return errors.New("private storage details")
	}
	m.writes++
	m.cfg.Projects[0] = p
	return nil
}

func TestExplicitProjectTrustRequiresHostAndReviewedDigest(t *testing.T) {
	old := config.ProjectGitHubTrust{RepositoryID: 1, RepositoryOwnerID: 2, Ref: "refs/heads/main", WorkflowRef: "old/repo/.github/workflows/site.yml@refs/heads/main"}
	next := config.ProjectGitHubTrust{RepositoryID: 3, RepositoryOwnerID: 4, Ref: old.Ref, WorkflowRef: "new/repo/.github/workflows/site.yml@refs/heads/main"}
	m := &trustMigrationManager{fakeManager: &fakeManager{cfg: config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: "admin", SitesToken: "publisher"},
		Pages: &config.Pages{Dir: t.TempDir()}, Routes: []config.Route{{Host: "site.localhost", Site: "site"}},
		Projects: []config.Project{{Name: "site", Site: "site", Hostnames: []string{"site.localhost"}, Deployers: []config.ProjectDeployer{{GitHub: &old}, {Token: "file:/private/reference"}}}}}}}
	h := New(m, nil)
	endpoint := "/_/api/projects/site/github-trust"
	for _, token := range []string{"", "admin", "publisher"} {
		if rec := request(h, http.MethodPut, endpoint, token, "not JSON"); rec.Code != 403 || m.writes != 0 {
			t.Fatal("TCP credential reached host-only trust migration")
		}
	}
	payload := map[string]any{"trust": next, "expected": m.ConfigDigest(), "apply": false}
	if rec := local(h, http.MethodPut, endpoint, jsonBody(t, payload)); rec.Code != 200 || m.writes != 0 || strings.Contains(rec.Body.String(), "/private/") {
		t.Fatalf("plan mutated or leaked reference: %d %s", rec.Code, rec.Body)
	}
	payload["apply"], payload["expected"] = true, "stale"
	if rec := local(h, http.MethodPut, endpoint, jsonBody(t, payload)); rec.Code != 409 || m.writes != 0 {
		t.Fatal("stale trust write accepted")
	}
	payload["expected"] = m.ConfigDigest()
	m.fail = true
	if rec := local(h, http.MethodPut, endpoint, jsonBody(t, payload)); rec.Code != 409 || strings.Contains(rec.Body.String(), "private storage") || *m.cfg.Projects[0].Deployers[0].GitHub != old {
		t.Fatal("failed persistence changed authority or exposed raw error")
	}
	m.fail = false
	if rec := local(h, http.MethodPut, endpoint, jsonBody(t, payload)); rec.Code != 200 || m.writes != 1 || *m.cfg.Projects[0].Deployers[0].GitHub != next || m.cfg.Projects[0].Deployers[1].Token != "file:/private/reference" {
		t.Fatalf("host migration failed: %d %s", rec.Code, rec.Body)
	}
	payload["expected"] = m.ConfigDigest()
	if rec := local(h, http.MethodPut, endpoint, jsonBody(t, payload)); rec.Code != 200 || m.writes != 1 {
		t.Fatal("retry wrote matching trust again")
	}
}
