// Package admin implements Ozal's loopback control plane.
package admin

import (
	"context"
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
	"sync"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/healthcheck"
	"github.com/ozal-project/ozal/internal/routemanager"
)

const maxRequestBody = 1 << 20

type routeManager interface {
	Config() config.Config
	RouteStatuses() []routemanager.RouteStatus
	Reload() error
	ConfigDigest() string
	AddRoute(config.Route) error
	DeleteRoute(string) error
}

// Handler serves the administrative dashboard and authenticated mutations.
type Handler struct {
	devices                *deviceBook
	enrolCommand           string
	github                 repositoryReader
	githubKeyDir           string
	githubStates           manifestStates
	operators              *operatorSessions
	manager                routeManager
	sink                   events.Sink
	broker                 *events.Broker
	certificate            func() CertificateStatus
	zoneCertificates       zoneCertificates
	zoneLookup             func(ctx context.Context, host string) (string, error)
	zoneSetup              zoneSetupService
	auditStatus            func() AuditStatus
	mutationAudit          events.AuditAppender
	auditMutationsRequired bool
	updateStatusFile       string
	pages                  deployments
	identities             identities
	identityMu             sync.Mutex
	ceremonies             *ceremonyBook
	people                 people
	connectors             connectors
	pairing                pairing
	domains                domainService
	mux                    *http.ServeMux
}

