package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/githubapp"
	"github.com/ozal-project/ozal/internal/pages/workflow"
)

type workflowRepositories struct {
	testRepositories
	writes    int
	plan      workflow.Plan
	writeErr  error
	afterRead func()
}

func TestWorkflowRechecksAuthorityAfterDiscovery(t *testing.T) {
	h, m, reader, recorder := workflowFixture(t)
	token, grant, err := h.operators.issue("Publisher", false, "publishing")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := workflow.Build(workflow.Request{Project: "blog", Repository: "octavia/website", Ref: "refs/heads/main", Filename: "ozal.yml", Endpoint: "https://deploy.example.test", SiteURL: "https://blog.example.test", Directory: ".", Output: "dist"})
	reader.afterRead = func() { h.operators.revoke(grant.ID) }
	body, _ := json.Marshal(workflowInput{Directory: ".", Output: "dist", Expected: m.ConfigDigest(), Digest: plan.Digest, Apply: true})
	rec := request(h, http.MethodPost, "/_/api/projects/blog/workflow", token, string(body))
	if rec.Code != 401 || reader.writes != 0 || len(recorder.Events()) != 0 {
		t.Fatalf("revoked publisher wrote: %d %s writes=%d", rec.Code, rec.Body, reader.writes)
	}
}

func TestWorkflowExtendsTheAdminWriteDeadline(t *testing.T) {
	h, _, reader, _ := workflowFixture(t)
	reader.afterRead = func() { time.Sleep(150 * time.Millisecond) }
	server := httptest.NewUnstartedServer(h)
	server.Config.WriteTimeout = 30 * time.Millisecond
	server.Start()
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/_/api/projects/blog/workflow", strings.NewReader(`{"directory":".","output":"dist"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer admin-test")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GitHub operation outlived the short admin deadline: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("workflow preview: HTTP %d", response.StatusCode)
	}
}

func (s *workflowRepositories) Repositories(ctx context.Context, cfg config.GitHubApp, page int) (githubapp.Listing, error) {
	if s.afterRead != nil {
		s.afterRead()
	}
	return s.testRepositories.Repositories(ctx, cfg, page)
}

func (s *workflowRepositories) OpenWorkflow(_ context.Context, _ config.GitHubApp, selected githubapp.Repository, plan workflow.Plan) (githubapp.PullRequest, error) {
	s.writes++
	s.plan = plan
	return githubapp.PullRequest{URL: "https://github.com/octavia/website/pull/19", Number: 19, Branch: plan.Branch}, s.writeErr
}

func workflowFixture(t *testing.T) (*Handler, *fakeManager, *workflowRepositories, *events.Recorder) {
	t.Helper()
	repo := githubapp.Repository{ID: 123, FullName: "octavia/website", DefaultBranch: "main"}
	repo.Owner.ID = 456
	reader := &workflowRepositories{testRepositories: testRepositories{listing: githubapp.Listing{Repositories: []githubapp.Repository{repo}}}}
	manager := &fakeManager{cfg: config.Config{Listen: "127.0.0.1:8888", Admin: config.Admin{Token: "admin-test", SitesToken: "sites-test"}, GitHub: &config.GitHubApp{AppID: 7, InstallationID: 42, PrivateKey: "file:/private/reference"}, Pages: &config.Pages{Dir: t.TempDir(), Deploy: &config.DeployEndpoint{Host: "deploy.example.test", Audience: "https://deploy.example.test"}}, Routes: []config.Route{{Host: "blog.example.test", Site: "blog"}}, Projects: []config.Project{{Name: "blog", Site: "blog", Hostnames: []string{"blog.example.test"}, Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 456, Ref: "refs/heads/main", WorkflowRef: "octavia/website/.github/workflows/ozal.yml@refs/heads/main"}}}}}}}
	recorder := &events.Recorder{}
	return New(manager, recorder, WithGitHubRepositories(reader)), manager, reader, recorder
}

