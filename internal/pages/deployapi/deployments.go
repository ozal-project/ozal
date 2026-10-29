package deployapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
	"github.com/ozal-project/ozal/internal/pages/deployauth"
)

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	project, id, ok := h.admit(w, r, true)
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
		status, code := 503, "deployment.failed"
		var tooLarge *http.MaxBytesError
		var refusal *pages.RefusedError
		switch {
		case errors.Is(err, pages.ErrProjectConflict):
			status, code = 409, "deployment.project_conflict"
		case errors.As(err, &tooLarge):
			status, code = 413, "deployment.upload_too_large"
		case errors.As(err, &refusal):
			status, code = 422, string(refusal.Code)
		}
		h.auditOutcome(r, events.Event{Time: h.options.Now().UTC(), Type: events.PagesDeployRefused, Actor: id.actor, Host: project.Site, Code: code, Reason: "deployment request refused", Deployment: &source})
		h.reject(w, r, status, code, project, id)
		return
	}
	outcome := events.Event{Time: h.options.Now().UTC(), Type: events.PagesDeployed, Actor: id.actor, Host: project.Site, Code: "deployment.created", Reason: "project deployment committed", Deployment: &source}
	h.auditOutcome(r, outcome)
	h.options.Sink.Emit(outcome)
	writeJSON(w, 201, deploymentView(deployment))
}

// auditOutcome closes the durable intent with what actually happened. It runs
// after the deployment committed or failed, so a failed append cannot undo it:
// the intent already made the attempt durable, the audit adapter counts and
// logs the failure, and the response still tells the publisher the truth. The
// append outlives a disconnected client so the journal is not left open.
func (h *Handler) auditOutcome(r *http.Request, event events.Event) {
	if !h.options.AuditEnabled || h.options.Audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	_ = h.options.Audit.Append(ctx, event)
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	project, id, ok := h.admit(w, r, false)
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
