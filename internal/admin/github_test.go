package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/githubapp"
)

type testRepositories struct {
	listing githubapp.Listing
	err     error
}

func (s testRepositories) Repositories(context.Context, config.GitHubApp, int) (githubapp.Listing, error) {
	return s.listing, s.err
}

type projectManager struct {
	*fakeManager
	saved bool
}

func (m *projectManager) PutProject(p config.Project, expected string, remove bool) error {
	m.saved = true
	if remove {
		m.cfg.Projects = nil
	} else {
		m.cfg.Projects = []config.Project{p}
	}
	return nil
}

func TestProjectTrustUsesInstallationRepositoryAndOwnerIDs(t *testing.T) {
	manager := &projectManager{fakeManager: &fakeManager{cfg: config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: "secret", SitesToken: "publisher"}, Pages: &config.Pages{Dir: t.TempDir()}, GitHub: &config.GitHubApp{AppID: 1, InstallationID: 2, PrivateKey: "file:/private/reference"}, Routes: []config.Route{{Host: "blog.localhost", Site: "blog"}}}}}
	repo := githubapp.Repository{ID: 123, FullName: "acme/blog", DefaultBranch: "main"}
	repo.Owner.ID = 456
	reader := testRepositories{listing: githubapp.Listing{Repositories: []githubapp.Repository{repo}}}
	handler := New(manager, nil, WithGitHubRepositories(reader))
	body := `{"name":"blog","site":"blog","repository_id":123,"page":1,"ref":"refs/heads/main","workflow":"deploy.yml","expected":"` + manager.ConfigDigest() + `"}`
	for _, token := range []string{"", "publisher"} {
		if rec := request(handler, http.MethodPost, "/_/api/projects", token, body); rec.Code != 401 {
			t.Fatalf("non-admin trust write: %d", rec.Code)
		}
	}
	if rec := request(handler, http.MethodPost, "/_/api/projects", "secret", body); rec.Code != 200 {
		t.Fatalf("bind project: %d %s", rec.Code, rec.Body)
	}
	trust := manager.cfg.Projects[0].Deployers[0].GitHub
	if trust.RepositoryID != 123 || trust.RepositoryOwnerID != 456 || trust.WorkflowRef != "acme/blog/.github/workflows/deploy.yml@refs/heads/main" {
		t.Fatal("trusted unverified repository metadata")
	}
	manager.saved = false
	if rec := request(handler, http.MethodPost, "/_/api/projects", "secret", strings.Replace(body, ":123", ":999", 1)); rec.Code != 403 || manager.saved {
		t.Fatal("uninstalled repo accepted")
	}
	if rec := request(handler, http.MethodPut, "/_/api/github", "secret", `{}`); rec.Code != 403 {
		t.Fatal("bearer changed GitHub key source")
	}
	if rec := request(handler, http.MethodGet, "/_/api/github/repositories", "secret", ""); strings.Contains(rec.Body.String(), "/private/reference") {
		t.Fatal("discovery exposed key reference")
	}
}