func TestWorkflowPlanThenExplicitReviewedWrite(t *testing.T) {
	h, manager, reader, recorder := workflowFixture(t)
	token, _, err := h.operators.issue("Publishing operator", false, "publishing")
	if err != nil {
		t.Fatal(err)
	}
	rec := request(h, http.MethodPost, "/_/api/projects/blog/workflow", token, `{"directory":"web/site","output":"dist"}`)
	if rec.Code != 200 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	var result struct {
		Plan     workflow.Plan `json:"plan"`
		Expected string        `json:"expected"`
		Applied  bool          `json:"applied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Applied || result.Expected != manager.ConfigDigest() || reader.writes != 0 || len(recorder.Events()) != 0 || strings.Contains(rec.Body.String(), "/private/reference") {
		t.Fatal("planning wrote data, exposed a secret reference or emitted success")
	}
	body, _ := json.Marshal(workflowInput{Directory: "web/site", Output: "dist", Expected: result.Expected, Digest: result.Plan.Digest, Apply: true})
	rec = request(h, http.MethodPost, "/_/api/projects/blog/workflow", token, string(body))
	if rec.Code != 201 || reader.writes != 1 || reader.plan != result.Plan || !recorder.Has(events.GitHubWorkflowProposed) {
		t.Fatalf("proposal: %d %s writes=%d", rec.Code, rec.Body, reader.writes)
	}
	if manager.cfg.Projects[0].Deployers[0].GitHub.WorkflowRef != "octavia/website/.github/workflows/ozal.yml@refs/heads/main" {
		t.Fatal("proposing a workflow changed publisher trust")
	}
}

func TestWorkflowRefusalsNeverWriteOrEmitSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Handler, *fakeManager, *workflowRepositories, *workflowInput)
		code   int
	}{
		{"no digest", func(_ *Handler, _ *fakeManager, _ *workflowRepositories, r *workflowInput) { r.Digest = "" }, 409},
		{"wrong digest", func(_ *Handler, _ *fakeManager, _ *workflowRepositories, r *workflowInput) { r.Digest = "unreviewed" }, 409},
		{"edited output", func(_ *Handler, _ *fakeManager, _ *workflowRepositories, r *workflowInput) { r.Output = "other" }, 409},
		{"no revision", func(_ *Handler, _ *fakeManager, _ *workflowRepositories, r *workflowInput) { r.Expected = "" }, 409},
		{"stale revision", func(_ *Handler, _ *fakeManager, _ *workflowRepositories, r *workflowInput) { r.Expected = "old" }, 409},
		{"removed repo", func(_ *Handler, _ *fakeManager, s *workflowRepositories, _ *workflowInput) {
			s.listing.Repositories = nil
		}, 403},
		{"moved owner", func(_ *Handler, _ *fakeManager, s *workflowRepositories, _ *workflowInput) {
			s.listing.Repositories[0].Owner.ID++
		}, 403},
		{"renamed repo", func(_ *Handler, _ *fakeManager, s *workflowRepositories, _ *workflowInput) {
			s.listing.Repositories[0].FullName = "octavia/renamed"
		}, 409},
		{"disconnected App", func(_ *Handler, m *fakeManager, _ *workflowRepositories, _ *workflowInput) { m.cfg.GitHub = nil }, 422},
		{"no endpoint", func(_ *Handler, m *fakeManager, _ *workflowRepositories, _ *workflowInput) { m.cfg.Pages.Deploy = nil }, 422},
		{"custom audience", func(_ *Handler, m *fakeManager, _ *workflowRepositories, _ *workflowInput) {
			m.cfg.Pages.Deploy.Audience = "different-audience"
		}, 422},
		{"foreign host", func(_ *Handler, _ *fakeManager, _ *workflowRepositories, r *workflowInput) {
			r.Hostname = "other.example.test"
		}, 422},
		{"private host", func(_ *Handler, m *fakeManager, _ *workflowRepositories, _ *workflowInput) {
			m.cfg.Routes[0].Access = &config.AccessPolicy{}
		}, 422},
		{"legacy private host", func(_ *Handler, m *fakeManager, _ *workflowRepositories, _ *workflowInput) {
			m.cfg.Routes[0].AuthRequired = true
		}, 422},
		{"tag trust", func(_ *Handler, m *fakeManager, _ *workflowRepositories, _ *workflowInput) {
			trust := m.cfg.Projects[0].Deployers[0].GitHub
			trust.Ref = "refs/tags/v1"
			trust.WorkflowRef = "octavia/website/.github/workflows/ozal.yml@refs/tags/v1"
		}, 422},
		{"config changes during discovery", func(_ *Handler, m *fakeManager, s *workflowRepositories, _ *workflowInput) {
			s.afterRead = func() { m.cfg.Listen = "127.0.0.1:9999" }
		}, 409},
		{"audit unavailable", func(h *Handler, _ *fakeManager, _ *workflowRepositories, _ *workflowInput) {
			h.auditMutationsRequired = true
		}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m, reader, events := workflowFixture(t)
			plan, err := workflow.Build(workflow.Request{Project: "blog", Repository: "octavia/website", Ref: "refs/heads/main", Filename: "ozal.yml", Endpoint: "https://deploy.example.test", SiteURL: "https://blog.example.test", Directory: ".", Output: "dist"})
			if err != nil {
				t.Fatal(err)
			}
			r := workflowInput{Directory: ".", Output: "dist", Expected: m.ConfigDigest(), Digest: plan.Digest, Apply: true}
			tc.change(h, m, reader, &r)
			body, _ := json.Marshal(r)
			rec := request(h, http.MethodPost, "/_/api/projects/blog/workflow", "admin-test", string(body))
			if rec.Code != tc.code || reader.writes != 0 || len(events.Events()) != 0 {
				t.Fatalf("refusal: %d %s writes=%d events=%v", rec.Code, rec.Body, reader.writes, events.Events())
			}
		})
	}
}

func TestWorkflowRequiresPublishingAuthorityAndRedactsProviderErrors(t *testing.T) {
	h, m, reader, recorder := workflowFixture(t)
	people, _, _ := h.operators.issue("People operator", false, "people")
	for token, want := range map[string]int{"": 401, "sites-test": 401, people: 403} {
		if rec := request(h, http.MethodPost, "/_/api/projects/blog/workflow", token, `{}`); rec.Code != want {
			t.Fatalf("authority: %d want %d", rec.Code, want)
		}
	}
	plan, _ := workflow.Build(workflow.Request{Project: "blog", Repository: "octavia/website", Ref: "refs/heads/main", Filename: "ozal.yml", Endpoint: "https://deploy.example.test", SiteURL: "https://blog.example.test", Directory: ".", Output: "dist"})
	reader.writeErr = errors.New("provider contains credential and /private/path")
	body, _ := json.Marshal(workflowInput{Directory: ".", Output: "dist", Expected: m.ConfigDigest(), Digest: plan.Digest, Apply: true})
	rec := request(h, http.MethodPost, "/_/api/projects/blog/workflow", "admin-test", string(body))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "credential") || strings.Contains(rec.Body.String(), "/private/path") || recorder.Has(events.GitHubWorkflowProposed) || !strings.Contains(rec.Body.String(), plan.Branch) {
		t.Fatalf("provider failure: %d %s", rec.Code, rec.Body)
	}
}