func New(manager routeManager, sink events.Sink, opts ...Option) *Handler {
	if sink == nil {
		sink = events.Discard
	}
	h := &Handler{operators: newOperatorSessions(), manager: manager, sink: sink, mux: http.NewServeMux(), ceremonies: newCeremonyBook(), devices: newDeviceBook()}
	h.registerOperators()
	h.mux.HandleFunc("GET /_/console.css", h.consoleCSS)
	h.mux.HandleFunc("GET /_/fonts/manrope.ttf", h.consoleFont)
	// Readiness says only that the process answers. Everything that describes
	// routes, certificates or audit state needs a credential, even on loopback.
	h.mux.HandleFunc("GET /_/healthz", h.healthz)
	h.mux.HandleFunc("GET /_/status", h.requireBearer(h.status))
	h.mux.HandleFunc("GET /_/api/status", h.requireBearer(h.apiStatus))
	h.mux.HandleFunc("GET /_/api/updates/status", h.requireTool("updates", h.updateStatus))
	h.mux.HandleFunc("POST /_/api/updates/status", h.requireLocal(h.reportUpdateStatus))
	h.mux.HandleFunc("GET /_/api/setup", h.requireTool("applications", h.setupStatus))
	h.mux.HandleFunc("POST /_/api/apps/plan", h.requireTool("applications", h.planApplication))
	h.mux.HandleFunc("DELETE /_/api/apps/{host}", h.requireTool("applications", h.removeApplication))
	h.mux.HandleFunc("POST /_/api/apps", h.requireTool("applications", h.saveApplication))
	h.mux.HandleFunc("POST /_/api/apps/{host}/check", h.requireTool("applications", h.checkApplication))
	h.mux.HandleFunc("POST /_/api/apps/{host}/dns", h.requireTool("applications", h.createApplicationRecord))
	h.mux.HandleFunc("PUT /_/api/github", h.requireLocal(h.configureGitHub))
	h.mux.HandleFunc("GET /_/api/github/repositories", h.requireTool("publishing", h.githubRepositories))
	h.mux.HandleFunc("POST /_/api/github/manifest", h.requireTool("publishing", h.githubManifest))
	h.mux.HandleFunc("POST /_/api/github/manifest/conversion", h.requireTool("publishing", h.githubManifestConversion))
	h.mux.HandleFunc("POST /_/api/github/installation", h.requireTool("publishing", h.githubInstallation))
	h.mux.HandleFunc("POST /_/api/github/workflow", h.requireTool("publishing", h.githubWorkflowPreview))
	h.mux.HandleFunc("POST /_/api/projects", h.requireTool("publishing", h.saveGitHubProject))
	h.mux.HandleFunc("POST /_/api/projects/{name}/workflow", h.requireTool("publishing", h.projectWorkflow))
	h.mux.HandleFunc("DELETE /_/api/projects/{name}", h.requireTool("publishing", h.removeProject))
	h.mux.HandleFunc("GET /_/api/projects", h.requireTool("publishing", h.listProjects))
	h.mux.HandleFunc("PUT /_/api/projects/{name}/github-trust", h.requireLocal(h.migrateProjectTrust))
	h.mux.HandleFunc("POST /_/api/projects/{name}/rename", h.requireLocal(h.renameProject))
	h.mux.HandleFunc("POST /_/reload", h.requireTool("applications", h.reload))
	h.mux.HandleFunc("POST /routes", h.requireTool("applications", h.addRoute))
	h.mux.HandleFunc("DELETE /routes/{host}", h.requireTool("applications", h.deleteRoute))
	h.mux.HandleFunc("POST /_/api/policy/explain", h.requireTool("applications", h.explainPolicy))
	h.mux.HandleFunc("GET /_/api/zones", h.requireTool("applications", h.listZones))
	h.mux.HandleFunc("POST /_/api/zones", h.requireTool("applications", h.addZone))
	h.mux.HandleFunc("DELETE /_/api/zones/{name}", h.requireTool("applications", h.removeZone))
	h.mux.HandleFunc("POST /_/api/zones/{name}/check", h.requireTool("applications", h.checkZone))
	h.mux.HandleFunc("POST /_/api/zones/{name}/renew", h.requireTool("applications", h.renewZone))
	h.mux.HandleFunc("GET /_/api/zones/{name}/setup", h.requireTool("applications", h.zoneSetupStatus))
	h.mux.HandleFunc("POST /_/api/zones/{name}/setup", h.requireTool("applications", h.applyZoneSetup))
	for _, opt := range opts {
		opt(h)
	}
	if h.pages != nil {
		h.registerSites()
	}
	if h.identities != nil {
		h.registerIdentities()
	}
	if h.people != nil {
		h.registerPeople()
	}
	if h.connectors != nil {
		h.registerConnectors()
	}
	if h.pairing != nil {
		h.registerPairing()
	}
	if h.broker != nil {
		h.mux.HandleFunc("GET /_/overview", h.requireBearer(h.overview))
		h.mux.HandleFunc("GET /_/events", h.requireBearer(h.stream))
		// The old server-rendered pages moved into the console app.
		h.mux.Handle("GET /_/activity", http.RedirectHandler("/activity", http.StatusMovedPermanently))
		h.mux.Handle("GET /_/simulator", http.RedirectHandler("/simulator", http.StatusMovedPermanently))
		h.mux.HandleFunc("GET /", h.console)
	} else {
		h.mux.Handle("GET /{$}", http.RedirectHandler("/_/status", http.StatusSeeOther))
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w, r)

	// DNS rebinding makes a browser connect to 127.0.0.1 while sending an
	// attacker-controlled Host header; the admin plane only answers names of
	// the local machine.
	if !loopbackHost(r.Host) {
		http.Error(w, "admin listener accepts loopback hosts only", http.StatusMisdirectedRequest)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// setSecurityHeaders applies the baseline policy to every admin response.
// Pages override the CSP with their own richer policy; HSTS only goes out on
// TLS connections (the loopback admin listener is plain HTTP today).
func setSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if r.TLS != nil {
		hdr.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// loopbackHost reports whether an admin Host header names only the local
// machine: an IP in 127.0.0.0/8 or ::1, or an exact current/legacy console name.
func loopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, "ozal.localhost") || strings.EqualFold(host, "switchboard.localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CertificateStatus is the public listener's origin-certificate state as
// the trust console displays it. The composition root projects the
// certificate manager into this shape, so admin needs no dependency on the
// certificate package.
type CertificateStatus struct {
	Host string `json:"host"`
	// Names lists every name the certificate is configured to cover, one per
	// entry; Host joins them for older readers.
	Names       []string `json:"names,omitempty"`
	Mode        string   `json:"mode"`
	Valid       bool     `json:"valid"`
	NotAfter    string   `json:"not_after"`
	LastOutcome string   `json:"last_outcome"`
	LastError   string   `json:"last_error"`
	LastAttempt string   `json:"last_attempt"`
	NextAttempt string   `json:"next_attempt"`
	Pending     int      `json:"pending"`
}

// AuditStatus is an operational projection for the configured durable access
// audit. FailureCount and LastFailureAt are process-local; no filesystem path
// or raw I/O error is exposed to the console.
type AuditStatus struct {
	Available     bool   `json:"available"`
	FailureCount  uint64 `json:"failure_count"`
	LastFailureAt string `json:"last_failure_at,omitempty"`
}

type statusView struct {
	Active      string
	Protected   int
	Public      int
	Static      int
	HasActivity bool
	GeneratedAt string
	Certificate *CertificateStatus
	Audit       *AuditStatus
	Routes      []routeView
}

type routeView struct {
	Access         string         `json:"access"`
	Policy         string         `json:"policy"`
	Kind           string         `json:"kind"`
	IsStatic       bool           `json:"is_static"`
	IsTunnel       bool           `json:"is_tunnel"`
	Host           string         `json:"host"`
	Target         string         `json:"target"`
	HealthPath     string         `json:"health_path"`
	State          string         `json:"state"`
	StateClass     string         `json:"state_class"`
	Latency        string         `json:"latency"`
	LastCheck      string         `json:"last_check"`
	LastTransition string         `json:"last_transition"`
	LastError      string         `json:"last_error"`
	Upstreams      []upstreamView `json:"upstreams,omitempty"`
}

type upstreamView struct {
	Target     string `json:"target"`
	State      string `json:"state"`
	StateClass string `json:"state_class"`
	LastCheck  string `json:"last_check"`
	LastError  string `json:"last_error,omitempty"`
}

// buildStatusView projects route health and the certificate state; both the
// HTML debug view and the JSON API render it, so they always agree.
func (h *Handler) buildStatusView() statusView {
	statuses := h.manager.RouteStatuses()
	view := statusView{
		Active:      "routes",
		HasActivity: h.broker != nil,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Routes:      make([]routeView, len(statuses)),
	}
	if h.certificate != nil {
		status := h.certificate()
		view.Certificate = &status
	}
	if h.auditStatus != nil {
		status := h.auditStatus()
		view.Audit = &status
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
		if status.Route.IsRedirect() {
			kind = "Redirect"
			state, stateClass, healthPath = "Not probed", "unknown", "Not probed"
		}
		if status.Route.Connector != "" {
			kind = "Tunnel connector"
			state, stateClass = "Connector offline", "unhealthy"
			if status.ConnectorConnected {
				state, stateClass = "Connector connected", "healthy"
			}
			healthPath = "Not probed"
		}
		view.Routes[i] = routeView{
			Access: access, Policy: policyLabel(status.Route), Kind: kind, IsStatic: status.Route.IsStatic(), IsTunnel: status.Route.Connector != "",
			Host:           status.Route.Host,
			Target:         status.Route.Destination(),
			HealthPath:     healthPath,
			State:          state,
			StateClass:     stateClass,
			Latency:        latency,
			LastCheck:      lastCheck,
			LastTransition: lastTransition,
			LastError:      status.Health.LastError,
			Upstreams:      upstreamViews(status.Upstreams),
		}
	}
	return view
}

func upstreamViews(statuses []healthcheck.TargetStatus) []upstreamView {
	views := make([]upstreamView, 0, len(statuses))
	for _, target := range statuses {
		state := "Unchecked"
		stateClass := "unknown"
		lastCheck := "-"
		if target.Status.Checked {
			state = "Healthy"
			stateClass = "healthy"
			if !target.Status.Healthy {
				state = "Unhealthy"
				stateClass = "unhealthy"
			}
			lastCheck = target.Status.LastCheck.UTC().Format(time.RFC3339)
		}
		views = append(views, upstreamView{
			Target: target.Target, State: state, StateClass: stateClass,
			LastCheck: lastCheck, LastError: target.Status.LastError,
		})
	}
	return views
}

// healthz is the unauthenticated readiness probe for service managers,
// installers and tunnels. It reveals nothing about the configuration.
func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, "ok\n")
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	view := h.buildStatusView()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := dashboardTemplate.Execute(w, view); err != nil {
		log.Printf("render admin status: %v", err)
	}
}

func (h *Handler) reload(w http.ResponseWriter, r *http.Request) {
	if !h.recordMutationIntent(w, r, "config.reload", "") {
		return
	}
	if err := h.manager.Reload(); err != nil {
		h.emit(events.Event{
			Type: events.ConfigReloadFailed, Actor: actor(r), Code: "config.reload_failed",
			Reason: "the configuration could not be loaded or validated", ConfigSHA256: h.manager.ConfigDigest(),
		})
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.emit(events.Event{Type: events.ConfigReloaded, Actor: actor(r), ConfigSHA256: h.manager.ConfigDigest()})
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

func (h *Handler) addRoute(w http.ResponseWriter, r *http.Request) {
	var route config.Route
	if err := decodeJSON(w, r, &route); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.allowOperatorRoot(w, r, route, false) {
		return
	}
	if !h.recordMutationIntent(w, r, "route.create", safeRouteEventHost(route.Host)) {
		return
	}
	if err := h.manager.AddRoute(route); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, routemanager.ErrRouteExists) {
			status = http.StatusConflict
		} else if errors.Is(err, routemanager.ErrPersistence) {
			status = http.StatusInternalServerError
		}
		h.emit(events.Event{
			Type: events.RouteCreateFailed, Actor: actor(r), Host: safeRouteEventHost(route.Host),
			Code: routeMutationFailureCode(err), Reason: "the route could not be created",
		})
		writeError(w, status, err.Error())
		return
	}
	h.emit(events.Event{Type: events.RouteCreated, Actor: actor(r), Host: route.Host})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !h.recordMutationIntent(w, r, "route.delete", safeRouteEventHost(host)) {
		return
	}
	if err := h.manager.DeleteRoute(host); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, routemanager.ErrRouteNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, routemanager.ErrPersistence) {
			status = http.StatusInternalServerError
		}
		h.emit(events.Event{
			Type: events.RouteDeleteFailed, Actor: actor(r), Host: safeRouteEventHost(host),
			Code: routeMutationFailureCode(err), Reason: "the route could not be removed",
		})
		writeError(w, status, err.Error())
		return
	}
	h.emit(events.Event{Type: events.RouteDeleted, Actor: actor(r), Host: host})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func routeMutationFailureCode(err error) string {
	switch {
	case errors.Is(err, routemanager.ErrRouteExists):
		return "route.already_exists"
	case errors.Is(err, routemanager.ErrRouteNotFound):
		return "route.not_found"
	case errors.Is(err, routemanager.ErrPersistence):
		return "route.persistence_failed"
	default:
		return "route.invalid_or_unavailable"
	}
}

