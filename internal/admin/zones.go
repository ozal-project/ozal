package admin

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/domaincheck"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/routemanager"
)

// zoneCertificates is what the zones page reads from the per-zone
// certificate set (EDGE-008).
type zoneCertificates interface {
	Status() []certs.ZoneStatus
	Renew(zone string) error
}

// WithZoneResolver replaces the system resolver the zone check reads the
// challenge CNAME with; tests use it so they never touch public DNS.
func WithZoneResolver(lookupCNAME func(ctx context.Context, host string) (string, error)) Option {
	return func(h *Handler) { h.zoneLookup = lookupCNAME }
}

// zoneWriter changes the runtime zones under a reviewed digest.
type zoneWriter interface {
	AddZone(name, expected string) error
	RemoveZone(name, expected string) error
}

// WithZones shows each zone's certificate state. Without it (autocert or
// no TLS) zones are listed from the configuration alone.
func WithZones(certificates zoneCertificates) Option {
	return func(h *Handler) { h.zoneCertificates = certificates }
}

type zoneChallengeView struct {
	// Name is the record the operator creates: a CNAME to Target when
	// Delegated, otherwise the TXT the provider writes itself.
	Name      string `json:"name"`
	Target    string `json:"target"`
	Delegated bool   `json:"delegated"`
}

