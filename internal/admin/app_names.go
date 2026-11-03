package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/store/sqlite"
)

// appNames is the display name of each application (sqlite.AppNames). A
// name is a label for people reading the console; no decision reads it.
type appNames interface {
	Names(ctx context.Context) (map[string]string, error)
	SetName(ctx context.Context, host, name string) error
}

// WithAppNames lets operators name applications in the console.
func WithAppNames(store appNames) Option {
	return func(h *Handler) { h.appNames = store }
}

// namesOf returns the stored names of the configured hosts only, so a name
// left behind by a removed route is never shown.
func (h *Handler) namesOf(ctx context.Context, routes []config.Route) map[string]string {
	out := map[string]string{}
	if h.appNames == nil {
		return out
	}
	names, err := h.appNames.Names(ctx)
	if err != nil {
		return out
	}
	for _, rt := range routes {
		if name, ok := names[rt.Host]; ok {
			out[rt.Host] = name
		}
	}
	return out
}

func (h *Handler) renameApplication(w http.ResponseWriter, r *http.Request) {
	if h.appNames == nil {
		writeError(w, http.StatusServiceUnavailable, "application names need the state database")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid name request")
		return
	}
	host, ok := configuredHost(h.manager.Config(), r.PathValue("host"))
	if !ok {
		writeError(w, http.StatusNotFound, "no application uses this hostname")
		return
	}
	name, err := sqlite.NormalizeAppName(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.recordMutationIntent(w, r, "application.rename", safeRouteEventHost(host)) {
		return
	}
	if err := h.appNames.SetName(r.Context(), host, name); err != nil {
		if errors.Is(err, sqlite.ErrInvalidAppName) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "the name could not be saved")
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Host: safeRouteEventHost(host), Code: "application.renamed"})
	writeJSON(w, http.StatusOK, map[string]string{"host": host, "name": name})
}
