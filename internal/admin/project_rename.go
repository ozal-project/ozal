package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
	"github.com/ozal-project/ozal/internal/routemanager"
)

// siteCopier copies a site's live deployment to a new site name.
type siteCopier interface {
	CopyLive(ctx context.Context, from, to string) (pages.Deployment, error)
}

// projectRenamer renames a project and its site under a reviewed digest.
type projectRenamer interface {
	RenameProject(from, to, expected string) error
}

// renameProject is host-only, like publisher trust: renaming changes which
// name CI publishes under. The plan names what changes; applying first copies
// the live build to the new site name, so the moved routes never serve an
// empty site, then renames project, site and routes in one configuration
// change. The old site's builds stay retained: renaming back undoes it.
func (h *Handler) renameProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To       string `json:"to"`
		Expected string `json:"expected"`
		Apply    bool   `json:"apply"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Expected == "" {
		writeError(w, http.StatusBadRequest, "new name and reviewed configuration digest required")
		return
	}
	if req.Expected != h.manager.ConfigDigest() {
		writeError(w, http.StatusConflict, "configuration changed; refresh and review the rename")
		return
	}
	from := r.PathValue("name")
	cfg := h.manager.Config()
	_, project, hosts, err := config.PlanProjectRename(cfg, from, req.To)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	oldSite := ""
	for _, p := range cfg.Projects {
		if p.Name == from {
			oldSite = p.Site
		}
	}
	plan := map[string]any{"name": from, "to": project.Name, "site_from": oldSite, "site_to": project.Site,
		"hosts": hosts, "applied": false, "config_sha256": h.manager.ConfigDigest()}
	if !req.Apply {
		writeJSON(w, http.StatusOK, plan)
		return
	}
	copier, okCopy := h.pages.(siteCopier)
	renamer, okRename := h.manager.(projectRenamer)
	if !okCopy || !okRename {
		writeError(w, http.StatusServiceUnavailable, "project rename is unavailable on this edge")
		return
	}
	if !h.recordMutationIntent(w, r, "project.rename", from) {
		return
	}
	copied, err := copier.CopyLive(r.Context(), oldSite, project.Site)
	if err != nil {
		if errors.Is(err, pages.ErrNothingLive) {
			writeError(w, http.StatusUnprocessableEntity, "site "+oldSite+" has nothing live to carry over; publish it first")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "the live build could not be copied; nothing was renamed")
		return
	}
	if err := renamer.RenameProject(from, req.To, req.Expected); err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, routemanager.ErrConfigChanged) {
			status = http.StatusConflict
		}
		writeError(w, status, "the build was copied to "+project.Site+" but the rename was not saved; review and retry")
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: project.Name,
		Code: "project.renamed", Reason: from + " -> " + project.Name, ConfigSHA256: h.manager.ConfigDigest()})
	plan["applied"], plan["deployment"], plan["config_sha256"] = true, copied.ID, h.manager.ConfigDigest()
	writeJSON(w, http.StatusOK, plan)
}