type zoneView struct {
	Name        string            `json:"name"`
	Source      string            `json:"source"`
	State       string            `json:"state"`
	Names       []string          `json:"names"`
	NotAfter    string            `json:"not_after,omitempty"`
	RenewsAt    string            `json:"renews_at,omitempty"`
	LastAttempt string            `json:"last_attempt,omitempty"`
	NextAttempt string            `json:"next_attempt,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	Challenge   zoneChallengeView `json:"challenge"`
	Hosts       []string          `json:"hosts"`
}

func (h *Handler) listZones(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Config()
	states := map[string]certs.ZoneStatus{}
	if h.zoneCertificates != nil {
		for _, status := range h.zoneCertificates.Status() {
			states[status.Zone] = status
		}
	}
	zones := []zoneView{}
	for _, zone := range config.ServedZones(cfg) {
		challenge := config.ChallengeFor(cfg.TLS, zone.Name)
		view := zoneView{
			Name: zone.Name, Source: zone.Source, State: string(certs.ZonePending),
			Names:     certs.ZoneNames(zone.Name),
			Challenge: zoneChallengeView{Name: challenge.Name, Target: challenge.Target, Delegated: challenge.Delegated},
			Hosts:     config.ZoneHosts(cfg, zone.Name),
		}
		if view.Hosts == nil {
			view.Hosts = []string{}
		}
		if status, ok := states[zone.Name]; ok {
			view.State = string(status.State)
			view.LastError = status.LastError
			view.LastAttempt, view.NextAttempt = zoneTime(status.LastAttempt), zoneTime(status.NextAttempt)
			if status.Valid {
				view.NotAfter, view.RenewsAt = zoneTime(status.NotAfter), zoneTime(status.RenewAt)
			}
		}
		zones = append(zones, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config_sha256": h.manager.ConfigDigest(),
		// Zones need the edge to issue its own certificates.
		"available": cfg.TLS.Enabled && cfg.TLS.DNS01 != nil,
		"zones":     zones,
	})
}

func zoneTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

type zoneRequest struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
}

func (h *Handler) addZone(w http.ResponseWriter, r *http.Request) {
	var req zoneRequest
	if err := decodeJSON(w, r, &req); err != nil || req.Expected == "" {
		writeError(w, http.StatusBadRequest, "zone name and reviewed configuration digest required")
		return
	}
	name, err := config.NormalizeZoneName(req.Name)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.changeZone(w, r, "zone.add", "zone.added", name, func(writer zoneWriter) error { return writer.AddZone(name, req.Expected) })
}

func (h *Handler) removeZone(w http.ResponseWriter, r *http.Request) {
	var req zoneRequest
	if err := decodeJSON(w, r, &req); err != nil || req.Expected == "" {
		writeError(w, http.StatusBadRequest, "reviewed configuration digest required")
		return
	}
	name, err := config.NormalizeZoneName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.changeZone(w, r, "zone.remove", "zone.removed", name, func(writer zoneWriter) error { return writer.RemoveZone(name, req.Expected) })
}

func (h *Handler) changeZone(w http.ResponseWriter, r *http.Request, intent, code, name string, change func(zoneWriter) error) {
	writer, ok := h.manager.(zoneWriter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "zone editing is unavailable")
		return
	}
	if !h.recordMutationIntent(w, r, intent, name) {
		return
	}
	if err := change(writer); err != nil {
		if errors.Is(err, routemanager.ErrConfigChanged) {
			writeError(w, http.StatusConflict, "configuration changed; refresh and review again")
			return
		}
		if errors.Is(err, routemanager.ErrPersistence) {
			writeError(w, http.StatusServiceUnavailable, "the zone could not be saved; nothing changed")
			return
		}
		// Validation messages name the zone, the provider or the hosts
		// that still depend on it; they hold no secret.
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	digest := h.manager.ConfigDigest()
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: name, Code: code, ConfigSHA256: digest})
	writeJSON(w, http.StatusOK, map[string]string{"status": code, "config_sha256": digest})
}

// servedZone returns the normalized zone in the path when the edge serves it.
func servedZone(cfg config.Config, raw string) (string, bool) {
	name, err := config.NormalizeZoneName(raw)
	if err != nil {
		return "", false
	}
	for _, zone := range config.ServedZoneNames(cfg) {
		if zone == name {
			return name, true
		}
	}
	return "", false
}

// checkZone reads the zone's challenge record from DNS and says whether
// issuance can work, with the exact record to create when it cannot.
func (h *Handler) checkZone(w http.ResponseWriter, r *http.Request) {
	cfg := h.manager.Config()
	name, ok := servedZone(cfg, r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "this edge does not serve that zone")
		return
	}
	challenge := config.ChallengeFor(cfg.TLS, name)
	var canonical string
	failed := false
	if challenge.Delegated {
		lookup := h.zoneLookup
		if lookup == nil {
			lookup = net.DefaultResolver.LookupCNAME
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		var err error
		canonical, err = lookup(ctx, challenge.Name)
		cancel()
		failed = err != nil
	}
	result := map[string]any{
		"zone":      name,
		"challenge": domaincheck.AssessChallenge(challenge.Name, challenge.Target, challenge.Delegated, canonical, failed),
	}
	if h.zoneCertificates != nil {
		for _, status := range h.zoneCertificates.Status() {
			if status.Zone == name {
				result["state"] = string(status.State)
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// renewZone asks for an issuance attempt now, within the manager's limits.
func (h *Handler) renewZone(w http.ResponseWriter, r *http.Request) {
	if h.zoneCertificates == nil {
		writeError(w, http.StatusServiceUnavailable, "this edge does not issue its own certificates (tls.dns01)")
		return
	}
	name, ok := servedZone(h.manager.Config(), r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "this edge does not serve that zone")
		return
	}
	if !h.recordMutationIntent(w, r, "zone.renew", name) {
		return
	}
	switch err := h.zoneCertificates.Renew(name); {
	case errors.Is(err, certs.ErrAttemptRunning):
		writeError(w, http.StatusConflict, "an issuance for this zone is already running or queued")
		return
	case errors.Is(err, certs.ErrRenewTooSoon):
		writeError(w, http.StatusTooManyRequests, "the last attempt was less than a minute ago or the CA asked to wait; try again later")
		return
	case errors.Is(err, certs.ErrUnknownZone):
		writeError(w, http.StatusConflict, "the zone is still being set up; try again in a moment")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "renewal could not be requested")
		return
	}
	h.emit(events.Event{Type: events.Type("zone.renew_requested"), Actor: actor(r), Host: name, Code: "renew_requested"})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "renew_requested"})
}
