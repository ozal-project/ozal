package admin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

func TestProjectsAPIRequiresAdminBearerAndRedactsTokenReferences(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: "admin-secret", SitesToken: "sites-secret"},
		Projects: []config.Project{{
			Name: "portfolio", Site: "portfolio", Hostnames: []string{"portfolio.example"},
			Deployers: []config.ProjectDeployer{
				{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 456, Ref: "refs/heads/main", WorkflowRef: "octavio/site/.github/workflows/deploy.yml@refs/heads/main"}},
				{Token: "file:/etc/switchboard/portfolio-deploy.token"},
				{Token: "inline-secret-must-not-appear"},
			},
		}},
	}}
	handler := New(manager, nil)

	for _, token := range []string{"", "sites-secret"} {
		rec := request(handler, http.MethodGet, "/_/api/projects", token, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q status = %d, want 401", token, rec.Code)
		}
	}

	rec := request(handler, http.MethodGet, "/_/api/projects", "admin-secret", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"portfolio.example", `"repository_id":123`, `"repository_owner_id":456`, `"refs/heads/main"`, `"token_configured":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("project response missing %q: %s", want, body)
		}
	}
	for _, secret := range []string{"file:/etc/switchboard/portfolio-deploy.token", "/etc/switchboard", "inline-secret-must-not-appear", "admin-secret", "sites-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("project response exposed %q: %s", secret, body)
		}
	}
}
