package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/octaviosantacruz/switchboard/internal/certs"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/domaincheck"
	"github.com/octaviosantacruz/switchboard/internal/domaincheck/probe"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

// domainService connects a configured hostname to this edge (UI-006): it
// reports DNS, certificate and reachability, and creates the one missing
// record when a DNS provider is configured.
type domainService interface {
	Check(ctx context.Context, cfg config.Config, host string) probe.Report
	CreateRecord(ctx context.Context, cfg config.Config, host string) (domaincheck.Record, error)
}

// WithDomains enables the connection checklist for applications.
func WithDomains(service domainService) Option {
	return func(h *Handler) { h.domains = service }
}

// configuredHost returns the route host for a path value, or false when no
// route serves it: the edge only ever probes or creates records for
// hostnames an operator has configured.
func configuredHost(cfg config.Config, raw string) (string, bool) {
	host, err := config.NormalizeHost(raw)
	if err != nil {
		return "", false
	}
	for _, route := range cfg.Routes {
		if route.Host == host {
			return host, true
		}
	}
	return "", false
}

func (h *Handler) checkApplication(w http.ResponseWriter, r *http.Request) {
	if h.domains == nil {
		writeError(w, http.StatusServiceUnavailable, "connection checks are unavailable")
		return
	}
	cfg := h.manager.Config()
	host, ok := configuredHost(cfg, r.PathValue("host"))
	if !ok {
		writeError(w, http.StatusNotFound, "no application uses this hostname")
		return
	}
	writeJSON(w, http.StatusOK, h.domains.Check(r.Context(), cfg, host))
}

func (h *Handler) createApplicationRecord(w http.ResponseWriter, r *http.Request) {
	if h.domains == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS records cannot be created here")
		return
	}
	cfg := h.manager.Config()
	host, ok := configuredHost(cfg, r.PathValue("host"))
	if !ok {
		writeError(w, http.StatusNotFound, "no application uses this hostname")
		return
	}
	if cfg.DNS == nil || cfg.DNS.Cloudflare == nil {
		writeError(w, http.StatusConflict, probe.ErrNoProvider.Error())
		return
	}
	if !h.recordMutationIntent(w, r, "dns.create", safeRouteEventHost(host)) {
		return
	}
	record, err := h.domains.CreateRecord(r.Context(), cfg, host)
	switch {
	case errors.Is(err, certs.ErrRecordExists):
		writeError(w, http.StatusConflict, "this hostname already has a DNS record; it was left unchanged. Change it at your DNS provider if it should point here.")
		return
	case errors.Is(err, certs.ErrAmbiguous):
		writeError(w, http.StatusBadGateway, "the provider did not confirm the record; check the connection before retrying, it may exist already")
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "the DNS record could not be created: "+err.Error())
		return
	}
	h.emit(events.Event{Type: events.DNSRecordCreated, Actor: actor(r), Host: safeRouteEventHost(host), Code: record.Type})
	writeJSON(w, http.StatusCreated, record)
}
