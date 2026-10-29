package admin

import (
	"net/http"
	"slices"
)

type projectView struct {
	Name      string                `json:"name"`
	Site      string                `json:"site"`
	Hostnames []string              `json:"hostnames"`
	Deployers []projectDeployerView `json:"deployers"`
}

type projectDeployerView struct {
	Type            string             `json:"type"`
	GitHub          *projectGitHubView `json:"github,omitempty"`
	TokenConfigured bool               `json:"token_configured,omitempty"`
}

type projectGitHubView struct {
	RepositoryID      int64  `json:"repository_id"`
	RepositoryOwnerID int64  `json:"repository_owner_id"`
	Ref               string `json:"ref"`
	WorkflowRef       string `json:"workflow_ref,omitempty"`
}

func (h *Handler) listProjects(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Config()
	projects := cfg.Projects
	out := make([]projectView, 0, len(projects))
	for _, project := range projects {
		view := projectView{
			Name:      project.Name,
			Site:      project.Site,
			Hostnames: append([]string{}, project.Hostnames...),
			Deployers: make([]projectDeployerView, 0, len(project.Deployers)),
		}
		for _, deployer := range project.Deployers {
			switch {
			case deployer.GitHub != nil:
				github := deployer.GitHub
				view.Deployers = append(view.Deployers, projectDeployerView{
					Type: "github",
					GitHub: &projectGitHubView{
						RepositoryID: github.RepositoryID, RepositoryOwnerID: github.RepositoryOwnerID,
						Ref: github.Ref, WorkflowRef: github.WorkflowRef,
					},
				})
			case deployer.Token != "":
				// A token reference reveals the secret's source and filesystem
				// location; report only that this authentication method exists.
				view.Deployers = append(view.Deployers, projectDeployerView{Type: "token", TokenConfigured: true})
			}
		}
		out = append(out, view)
	}
	// Publishing operators need site names and a revision for binding trust,
	// not the Applications API's upstreams, access policies or secret sources.
	sites := []string{}
	for _, route := range cfg.Routes {
		if route.Site != "" && !slices.Contains(sites, route.Site) {
			sites = append(sites, route.Site)
		}
	}
	slices.Sort(sites)
	writeJSON(w, http.StatusOK, map[string]any{"projects": out, "sites": sites, "config_sha256": h.manager.ConfigDigest()})
}
