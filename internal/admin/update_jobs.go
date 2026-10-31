package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/updateexec"
	"github.com/ozal-project/ozal/internal/updatejob"
)

// updateExecutor is the fixed local capability behind the console. It is
// optional: without it the console can still show the last reported status.
type updateExecutor interface {
	Plan(ctx context.Context) (updatejob.Plan, error)
	Start(ctx context.Context, planID string) (updatejob.Job, error)
	Job(ctx context.Context, id string) (updatejob.Job, error)
	Latest(ctx context.Context) (updatejob.Job, error)
}

// WithUpdateExecutor enables the reviewed check/install operations through a
// local executor. The host enables installation by configuring the socket;
// the console cannot choose a path, command or unit.
func WithUpdateExecutor(executor updateExecutor) Option {
	return func(h *Handler) { h.updatesExecutor = executor }
}

func (h *Handler) updatePlan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.updatesExecutor == nil {
		writeError(w, http.StatusServiceUnavailable, "the update executor is not configured on this host")
		return
	}
	plan, err := h.updatesExecutor.Plan(r.Context())
	if err != nil {
		writeUpdateExecutorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h *Handler) startUpdateJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.updatesExecutor == nil {
		writeError(w, http.StatusServiceUnavailable, "the update executor is not configured on this host")
		return
	}
	grant, ok := r.Context().Value(operatorKey{}).(operatorGrant)
	if !ok {
		writeErrorCode(w, http.StatusForbidden, "sign in with an administrator passkey to install an update", "operator_required")
		return
	}
	if !h.operators.passkeyFresh(grant.ID, passkeyStepUpMaxAge) {
		writeErrorCode(w, http.StatusForbidden, "confirm this installation with your passkey", "passkey_required")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2048))
	if err != nil || len(raw) > 1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "the request is too large")
		return
	}
	var req struct {
		PlanID string `json:"plan_id"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || !updatejob.ValidID(req.PlanID) {
		writeError(w, http.StatusBadRequest, "send the reviewed plan id")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "send exactly one JSON document")
		return
	}
	if !h.recordMutationIntent(w, r, "update.install", req.PlanID) {
		return
	}
	job, err := h.updatesExecutor.Start(r.Context(), req.PlanID)
	if err != nil {
		writeUpdateExecutorError(w, err)
		return
	}
	h.emit(events.Event{Type: events.Type("update.started"), Actor: h.actorOf(r), Host: job.To, Code: job.ID, Reason: "reviewed plan " + req.PlanID})
	writeJSON(w, http.StatusAccepted, publicUpdateJob(job))
}

func (h *Handler) latestUpdateJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.updatesExecutor == nil {
		writeError(w, http.StatusServiceUnavailable, "the update executor is not configured on this host")
		return
	}
	job, err := h.updatesExecutor.Latest(r.Context())
	if errors.Is(err, updateexec.ErrNoJob) {
		writeError(w, http.StatusNotFound, "no update job has run")
		return
	}
	if err != nil {
		writeUpdateExecutorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, publicUpdateJob(job))
}

func (h *Handler) updateJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.updatesExecutor == nil {
		writeError(w, http.StatusServiceUnavailable, "the update executor is not configured on this host")
		return
	}
	id := r.PathValue("id")
	if !updatejob.ValidID(id) {
		writeError(w, http.StatusNotFound, "no such update job")
		return
	}
	job, err := h.updatesExecutor.Job(r.Context(), id)
	if err != nil {
		writeUpdateExecutorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, publicUpdateJob(job))
}

// publicUpdateJob removes host detail. The executor's reason may name a
// config path, a unit or a process error; the console gets a stable sentence.
func publicUpdateJob(job updatejob.Job) updatejob.Job {
	switch job.Result {
	case updatejob.ResultUpdated:
		job.Reason = "the update completed"
	case updatejob.ResultRolledBack:
		job.Reason = "the previous version was restored"
	case updatejob.ResultFailed:
		job.Reason = "the update failed"
	case updatejob.ResultInterventionRequired:
		job.Reason = "the update needs operator intervention"
	default:
		job.Reason = ""
	}
	return job
}

// writeUpdateExecutorError keeps transport details on the host. Only an
// explicit, bounded executor refusal is shown to the operator.
func writeUpdateExecutorError(w http.ResponseWriter, err error) {
	var failure *updateexec.Error
	if errors.As(err, &failure) {
		switch failure.Status {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict:
			writeError(w, failure.Status, failure.Message)
			return
		}
	}
	writeError(w, http.StatusServiceUnavailable, "the local update executor is unavailable or refused the request")
}
