package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/pages"
)

// deployments is the deployment store as the admin API needs it
// (pages.Store implements it).
type deployments interface {
	Deploy(ctx context.Context, site string, archive io.Reader) (pages.Deployment, error)
	Rollback(ctx context.Context, site, id string) (pages.Deployment, error)
	Sites(ctx context.Context) ([]pages.Site, error)
}

// WithPages enables the site deployment endpoints (PAGES-002).
func WithPages(store deployments) Option {
	return func(h *Handler) { h.pages = store }
}

func (h *Handler) registerSites() {
	h.mux.HandleFunc("GET /_/api/sites", h.requireSitesBearer(h.listSites))
	h.mux.HandleFunc("POST /_/api/sites/{site}/deployments", h.requireSitesBearer(h.deploySite))
	h.mux.HandleFunc("POST /_/api/sites/{site}/rollback", h.requireSitesBearer(h.rollbackSite))
}

func (h *Handler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.pages.Sites(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "deployments could not be listed")
		return
	}
	if sites == nil {
		sites = []pages.Site{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

// siteUploadTimeout bounds one site upload, body and extraction included.
// The admin server gives every other request 10 s; an archive over a slow
// link needs more (2026-10-21: 500 KB took 5–8 s through the homelab tunnel,
// and two deploys were cut). Only a caller that passed the bearer check gets
// here, so the longer deadline is not open to a slow anonymous client.
const siteUploadTimeout = 10 * time.Minute

// deploySite takes the tar.gz as the raw request body. The cap is applied
// before a single byte is decompressed; the per-file and total limits are
// applied by the extractor on the real bytes.
func (h *Handler) deploySite(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	// A writer that cannot move its deadlines (a test recorder) keeps the
	// server's; the upload still works, only with the shorter limit.
	deadline := time.Now().Add(siteUploadTimeout)
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	target := site
	if !pages.ValidSiteName(site) {
		target = ""
	}
	if !h.recordMutationIntent(w, r, "site.deploy", target) {
		return
	}
	body := http.MaxBytesReader(w, r.Body, pages.MaxUploadBytes)
	deployment, err := h.pages.Deploy(r.Context(), site, body)
	if err != nil {
		h.siteError(w, r, site, err)
		return
	}
	h.emit(events.Event{Type: events.PagesDeployed, Actor: actor(r), Host: site, Reason: deployment.ID})
	writeJSON(w, http.StatusCreated, deployment)
}

func (h *Handler) rollbackSite(w http.ResponseWriter, r *http.Request) {
	site := r.PathValue("site")
	var request struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target := site
	if !pages.ValidSiteName(site) {
		target = ""
	}
	if !h.recordMutationIntent(w, r, "site.rollback", target) {
		return
	}
	deployment, err := h.pages.Rollback(r.Context(), site, request.ID)
	if err != nil {
		h.siteError(w, r, site, err)
		return
	}
	h.emit(events.Event{Type: events.PagesRolledBack, Actor: actor(r), Host: site, Reason: deployment.ID})
	writeJSON(w, http.StatusOK, deployment)
}

// siteError maps store errors to statuses. Refusals carry their stable code
// so a client can tell a hostile archive from a server fault.
func (h *Handler) siteError(w http.ResponseWriter, r *http.Request, site string, err error) {
	var refusal *pages.RefusedError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &refusal):
		h.emit(events.Event{Type: events.PagesRefused, Actor: actor(r), Host: site, Code: string(refusal.Code), Reason: refusal.Reason})
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": refusal.Reason, "code": string(refusal.Code)})
	case errors.As(err, &tooBig):
		h.emit(events.Event{Type: events.PagesRefused, Actor: actor(r), Host: site, Code: "archive.upload_too_large", Reason: "upload exceeds the size cap"})
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds the size cap")
	case errors.Is(err, pages.ErrUploadInterrupted):
		// The archive did not finish arriving; nothing about it was judged.
		code, status := "upload.interrupted", http.StatusBadRequest
		if errors.Is(err, os.ErrDeadlineExceeded) {
			code, status = "upload.timeout", http.StatusRequestTimeout
		}
		h.emit(events.Event{Type: events.PagesRefused, Actor: actor(r), Host: site, Code: code, Reason: "the upload stopped before the archive was complete"})
		writeJSON(w, status, map[string]string{"error": "the upload stopped before the archive was complete; send it again", "code": code})
	case errors.Is(err, pages.ErrBadSiteName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pages.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		h.emit(events.Event{
			Type: events.PagesFailed, Actor: actor(r), Host: site,
			Code: "deployment.failed", Reason: "deployment could not be completed",
		})
		writeError(w, http.StatusInternalServerError, "deployment failed")
	}
}
