package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/ozal-project/ozal/internal/cftunnel"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

// ingressService reads and applies the Cloudflare Tunnel this edge is
// published through (ingress.mode "cloudflare_tunnel").
type ingressService interface {
	Inspect(ctx context.Context, cfg config.Config) cftunnel.Report
	Apply(ctx context.Context, cfg config.Config) (cftunnel.Report, error)
}

// WithIngress shows how visitors reach this edge and lets an operator create
// the Cloudflare Tunnel through the API.
func WithIngress(service ingressService) Option {
	return func(h *Handler) { h.ingress = service }
}

// ingressStatus answers in every mode: the console uses the mode to choose
// which Domains steps to show.
func (h *Handler) ingressStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.manager.Config()
	if h.ingress == nil || cfg.IngressMode() != config.IngressCloudflareTunnel {
		writeJSON(w, http.StatusOK, cftunnel.Report{Mode: cfg.IngressMode()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, h.ingress.Inspect(ctx, cfg))
}

// applyIngress creates or recovers the tunnel and points it at this edge.
// It never shows the connector token and never starts cloudflared.
func (h *Handler) applyIngress(w http.ResponseWriter, r *http.Request) {
	cfg := h.manager.Config()
	if h.ingress == nil || cfg.IngressMode() != config.IngressCloudflareTunnel {
		writeError(w, http.StatusConflict, "this edge is not configured for a Cloudflare Tunnel (ingress.mode)")
		return
	}
	if !h.recordMutationIntent(w, r, "ingress.cloudflare_tunnel", cfg.Ingress.CloudflareTunnel.Name) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	report, err := h.ingress.Apply(ctx, cfg)
	for _, action := range report.Applied {
		h.emit(events.Event{Type: events.Type("ingress.tunnel_applied"), Actor: actor(r), Host: cfg.Ingress.CloudflareTunnel.Name, Code: action})
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "report": report})
		return
	}
	writeJSON(w, http.StatusOK, report)
}
