package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/githubapp"
	"github.com/ozal-project/ozal/internal/pages/workflow"
)

type workflowProposer interface {
	OpenWorkflow(context.Context, config.GitHubApp, githubapp.Repository, workflow.Plan) (githubapp.PullRequest, error)
}

type workflowInput struct {
	Directory string `json:"directory"`
	Output    string `json:"output"`
	Hostname  string `json:"hostname"`
	Page      int    `json:"page"`
	Expected  string `json:"expected"`
	Digest    string `json:"digest"`
	Apply     bool   `json:"apply"`
}

func (h *Handler) projectWorkflow(w http.ResponseWriter, r *http.Request) {
	var req workflowInput
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid workflow request")
		return
	}
	// The normal admin write timeout is ten seconds. GitHub requires several
	// bounded requests; keep the response writable for this operation's minute.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(65 * time.Second))
	proposer, ok := h.github.(workflowProposer)
	if !ok {
		writeError(w, 503, "GitHub workflow proposals are unavailable")
		return
	}
	expected := h.manager.ConfigDigest()
	cfg := h.manager.Config()
	if req.Expected != "" && req.Expected != expected || req.Apply && req.Expected == "" {
		writeError(w, 409, "configuration changed; review the workflow again")
		return
	}
	if !cfg.GitHub.Installed() || cfg.Pages == nil || cfg.Pages.Deploy == nil {
		writeError(w, 422, "configure the GitHub App and dedicated HTTPS deploy endpoint on the host first")
		return
	}
	var project config.Project
	for _, p := range cfg.Projects {
		if p.Name == r.PathValue("name") {
			project = p
			break
		}
	}
	if project.Name == "" {
		writeError(w, 404, "project not found")
		return
	}
	var trust *config.ProjectGitHubTrust
	for _, d := range project.Deployers {
		if d.GitHub != nil {
			if trust != nil {
				writeError(w, 422, "choose a project with exactly one GitHub publisher")
				return
			}
			trust = d.GitHub
		}
	}
	if trust == nil {
		writeError(w, 422, "bind a GitHub publisher before preparing its workflow")
		return
	}
	if req.Page == 0 {
		req.Page = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	listing, err := h.github.Repositories(ctx, *cfg.GitHub, req.Page)
	if err != nil {
		writeError(w, 503, "GitHub repositories could not be verified")
		return
	}
	var selected githubapp.Repository
	for _, repository := range listing.Repositories {
		if repository.ID == trust.RepositoryID && repository.Owner.ID == trust.RepositoryOwnerID {
			selected = repository
			break
		}
	}
	if selected.ID == 0 {
		writeError(w, 403, "publisher repository is not available on this installation page")
		return
	}
	prefix := selected.FullName + "/.github/workflows/"
	suffix := "@" + trust.Ref
	if !strings.HasPrefix(trust.WorkflowRef, prefix) || !strings.HasSuffix(trust.WorkflowRef, suffix) {
		writeError(w, 409, "repository name no longer matches publisher trust; reconcile trust on the host")
		return
	}
	filename := strings.TrimSuffix(strings.TrimPrefix(trust.WorkflowRef, prefix), suffix)
	host := ""
	for _, hostname := range project.Hostnames {
		if req.Hostname != "" && req.Hostname != hostname {
			continue
		}
		for _, route := range cfg.Routes {
			if route.Host == hostname && route.Site == project.Site && !route.AuthRequired && route.Access == nil {
				host = hostname
				break
			}
		}
		if host != "" {
			break
		}
	}
	if host == "" {
		writeError(w, 422, "public acceptance requires a public hostname of this project; protected sites need a separately reviewed acceptance workflow")
		return
	}
	plan, status, message := publishingPlan(cfg, project.Name, selected, trust.Ref, filename, host, req.Directory, req.Output)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	if h.manager.ConfigDigest() != expected {
		writeError(w, 409, "configuration changed; review the workflow again")
		return
	}
	if !req.Apply {
		writeJSON(w, 200, map[string]any{"plan": plan, "expected": expected, "applied": false})
		return
	}
	if req.Digest == "" || req.Digest != plan.Digest {
		writeError(w, 409, "workflow content changed; review the generated file again")
		return
	}
	// Discovery may have taken seconds. A session revoked or expired while it
	// ran must not start a write using its old authority.
	h.requireTool("publishing", func(w http.ResponseWriter, r *http.Request) {
		if !h.recordMutationIntent(w, r, "github.workflow_propose", project.Name) {
			return
		}
		result, err := proposer.OpenWorkflow(ctx, *cfg.GitHub, selected, plan)
		if err != nil {
			status, message := 503, "GitHub workflow proposal could not be confirmed; inspect GitHub before retrying"
			if errors.Is(err, githubapp.ErrWorkflowExists) || errors.Is(err, githubapp.ErrBranchExists) {
				status, message = 409, err.Error()
			}
			writeJSON(w, status, map[string]any{"error": message, "branch": plan.Branch, "repository": plan.Repository})
			return
		}
		h.emit(events.Event{Type: events.GitHubWorkflowProposed, Actor: actor(r), Host: project.Name, Code: "github.workflow_proposed", Reason: "GitHub accepted a publishing workflow pull request; review before merging"})
		writeJSON(w, 201, map[string]any{"pull_request": result, "applied": true})
	})(w, r)
}

// publishingPlan generates the workflow for one repository, branch and public
// hostname. The wizard's preview and the pull request both use it, so the
// digest an operator reviewed is the one later applied.
func publishingPlan(cfg config.Config, project string, selected githubapp.Repository, ref, filename, host, directory, output string) (workflow.Plan, int, string) {
	if cfg.Pages == nil || cfg.Pages.Deploy == nil {
		return workflow.Plan{}, 422, "configure the dedicated HTTPS deploy endpoint on the host first"
	}
	endpoint := "https://" + cfg.Pages.Deploy.Host
	if cfg.Pages.Deploy.Audience != endpoint {
		return workflow.Plan{}, 422, "the generated publisher requires an OIDC audience equal to its HTTPS deploy origin; use a manually reviewed workflow for a custom audience"
	}
	plan, err := workflow.Build(workflow.Request{Project: project, Repository: selected.FullName, Ref: ref, Filename: filename, Endpoint: endpoint, SiteURL: "https://" + host, Directory: directory, Output: output})
	if err != nil {
		return workflow.Plan{}, 422, err.Error()
	}
	return plan, 0, ""
}
