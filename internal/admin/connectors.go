package admin

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net/http"
	"time"

	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/tunnel"
)

// connectors is the tunnel connector registry (tunnel.Registry implements
// it): the operations of `ozal connector` (CLI-001).
type connectors interface {
	List(ctx context.Context) ([]tunnel.Record, error)
	Current(ctx context.Context, name string) (tunnel.Fingerprint, error)
	Enroll(ctx context.Context, name string, fp tunnel.Fingerprint) error
	Rotate(ctx context.Context, name string, expected, replacement tunnel.Fingerprint) error
	Revoke(ctx context.Context, name string) error
}

// WithConnectors enables the connector endpoints.
func WithConnectors(registry connectors) Option {
	return func(h *Handler) { h.connectors = registry }
}

// registerConnectors: reads take the admin token, changes run on the host
// only. Whoever enrols a certificate decides which machine may serve a
// protected host, so no bearer that travels to other machines may do it.
func (h *Handler) registerConnectors() {
	h.mux.HandleFunc("GET /_/api/connectors", h.requireTool("connectors", h.listConnectors))
	h.mux.HandleFunc("GET /_/api/connectors/{name}", h.requireTool("connectors", h.currentConnector))
	h.mux.HandleFunc("POST /_/api/connectors", h.requireLocal(h.enrollConnector))
	h.mux.HandleFunc("POST /_/api/connectors/{name}/rotate", h.requireLocal(h.rotateConnector))
	h.mux.HandleFunc("POST /_/api/connectors/{name}/revoke", h.requireLocal(h.revokeConnector))
}

type connectorView struct {
	Name      string    `json:"name"`
	SHA256    string    `json:"sha256"`
	NotAfter  time.Time `json:"not_after,omitzero"`
	Hosts     []string  `json:"hosts"`
	Connected bool      `json:"connected"`
	Created   time.Time `json:"created"`
	Revoked   time.Time `json:"revoked,omitzero"`
}

func (h *Handler) listConnectors(w http.ResponseWriter, r *http.Request) {
	records, err := h.connectors.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connectors could not be listed")
		return
	}
	out := make([]connectorView, 0, len(records))
	for _, rec := range records {
		view := connectorView{Name: rec.Name, SHA256: hex.EncodeToString(rec.Fingerprint[:]), Created: rec.CreatedAt.UTC(), Revoked: rec.RevokedAt.UTC(), NotAfter: rec.NotAfter, Hosts: []string{}}
		for _, status := range h.manager.RouteStatuses() {
			if status.Route.Connector == rec.Name {
				view.Hosts = append(view.Hosts, status.Route.Host)
				view.Connected = view.Connected || status.ConnectorConnected
			}
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": out})
}

func (h *Handler) currentConnector(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	fp, err := h.connectors.Current(r.Context(), name)
	if err != nil {
		h.connectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "sha256": hex.EncodeToString(fp[:])})
}

// The certificate travels as PEM so the edge, not the client, decides what
// it fingerprints: a private key in the body is refused like in a file.
type enrollRequest struct {
	Name           string `json:"name"`
	CertificatePEM string `json:"certificate_pem"`
}

func (h *Handler) enrollConnector(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fp, err := tunnel.FingerprintFromPEM([]byte(req.CertificatePEM))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.recordMutationIntent(w, r, "connector.enroll", req.Name) {
		return
	}
	var enrollErr error
	if detailed, ok := h.connectors.(interface {
		EnrollWithExpiry(context.Context, string, tunnel.Fingerprint, time.Time) error
	}); ok {
		enrollErr = detailed.EnrollWithExpiry(r.Context(), req.Name, fp, certificateExpiry(req.CertificatePEM))
	} else {
		enrollErr = h.connectors.Enroll(r.Context(), req.Name, fp)
	}
	if err := enrollErr; err != nil {
		h.connectorError(w, err)
		return
	}
	h.emit(events.Event{Type: events.ConnectorEnrolled, Actor: actor(r), Host: req.Name, Reason: shortFingerprint(fp)})
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "sha256": hex.EncodeToString(fp[:])})
}

type rotateRequest struct {
	Expected       string `json:"expected_sha256"`
	CertificatePEM string `json:"certificate_pem"`
}

// rotateConnector is a compare-and-swap: it fails unless the active
// certificate is still the one the caller saw.
func (h *Handler) rotateConnector(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req rotateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, err := hex.DecodeString(req.Expected)
	if err != nil || len(raw) != len(tunnel.Fingerprint{}) {
		writeError(w, http.StatusBadRequest, "expected_sha256 must be 64 hex characters")
		return
	}
	var expected tunnel.Fingerprint
	copy(expected[:], raw)
	next, err := tunnel.FingerprintFromPEM([]byte(req.CertificatePEM))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.recordMutationIntent(w, r, "connector.rotate", name) {
		return
	}
	var rotateErr error
	if detailed, ok := h.connectors.(interface {
		RotateWithExpiry(context.Context, string, tunnel.Fingerprint, tunnel.Fingerprint, time.Time) error
	}); ok {
		rotateErr = detailed.RotateWithExpiry(r.Context(), name, expected, next, certificateExpiry(req.CertificatePEM))
	} else {
		rotateErr = h.connectors.Rotate(r.Context(), name, expected, next)
	}
	if err := rotateErr; err != nil {
		h.connectorError(w, err)
		return
	}
	h.emit(events.Event{Type: events.ConnectorRotated, Actor: actor(r), Host: name, Reason: shortFingerprint(next)})
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "sha256": hex.EncodeToString(next[:])})
}

func (h *Handler) revokeConnector(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !h.recordMutationIntent(w, r, "connector.revoke", name) {
		return
	}
	if err := h.connectors.Revoke(r.Context(), name); err != nil {
		h.connectorError(w, err)
		return
	}
	h.emit(events.Event{Type: events.ConnectorRevoked, Actor: actor(r), Host: name})
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "revoked": "true"})
}

func (h *Handler) connectorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tunnel.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, tunnel.ErrAlreadyActive), errors.Is(err, tunnel.ErrEnrolmentChanged):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, tunnel.ErrInvalidName), errors.Is(err, tunnel.ErrSameCertificate):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "connector registry error")
	}
}

func shortFingerprint(fp tunnel.Fingerprint) string {
	return "sha256:" + hex.EncodeToString(fp[:8])
}

// The public PEM has already passed FingerprintFromPEM validation.
func certificateExpiry(value string) time.Time {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return time.Time{}
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return certificate.NotAfter
}
