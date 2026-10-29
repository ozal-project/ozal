package admin

import (
	"net/http"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

// migrateProjectTrust is host-only: possession of a web administrator or
// publishing token never authorizes binding arbitrary repository IDs.
func (h *Handler) migrateProjectTrust(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Trust    config.ProjectGitHubTrust `json:"trust"`
		Expected string                    `json:"expected"`
		Apply    bool                      `json:"apply"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid publisher trust request")
		return
	}
	if req.Expected == "" || req.Expected != h.manager.ConfigDigest() {
		writeError(w, 409, "configuration changed; refresh and review publisher trust")
		return
	}
	project, before, err := config.PlanProjectTrust(h.manager.Config(), r.PathValue("name"), req.Trust)
	if err != nil {
		writeError(w, 422, "publisher trust is invalid or the project has no single GitHub publisher")
		return
	}
	changed := before != req.Trust
	if req.Apply && changed {
		writer, ok := h.manager.(projectWriter)
		if !ok {
			writeError(w, 503, "project editing unavailable")
			return
		}
		if !h.recordMutationIntent(w, r, "project.trust", project.Name) {
			return
		}
		if err := writer.PutProject(project, req.Expected, false); err != nil {
			writeError(w, 409, "project could not be saved; refresh and review publisher trust")
			return
		}
		h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: project.Name,
			Code: "project.trust_migrated", ConfigSHA256: h.manager.ConfigDigest()})
	}
	writeJSON(w, 200, map[string]any{"name": project.Name, "site": project.Site, "before": before,
		"after": req.Trust, "changed": changed, "applied": req.Apply, "config_sha256": h.manager.ConfigDigest()})
}
