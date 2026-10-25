package deployapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/pages"
	"github.com/octaviosantacruz/switchboard/internal/pages/deployauth"
)

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	project, id, ok := h.admit(w, r)
	if !ok {
		return
	}
	if r.ContentLength > pages.MaxUploadBytes {
		h.reject(w, r, 413, "deployment.upload_too_large", project, id)
		return
	}
	h.mu.Lock()
	busy := h.busy[project.Name]
	if !busy {
		h.busy[project.Name] = true
	}
	h.mu.Unlock()
	if busy {
		h.reject(w, r, 409, "deployment.busy", project, id)
		return
	}
	defer func() { h.mu.Lock(); delete(h.busy, project.Name); h.mu.Unlock() }()
	source := id.source
	intent := events.Event{Time: h.options.Now().UTC(), Type: events.AdminMutationRequested, Actor: id.actor, Host: project.Site, Code: "project.deploy", Reason: "authenticated publisher requested deployment", Deployment: &source}
	if h.options.AuditEnabled {
		var err error
		if h.options.Audit == nil {
			err = errors.New("audit unavailable")
		} else {
			err = h.options.Audit.Append(r.Context(), intent)
		}
		if err != nil && h.options.AuditRequired {
			h.reject(w, r, 503, "deployment.audit_unavailable", project, id)
			return
		}
		h.options.Sink.Emit(intent)
	}
	deadline := time.Now().Add(10 * time.Minute)
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	metadata := pages.SourceMetadata{Project: project.Name}
	if id.claims != nil {
		metadata.CommitSHA = id.claims.CommitSHA
		metadata.RunURL = id.claims.WorkflowRunURL()
	}
	deployment, err := h.options.Store.DeployProject(ctx, project.Site, metadata, http.MaxBytesReader(w, r.Body, pages.MaxUploadBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		var refusal *pages.RefusedError
		switch {
		case errors.Is(err, pages.ErrProjectConflict):
			h.reject(w, r, 409, "deployment.project_conflict", project, id)
		case errors.As(err, &tooLarge):
			h.reject(w, r, 413, "deployment.upload_too_large", project, id)
		case errors.As(err, &refusal):
			h.reject(w, r, 422, string(refusal.Code), project, id)
		default:
			h.reject(w, r, 503, "deployment.failed", project, id)
		}
		return
	}
	h.options.Sink.Emit(events.Event{Time: h.options.Now().UTC(), Type: events.PagesDeployed, Actor: id.actor, Host: project.Site, Code: "deployment.created", Reason: "project deployment committed", Deployment: &source})
	writeJSON(w, 201, deploymentView(deployment))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	project, id, ok := h.admit(w, r)
	if !ok {
		return
	}
	if deploymentID := r.PathValue("id"); len(deploymentID) != 64 || !deployauth.ValidCommit(deploymentID) {
		h.reject(w, r, 404, "deployment.not_found", project, id)
		return
	}
	deployment, err := h.options.Store.Deployment(r.Context(), project.Site, r.PathValue("id"))
	if errors.Is(err, pages.ErrNotFound) || err == nil && deployment.Project != project.Name {
		h.reject(w, r, 404, "deployment.not_found", project, id)
		return
	}
	if err != nil {
		h.reject(w, r, 503, "deployment.failed", project, id)
		return
	}
	writeJSON(w, 200, deploymentView(deployment))
}

type deploymentResponse struct {
	Project   string    `json:"project"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Live      bool      `json:"live"`
	CommitSHA string    `json:"commit_sha,omitempty"`
	RunURL    string    `json:"run_url,omitempty"`
}

func deploymentView(d pages.Deployment) deploymentResponse {
	return deploymentResponse{Project: d.Project, ID: d.ID, CreatedAt: d.CreatedAt, Files: d.Files, Bytes: d.Bytes, Live: d.Live, CommitSHA: d.CommitSHA, RunURL: d.RunURL}
}