func safeRouteEventHost(host string) string {
	normalized, err := config.NormalizeHost(host)
	if err != nil {
		return ""
	}
	return normalized
}

// requireBearer admits the host socket, the admin token or any operator
// session. Use requireTool for anything that changes or reveals one console
// area.
func (h *Handler) requireBearer(next http.HandlerFunc) http.HandlerFunc {
	return h.bearer(next, false, "")
}

// requireTool admits the admin token, the host socket, or an operator session
// whose host login granted this tool. A session without it gets 403, so a
// login for one area never reaches another.
func (h *Handler) requireTool(tool string, next http.HandlerFunc) http.HandlerFunc {
	return h.bearer(next, false, tool)
}

// requireSitesBearer admits the admin token or admin.sites_token. The sites
// token lives on the machines that publish sites; it can deploy and roll
// back a site and nothing else, so losing one of those machines does not
// hand over people, routes or policy.
func (h *Handler) requireSitesBearer(next http.HandlerFunc) http.HandlerFunc {
	return h.bearer(next, true, "sites")
}

func (h *Handler) bearer(next http.HandlerFunc, sites bool, tool string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// The unix socket is already the host operator's credential.
		if isLocal(r) {
			next(w, r)
			return
		}
		if operatorRequest, ok := h.withOperator(r); ok {
			if grant, _ := operatorRequest.Context().Value(operatorKey{}).(operatorGrant); tool != "" && !grant.allows(tool) {
				writeError(w, http.StatusForbidden, "this operator session does not include the "+tool+" tool; ask the host for a login that does")
				return
			}
			next(w, operatorRequest)
			return
		}
		cfg := h.manager.Config().Admin
		token, err := config.ResolveSecret(cfg.Token)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin token reference could not be resolved")
			return
		}
		accepted := []string{}
		if token != "" {
			accepted = append(accepted, token)
		}
		if sites && cfg.SitesToken != "" {
			// A sites token that cannot be read only takes itself out: the
			// admin token keeps working while the file is being set up.
			if sitesToken, err := config.ResolveSecret(cfg.SitesToken); err == nil && sitesToken != "" {
				accepted = append(accepted, sitesToken)
			}
		}
		if len(accepted) == 0 {
			writeError(w, http.StatusServiceUnavailable, "admin mutations are disabled until admin.token is configured")
			return
		}
		// Require the Bearer scheme (case-insensitive per RFC 7235); a bare
		// token is as invalid as no token. "Bearer " with an empty token fails
		// the hash comparison below.
		scheme, provided, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || !matchesAny(provided, accepted) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next(w, r)
	}
}

// matchesAny compares in constant time against every candidate, without
// stopping at the first match.
func matchesAny(provided string, candidates []string) bool {
	providedHash := sha256.Sum256([]byte(provided))
	match := 0
	for _, candidate := range candidates {
		wantHash := sha256.Sum256([]byte(candidate))
		match |= subtle.ConstantTimeCompare(providedHash[:], wantHash[:])
	}
	return match == 1
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
	if operator, ok := r.Context().Value(operatorKey{}).(operatorGrant); ok {
		return "operator:" + operator.Name
	}
	if isLocal(r) {
		return localActor(r)
	}
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
