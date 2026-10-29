package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ozal-project/ozal/internal/config"
)

func TestProjectsAPIRequiresAdminBearerAndRedactsTokenReferences(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{
		Admin: config.Admin{Token: "admin-secret", SitesToken: "sites-secret"},
		Projects: []config.Project{{
			Name: "portfolio", Site: "portfolio", Hostnames: []string{"portfolio.example"},
			Deployers: []config.ProjectDeployer{
				{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 456, Ref: "refs/heads/main", WorkflowRef: "octavio/site/.github/workflows/deploy.yml@refs/heads/main"}},
				{Token: "file:/etc/ozal/portfolio-deploy.token"},
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
	for _, secret := range []string{"file:/etc/ozal/portfolio-deploy.token", "/etc/ozal", "inline-secret-must-not-appear", "admin-secret", "sites-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("project response exposed %q: %s", secret, body)
		}
	}
}

func TestPublishingContextDoesNotRequireApplicationsOrExposeUpstreams(t *testing.T) {
	manager := &fakeManager{cfg: config.Config{Admin: config.Admin{Token: "admin-test"}, Routes: []config.Route{{Host: "z.example.test", Site: "z-site"}, {Host: "a.example.test", Site: "a-site"}, {Host: "alias.example.test", Site: "a-site"}, {Host: "private.example.test", Target: "http://private-upstream:8080", Access: &config.AccessPolicy{RequiredGroups: []string{"private-group"}}}}}}
	h := New(manager, nil)
	token, _, err := h.operators.issue("Publisher", false, "publishing")
	if err != nil {
		t.Fatal(err)
	}
	if rec := request(h, http.MethodGet, "/_/api/setup", token, ""); rec.Code != 403 {
		t.Fatal("publisher can read application details")
	}
	rec := request(h, http.MethodGet, "/_/api/projects", token, "")
	if rec.Code != 200 {
		t.Fatalf("publishing read: %d %s", rec.Code, rec.Body)
	}
	var context struct {
		Sites  []string `json:"sites"`
		Digest string   `json:"config_sha256"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &context); err != nil {
		t.Fatal(err)
	}
	if strings.Join(context.Sites, ",") != "a-site,z-site" || context.Digest != manager.ConfigDigest() {
		t.Fatalf("publishing context: %+v", context)
	}
	if strings.Contains(rec.Body.String(), "private-upstream") || strings.Contains(rec.Body.String(), "private-group") || strings.Contains(rec.Body.String(), "private.example.test") {
		t.Fatal("publishing context leaked unrelated application configuration")
	}
}
