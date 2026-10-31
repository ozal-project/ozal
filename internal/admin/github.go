package admin

import (
	"context"
	"net/http"
	"strconv"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/githubapp"
)

type repositoryReader interface {
	Repositories(context.Context, config.GitHubApp, int) (githubapp.Listing, error)
}

func WithGitHubRepositories(reader repositoryReader) Option {
	return func(h *Handler) { h.github = reader }
}

type projectWriter interface {
	PutProject(config.Project, string, bool) error
}

// githubRepositories says how far GitHub is connected, then lists the
// installation's repositories. It never returns the key or its reference.
func (h *Handler) githubRepositories(w http.ResponseWriter, r *http.Request) {
	cfg := h.manager.Config()
	_, canSetup := h.setupGitHub()
	state := map[string]any{
		"configured": cfg.GitHub != nil, "installed": cfg.GitHub.Installed(), "repositories": []any{},
		// The console can connect GitHub itself only with somewhere to keep the key.
		"connect_available": canSetup && h.githubKeyDir != "",
		// Ozal can write the publishing workflow only with an OIDC deploy endpoint.
		"workflow_available": cfg.Pages != nil && cfg.Pages.Deploy != nil && cfg.Pages.Deploy.Audience == "https://"+cfg.Pages.Deploy.Host,
	}
	if cfg.GitHub == nil {
		writeJSON(w, 200, state)
		return
	}
	if !cfg.GitHub.Installed() {
		setup, ok := h.setupGitHub()
		if !ok {
			writeError(w, 503, "GitHub discovery is unavailable")
			return
		}
		app, err := setup.AppStatus(r.Context(), *cfg.GitHub)
		if err != nil {
			writeError(w, 503, "GitHub could not be asked about the App; check its key and try again")
			return
		}
		state["app"], state["owner"], state["install_url"] = app.Slug, app.Owner, app.InstallURL()
		writeJSON(w, 200, state)
		return
	}
	if h.github == nil {
		writeError(w, 503, "GitHub discovery is unavailable")
		return
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 1000 {
			writeError(w, 400, "invalid repository page")
			return
		}
		page = p
	}
	listing, err := h.github.Repositories(r.Context(), *cfg.GitHub, page)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	state["repositories"], state["page"], state["more"] = listing.Repositories, listing.Page, listing.More
	writeJSON(w, 200, state)
}
func (h *Handler) saveGitHubProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		Site         string `json:"site"`
		RepositoryID int64  `json:"repository_id"`
		Page         int    `json:"page"`
		Ref          string `json:"ref"`
		Workflow     string `json:"workflow"`
		Expected     string `json:"expected"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid project request")
		return
	}
	cfg := h.manager.Config()
	if req.Expected == "" || req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; refresh before binding publisher trust")
		return
	}
	if !cfg.GitHub.Installed() || h.github == nil {
		writeError(w, 422, "connect GitHub and choose its repositories first")
		return
	}
	listing, err := h.github.Repositories(r.Context(), *cfg.GitHub, req.Page)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	var selected *githubapp.Repository
	for i := range listing.Repositories {
		if listing.Repositories[i].ID == req.RepositoryID {
			selected = &listing.Repositories[i]
			break
		}
	}
	if selected == nil {
		writeError(w, 403, "repository is no longer available to this installation")
		return
	}
	if !validWorkflowFile(req.Workflow) {
		writeError(w, 400, "workflow must be a .yml or .yaml filename")
		return
	}
	project := config.Project{Name: req.Name, Site: req.Site, Hostnames: []string{}, Deployers: []config.ProjectDeployer{{GitHub: &config.ProjectGitHubTrust{RepositoryID: selected.ID, RepositoryOwnerID: selected.Owner.ID, Ref: req.Ref, WorkflowRef: selected.FullName + "/.github/workflows/" + req.Workflow + "@" + req.Ref}}}}
	for _, route := range cfg.Routes {
		if route.Site == req.Site {
			project.Hostnames = append(project.Hostnames, route.Host)
		}
	}
	candidate := config.Clone(cfg)
	found := false
	for i, p := range candidate.Projects {
		if p.Name == project.Name {
			candidate.Projects[i] = project
			found = true
		}
	}
	if !found {
		candidate.Projects = append(candidate.Projects, project)
	}
	if _, err := config.Validate(candidate); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	writer, ok := h.manager.(projectWriter)
	if !ok {
		writeError(w, 503, "project editing unavailable")
		return
	}
	// Repository discovery is network I/O: do not commit a change after the
	// publishing session expired or was revoked during that lookup.
	h.requireTool("publishing", func(w http.ResponseWriter, r *http.Request) {
		if !h.recordMutationIntent(w, r, "project.trust", project.Name) {
			return
		}
		if err := writer.PutProject(project, req.Expected, false); err != nil {
			writeError(w, 409, "project could not be saved; refresh and review again")
			return
		}
		h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: project.Name, Code: "project.trust_saved", ConfigSHA256: h.manager.ConfigDigest()})
		writeJSON(w, 200, map[string]any{"name": project.Name, "workflow_ref": project.Deployers[0].GitHub.WorkflowRef, "deploy_host_configured": cfg.Pages != nil && cfg.Pages.Deploy != nil})
	})(w, r)
}
func (h *Handler) removeProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expected string `json:"expected"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Expected == "" {
		writeError(w, 400, "reviewed configuration digest required")
		return
	}
	writer, ok := h.manager.(projectWriter)
	if !ok {
		writeError(w, 503, "project editing unavailable")
		return
	}
	name := r.PathValue("name")
	if !h.recordMutationIntent(w, r, "project.disconnect", name) {
		return
	}
	if err := writer.PutProject(config.Project{Name: name}, req.Expected, true); err != nil {
		writeError(w, 409, "configuration changed; refresh and review again")
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: name, Code: "project.disconnected", ConfigSHA256: h.manager.ConfigDigest()})
	writeJSON(w, 200, map[string]bool{"deploy_trust_removed": true, "content_retained": true})
}

func (h *Handler) configureGitHub(w http.ResponseWriter, r *http.Request) {
	var req struct {
		App      *config.GitHubApp `json:"app"`
		Expected string            `json:"expected"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid GitHub configuration")
		return
	}
	if req.Expected == "" || req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; refresh before connecting GitHub")
		return
	}
	if err := req.App.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writer, ok := h.manager.(interface {
		ConfigureGitHub(*config.GitHubApp, string) error
	})
	if !ok {
		writeError(w, 503, "GitHub configuration unavailable")
		return
	}
	if !h.recordMutationIntent(w, r, "github.configure", "") {
		return
	}
	if err := writer.ConfigureGitHub(req.App, req.Expected); err != nil {
		writeError(w, 422, "GitHub configuration could not be saved")
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Code: "github.configured", ConfigSHA256: h.manager.ConfigDigest()})
	writeJSON(w, 200, map[string]bool{"configured": req.App != nil, "existing_deploy_trust_unchanged": true})
}
