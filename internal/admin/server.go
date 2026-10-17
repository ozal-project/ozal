// Package admin implements Switchboard's loopback control plane.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
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

// Handler serves the administrative dashboard and authenticated mutations.
type Handler struct {
	manager routeManager
	sink    events.Sink
	broker  *events.Broker
	mux     *http.ServeMux
}

func New(manager routeManager, sink events.Sink, opts ...Option) *Handler {
	if sink == nil {
		sink = events.Discard
	}
	h := &Handler{manager: manager, sink: sink, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		target := "/_/status"
		if h.broker != nil {
			target = "/_/activity"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
	h.mux.HandleFunc("GET /_/console.css", h.consoleCSS)
	h.mux.HandleFunc("GET /_/fonts/manrope.ttf", h.consoleFont)
	h.mux.HandleFunc("GET /_/status", h.status)
	h.mux.HandleFunc("POST /_/reload", h.requireBearer(h.reload))
	h.mux.HandleFunc("POST /routes", h.requireBearer(h.addRoute))
	h.mux.HandleFunc("DELETE /routes/{host}", h.requireBearer(h.deleteRoute))
	for _, opt := range opts {
		opt(h)
	}
	if h.broker != nil {
		h.mux.HandleFunc("GET /_/overview", h.overview)
		h.mux.HandleFunc("GET /_/activity", h.activityPage)
		h.mux.HandleFunc("GET /_/activity.js", h.activityScript)
		h.mux.HandleFunc("GET /_/events", h.stream)
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// DNS rebinding makes a browser connect to 127.0.0.1 while sending an
	// attacker-controlled Host header; the admin plane only answers names of
	// the local machine.
	if !loopbackHost(r.Host) {
		http.Error(w, "admin listener accepts loopback hosts only", http.StatusMisdirectedRequest)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// loopbackHost reports whether an admin Host header names only the local
// machine: an IP in 127.0.0.0/8 or ::1, or the localhost name.
func loopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type statusView struct {
	Active      string
	Protected   int
	Public      int
	Static      int
	HasActivity bool
	GeneratedAt string
	Routes      []routeView
}

type routeView struct {
	Access         string
	Policy         string
	Kind           string
	IsStatic       bool
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
		Active:      "routes",
		HasActivity: h.broker != nil,
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

		access := "Public"
		if status.Route.RequiresAuth() {
			access = "Protected"
			view.Protected++
		} else {
			view.Public++
		}
		kind := "HTTP proxy"
		if status.Route.IsStatic() {
			kind = "Static files"
			view.Static++
			state = "Not probed"
			stateClass = "unknown"
		}
		view.Routes[i] = routeView{
			Access: access, Policy: policyLabel(status.Route), Kind: kind, IsStatic: status.Route.IsStatic(),
			Host:           status.Route.Host,
			Target:         status.Route.Destination(),
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := dashboardTemplate.Execute(w, view); err != nil {
		log.Printf("render admin status: %v", err)
	}
}

func (h *Handler) reload(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.Reload(); err != nil {
		h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), Error: err.Error()})
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r)})
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
		h.emit(events.Event{Type: events.RouteCreated, Actor: actor(r), Host: route.Host, Error: err.Error()})
		writeError(w, status, err.Error())
		return
	}
	h.emit(events.Event{Type: events.RouteCreated, Actor: actor(r), Host: route.Host})
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
		h.emit(events.Event{Type: events.RouteDeleted, Actor: actor(r), Host: host, Error: err.Error()})
		writeError(w, status, err.Error())
		return
	}
	h.emit(events.Event{Type: events.RouteDeleted, Actor: actor(r), Host: host})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) requireBearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := config.ResolveSecret(h.manager.Config().Admin.Token)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin token reference could not be resolved")
			return
		}
		if token == "" {
			writeError(w, http.StatusServiceUnavailable, "admin mutations are disabled until admin.token is configured")
			return
		}
		// Require the Bearer scheme (case-insensitive per RFC 7235); a bare
		// token is as invalid as no token. "Bearer " with an empty token fails
		// the hash comparison below.
		scheme, provided, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
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

// emit publishes a control-plane event with the timestamp filled in.
func (h *Handler) emit(e events.Event) {
	e.Time = time.Now()
	h.sink.Emit(e)
}
