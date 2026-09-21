// Package admin implements Switchboard's loopback control plane.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/routemanager"
)

const maxRequestBody = 1 << 20

type routeManager interface {
	Config() config.Config
	RouteStatuses() []routemanager.RouteStatus
	Reload() error
	AddRoute(config.Route) error
	DeleteRoute(string) error
}

type auditLogger interface {
	Printf(string, ...any)
}

// Handler serves the administrative dashboard and authenticated mutations.
type Handler struct {
	manager routeManager
	audit   auditLogger
	mux     *http.ServeMux
}

func New(manager routeManager, audit auditLogger) *Handler {
	if audit == nil {
		audit = log.Default()
	}
	h := &Handler{manager: manager, audit: audit, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /_/status", h.status)
	h.mux.HandleFunc("POST /_/reload", h.requireBearer(h.reload))
	h.mux.HandleFunc("POST /routes", h.requireBearer(h.addRoute))
	h.mux.HandleFunc("DELETE /routes/{host}", h.requireBearer(h.deleteRoute))
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type statusView struct {
	GeneratedAt string
	Routes      []routeView
}

type routeView struct {
	Host           string
	Target         string
	HealthPath     string
	State          string
	StateClass     string
	Latency        string
	LastCheck      string
	LastTransition string
	LastError      string
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	statuses := h.manager.RouteStatuses()
	view := statusView{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Routes:      make([]routeView, len(statuses)),
	}
	for i, status := range statuses {
		healthPath := status.Route.HealthPath
		if healthPath == "" {
			healthPath = "/"
		}
		state := "Unchecked"
		stateClass := "unknown"
		latency := "-"
		lastCheck := "-"
		lastTransition := "-"
		if status.Health.Checked {
			state = "Healthy"
			stateClass = "healthy"
			if !status.Health.Healthy {
				state = "Unhealthy"
				stateClass = "unhealthy"
			}
			latency = status.Health.Latency.Round(time.Microsecond).String()
			lastCheck = status.Health.LastCheck.UTC().Format(time.RFC3339)
			lastTransition = status.Health.LastTransition.UTC().Format(time.RFC3339)
		}
		view.Routes[i] = routeView{
			Host:           status.Route.Host,
			Target:         status.Route.Target,
			HealthPath:     healthPath,
			State:          state,
			StateClass:     stateClass,
			Latency:        latency,
			LastCheck:      lastCheck,
			LastTransition: lastTransition,
			LastError:      status.Health.LastError,
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := dashboardTemplate.Execute(w, view); err != nil {
		log.Printf("render admin status: %v", err)
	}
}

func (h *Handler) reload(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.Reload(); err != nil {
		h.audit.Printf("audit actor=%q action=config.reload result=rejected error=%q", actor(r), err)
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.audit.Printf("audit actor=%q action=config.reload result=accepted", actor(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

func (h *Handler) addRoute(w http.ResponseWriter, r *http.Request) {
	var route config.Route
	if err := decodeJSON(w, r, &route); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.manager.AddRoute(route); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, routemanager.ErrRouteExists) {
			status = http.StatusConflict
		} else if errors.Is(err, routemanager.ErrPersistence) {
			status = http.StatusInternalServerError
		}
		h.audit.Printf("audit actor=%q action=route.create host=%q result=rejected error=%q", actor(r), route.Host, err)
		writeError(w, status, err.Error())
		return
	}
	h.audit.Printf("audit actor=%q action=route.create host=%q result=accepted", actor(r), route.Host)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if err := h.manager.DeleteRoute(host); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, routemanager.ErrRouteNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, routemanager.ErrPersistence) {
			status = http.StatusInternalServerError
		}
		h.audit.Printf("audit actor=%q action=route.delete host=%q result=rejected error=%q", actor(r), host, err)
		writeError(w, status, err.Error())
		return
	}
	h.audit.Printf("audit actor=%q action=route.delete host=%q result=accepted", actor(r), host)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) requireBearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := h.manager.Config().Admin.Token
		if token == "" {
			writeError(w, http.StatusServiceUnavailable, "admin mutations are disabled until admin.token is configured")
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		wantHash := sha256.Sum256([]byte(token))
		providedHash := sha256.Sum256([]byte(provided))
		if subtle.ConstantTimeCompare(providedHash[:], wantHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next(w, r)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid JSON body: multiple values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write admin response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func actor(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "unknown"
	}
	return r.RemoteAddr
}

var dashboardTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="10">
  <title>Switchboard Status</title>
  <style>
    :root { color-scheme: light; --ink: #19211d; --muted: #65716b; --line: #d9dedb; --paper: #f7f8f6; --surface: #ffffff; --green: #1f7a4c; --red: #b13a35; --amber: #8a651a; }
    * { box-sizing: border-box; }
    body { margin: 0; background: var(--paper); color: var(--ink); font: 14px/1.5 ui-sans-serif, system-ui, sans-serif; }
    header { border-bottom: 1px solid var(--line); background: var(--surface); }
    header div, main { width: min(1180px, calc(100% - 32px)); margin: 0 auto; }
    header div { min-height: 64px; display: flex; align-items: center; justify-content: space-between; gap: 24px; }
    h1 { margin: 0; font: 600 20px/1.2 ui-monospace, SFMono-Regular, Consolas, monospace; letter-spacing: 0; }
    .updated { color: var(--muted); font-size: 12px; }
    main { padding: 32px 0 48px; overflow-x: auto; }
    table { width: 100%; min-width: 900px; border-collapse: collapse; background: var(--surface); border: 1px solid var(--line); }
    th, td { padding: 12px 14px; border-bottom: 1px solid var(--line); text-align: left; vertical-align: top; }
    th { color: var(--muted); background: #eef1ef; font-size: 11px; font-weight: 700; text-transform: uppercase; }
    tbody tr:last-child td { border-bottom: 0; }
    code { font: 12px/1.5 ui-monospace, SFMono-Regular, Consolas, monospace; overflow-wrap: anywhere; }
    .state { display: inline-flex; align-items: center; gap: 7px; font-weight: 650; white-space: nowrap; }
    .state::before { width: 8px; height: 8px; border-radius: 50%; background: var(--amber); content: ""; }
    .state.healthy { color: var(--green); } .state.healthy::before { background: var(--green); }
    .state.unhealthy { color: var(--red); } .state.unhealthy::before { background: var(--red); }
    .error { max-width: 280px; color: var(--red); overflow-wrap: anywhere; }
    .empty { padding: 40px 16px; color: var(--muted); text-align: center; }
    @media (max-width: 640px) { header div { align-items: flex-start; flex-direction: column; gap: 4px; padding: 14px 0; } main { padding-top: 20px; } }
  </style>
</head>
<body>
  <header><div><h1>Switchboard</h1><span class="updated">Updated {{.GeneratedAt}}</span></div></header>
  <main>
    <table>
      <thead><tr><th>Route</th><th>Upstream</th><th>Health</th><th>Latency</th><th>Last check</th><th>Last transition</th><th>Error</th></tr></thead>
      <tbody>
      {{range .Routes}}<tr>
        <td><strong>{{.Host}}</strong><br><code>{{.HealthPath}}</code></td>
        <td><code>{{.Target}}</code></td>
        <td><span class="state {{.StateClass}}">{{.State}}</span></td>
        <td>{{.Latency}}</td><td>{{.LastCheck}}</td><td>{{.LastTransition}}</td>
        <td class="error">{{.LastError}}</td>
      </tr>{{else}}<tr><td class="empty" colspan="7">No routes configured.</td></tr>{{end}}
      </tbody>
    </table>
  </main>
</body>
</html>`))
