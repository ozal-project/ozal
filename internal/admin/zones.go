package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/routemanager"
)

// zoneCertificates is what the zones page reads from the per-zone
// certificate set (EDGE-008).
type zoneCertificates interface {
	Status() []certs.ZoneStatus
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
				view.NotAfter = zoneTime(status.NotAfter)
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
