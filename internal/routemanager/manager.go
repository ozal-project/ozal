// Package routemanager builds and atomically swaps complete routing snapshots.
package routemanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/healthcheck"
	"github.com/octaviosantacruz/switchboard/internal/pipeline"
	"github.com/octaviosantacruz/switchboard/internal/proxy"
	"github.com/octaviosantacruz/switchboard/internal/ratelimit"
)

// Option configures the manager.
type Option func(*Manager)

// WithEventSink publishes auth, access and control-plane events to sink.
func WithEventSink(sink events.Sink) Option {
	return func(m *Manager) {
		if sink != nil {
			m.sink = sink
		}
	}
}

var (
	ErrRouteExists   = errors.New("route already exists")
	ErrRouteNotFound = errors.New("route not found")
	ErrPersistence   = errors.New("config persistence failed")
)

// RouteStatus combines the immutable route configuration with its latest
// health observation.
type RouteStatus struct {
	Route  config.Route
	Health healthcheck.Status
}

// Snapshot is immutable after construction except for the checker state. Its
// fields stay private so a request observes one complete routing generation.
type Snapshot struct {
	cfg           config.Config
	handler       http.Handler
	checker       *healthcheck.Checker
	healthCancel  context.CancelFunc
	gate          *auth.Gate
	gateCancel    context.CancelFunc
	sessions      *auth.SessionStore
	sessionCancel context.CancelFunc
	// transport carries every upstream request of this generation. It is the
	// previous generation's when the timeout is unchanged, so a reload keeps
	// reusing warm connections.
	transport        *http.Transport
	transportTimeout time.Duration
}

// Manager owns the current routing snapshot. Reloads and mutations are
// serialized while requests only pay for one atomic load.
type Manager struct {
	ctx                 context.Context
	configPath          string
	healthCheckInterval time.Duration
	reloadMu            sync.Mutex
	current             atomic.Pointer[Snapshot]
	gate                *auth.Gate
	gateCancel          context.CancelFunc
	gateAuth            config.Auth
	gateHosts           []string
	sessions            *auth.SessionStore
	sessionTTL          time.Duration
	sessionCancel       context.CancelFunc
	transport           *http.Transport
	transportTimeout    time.Duration
	sink                events.Sink
}

// New builds and installs the initial snapshot from configPath.
func New(ctx context.Context, configPath string, healthCheckInterval time.Duration, opts ...Option) (*Manager, error) {
	if healthCheckInterval <= 0 {
		return nil, fmt.Errorf("health check interval must be positive")
	}
	manager := &Manager{
		ctx:                 ctx,
		configPath:          configPath,
		healthCheckInterval: healthCheckInterval,
		sink:                events.Discard,
	}
	for _, opt := range opts {
		opt(manager)
	}
	if err := manager.Reload(); err != nil {
		return nil, err
	}
	return manager, nil
}

// Reload builds a complete candidate from disk before atomically installing
// it. A failed candidate never changes the snapshot serving requests.
func (m *Manager) Reload() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if err := m.contextError(); err != nil {
		return err
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return m.reloadError(err)
	}
	candidate, err := m.build(*cfg)
	if err != nil {
		return m.reloadError(err)
	}
	m.install(candidate)
	return nil
}

// AddRoute validates, persists, and installs a new route as one serialized
// operation. Existing normalized hosts are rejected.
func (m *Manager) AddRoute(route config.Route) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil {
		return fmt.Errorf("no routing snapshot available")
	}
	host, err := config.NormalizeHost(route.Host)
	if err != nil {
		return fmt.Errorf("invalid route host %q: %w", route.Host, err)
	}
	for _, existing := range current.cfg.Routes {
		if existing.Host == host {
			return fmt.Errorf("%w: %s", ErrRouteExists, host)
		}
	}

	cfg := cloneConfig(current.cfg)
	route.Host = host
	cfg.Routes = append(cfg.Routes, route)
	return m.persistAndInstall(cfg)
}

// DeleteRoute validates, persists, and installs a configuration without host.
func (m *Manager) DeleteRoute(host string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil {
		return fmt.Errorf("no routing snapshot available")
	}
	normalizedHost, err := config.NormalizeHost(host)
	if err != nil {
		return fmt.Errorf("invalid route host %q: %w", host, err)
	}

	cfg := cloneConfig(current.cfg)
	index := -1
	for i, route := range cfg.Routes {
		if route.Host == normalizedHost {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("%w: %s", ErrRouteNotFound, normalizedHost)
	}
	cfg.Routes = append(cfg.Routes[:index], cfg.Routes[index+1:]...)
	return m.persistAndInstall(cfg)
}

// Config returns a deep copy of the active configuration.
func (m *Manager) Config() config.Config {
	snapshot := m.current.Load()
	if snapshot == nil {
		return config.Config{}
	}
	return cloneConfig(snapshot.cfg)
}

// RouteStatuses returns one consistent routing generation and a copy of each
// route's latest health state.
func (m *Manager) RouteStatuses() []RouteStatus {
	snapshot := m.current.Load()
	if snapshot == nil {
		return nil
	}
	statuses := make([]RouteStatus, len(snapshot.cfg.Routes))
	for i, route := range snapshot.cfg.Routes {
		statuses[i] = RouteStatus{
			Route:  cloneRoute(route),
			Health: snapshot.checker.Status(route.Host),
		}
	}
	return statuses
}

// ServeHTTP delegates the entire request to one immutable snapshot.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snapshot := m.current.Load()
	if snapshot == nil {
		http.Error(w, "no routing snapshot available", http.StatusServiceUnavailable)
		return
	}
	snapshot.handler.ServeHTTP(w, r)
}

func (m *Manager) persistAndInstall(cfg config.Config) error {
	candidate, err := m.build(cfg)
	if err != nil {
		return err
	}
	if err := config.WriteAtomic(m.configPath, candidate.cfg); err != nil {
		m.discard(candidate)
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	m.install(candidate)
	return nil
}

func (m *Manager) build(raw config.Config) (_ *Snapshot, err error) {
	cfg, err := config.Validate(raw)
	if err != nil {
		return nil, err
	}
	if current := m.current.Load(); current != nil && !sameServerConfig(current.cfg, *cfg) {
		return nil, fmt.Errorf("listen, admin listen, and TLS settings cannot change without restarting Switchboard")
	}

	healthCtx, healthCancel := context.WithCancel(m.ctx)
	var gateCancel context.CancelFunc
	var sessionCancel context.CancelFunc
	defer func() {
		if err != nil {
			healthCancel()
			if gateCancel != nil {
				gateCancel()
			}
			if sessionCancel != nil {
				sessionCancel()
			}
		}
	}()

	var gate proxy.AuthGate
	var oidcGate *auth.Gate
	var sessions *auth.SessionStore
	protectedHosts := protectedRouteHosts(cfg.Routes)
	if len(protectedHosts) > 0 {
		// The session store outlives gate rebuilds: adding a protected route
		// must not log everyone out. A changed session TTL is a policy change,
		// so it gets a fresh store; the candidate is promoted only on install.
		sessions = m.sessions
		if sessions == nil || m.sessionTTL != auth.SessionTTL(cfg.Auth) {
			storeCtx, cancelStore := context.WithCancel(m.ctx)
			sessions = auth.NewSessionStore(auth.SessionTTL(cfg.Auth))
			sessions.StartCleanup(storeCtx, auth.SessionCleanupInterval)
			sessionCancel = cancelStore
		}
		if m.gate != nil && m.gateAuth == cfg.Auth && slices.Equal(m.gateHosts, protectedHosts) {
			oidcGate = m.gate
		} else {
			// Resolve the secret only for the consumer; cfg keeps the raw
			// reference so control-plane writes persist it unchanged.
			authConfig := cfg.Auth
			authConfig.ClientSecret, err = config.ResolveSecret(cfg.Auth.ClientSecret)
			if err != nil {
				return nil, fmt.Errorf("resolve auth.client_secret: %w", err)
			}
			gateCtx, cancelGate := context.WithCancel(m.ctx)
			oidcGate, err = auth.NewGate(gateCtx, authConfig, protectedHosts, sessions, m.sink)
			if err != nil {
				cancelGate()
				return nil, fmt.Errorf("build auth gate: %w", err)
			}
			gateCancel = cancelGate
		}
		gate = oidcGate
	}

	checker := healthcheck.New()
	var proxyOpts []proxy.Option
	trustedProxies, err := config.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	// Same pattern as the gate and the sessions: keep the installed transport
	// while its settings are unchanged, build a new one when they change.
	transport, transportTimeout := m.transport, cfg.UpstreamTimeout()
	if transport == nil || m.transportTimeout != transportTimeout {
		transport = proxy.NewUpstreamTransport(transportTimeout)
	}
	proxyOpts = append(proxyOpts, proxy.WithTrustedProxies(trustedProxies), proxy.WithEventSink(m.sink), proxy.WithTransport(transport))
	if cfg.DevErrors {
		proxyOpts = append(proxyOpts, proxy.WithDevErrors())
	}
	proxyHandler, err := proxy.New(cfg.Routes, checker, gate, proxyOpts...)
	if err != nil {
		return nil, fmt.Errorf("build proxy: %w", err)
	}
	handler := withAuthEndpoints(proxyHandler, oidcGate, authEndpointHosts(*cfg, protectedHosts))
	if rl := cfg.RateLimit; rl != nil {
		// Outside withAuthEndpoints so /_/auth/* is limited too. Trust runs
		// first for the client IP; the copy inside the proxy is a no-op.
		const idle = 10 * time.Minute
		handler = pipeline.Chain(
			pipeline.TrustWithProxies(trustedProxies),
			pipeline.RateLimit(
				ratelimit.NewLimiter(rl.General.Rate, rl.General.Burst, idle),
				ratelimit.NewLimiter(rl.Auth.Rate, rl.Auth.Burst, idle),
				"/_/auth/",
			),
		)(handler)
	}
	checker.Start(healthCtx, cfg.Routes, m.healthCheckInterval)
	if err := m.contextError(); err != nil {
		return nil, err
	}

	return &Snapshot{
		cfg:           cloneConfig(*cfg),
		handler:       handler,
		checker:       checker,
		healthCancel:  healthCancel,
		gate:          oidcGate,
		gateCancel:    gateCancel,
		sessions:      sessions,
		sessionCancel: sessionCancel,

		transport:        transport,
		transportTimeout: transportTimeout,
	}, nil
}

func (m *Manager) install(candidate *Snapshot) {
	previous := m.current.Swap(candidate)
	if previous != nil {
		previous.healthCancel()
	}
	if candidate.gate != m.gate {
		oldGateCancel := m.gateCancel
		m.gate = candidate.gate
		m.gateCancel = candidate.gateCancel
		m.gateAuth = candidate.cfg.Auth
		m.gateHosts = protectedRouteHosts(candidate.cfg.Routes)
		candidate.gateCancel = nil
		if oldGateCancel != nil {
			oldGateCancel()
		}
	}
	if candidate.transport != m.transport {
		old := m.transport
		m.transport, m.transportTimeout = candidate.transport, candidate.transportTimeout
		if old != nil {
			// Requests already in flight finish on it; only its idle
			// connections are closed, and none are opened again.
			old.CloseIdleConnections()
		}
	}
	if candidate.sessions != m.sessions {
		oldSessionCancel := m.sessionCancel
		m.sessions = candidate.sessions
		m.sessionTTL = auth.SessionTTL(candidate.cfg.Auth)
		m.sessionCancel = candidate.sessionCancel
		candidate.sessionCancel = nil
		if oldSessionCancel != nil {
			oldSessionCancel()
		}
	}
}

func (m *Manager) discard(candidate *Snapshot) {
	candidate.healthCancel()
	if candidate.gateCancel != nil {
		candidate.gateCancel()
	}
	if candidate.sessionCancel != nil {
		candidate.sessionCancel()
	}
}

func (m *Manager) contextError() error {
	if err := m.ctx.Err(); err != nil {
		return fmt.Errorf("route manager context is closed: %w", err)
	}
	return nil
}

func (m *Manager) reloadError(err error) error {
	if m.current.Load() != nil {
		log.Printf("route reload rejected; keeping last-good snapshot: %v", err)
	}
	return err
}

// authEndpointHosts returns the hosts where /_/auth/* belongs to Switchboard:
// the protected routes, plus the redirect_url host where the OIDC callback
// lands. On any other host the path is the upstream's, not ours.
func authEndpointHosts(cfg config.Config, protectedHosts []string) map[string]bool {
	hosts := make(map[string]bool, len(protectedHosts)+1)
	for _, host := range protectedHosts {
		hosts[host] = true
	}
	if redirect, err := url.Parse(cfg.Auth.RedirectURL); err == nil && redirect.Host != "" {
		if host, err := config.NormalizeHost(redirect.Host); err == nil {
			hosts[host] = true
		}
	}
	return hosts
}

func withAuthEndpoints(next http.Handler, gate *auth.Gate, hosts map[string]bool) http.Handler {
	if gate == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := config.NormalizeHost(r.Host)
		if err != nil || !hosts[host] {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/_/auth/login":
			gate.HandleLogin(w, r)
		case "/_/auth/callback":
			gate.HandleCallback(w, r)
		case "/_/auth/logout":
			// Logout is a state change: a GET would let any page log the
			// user out with an image tag.
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			gate.HandleLogout(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func protectedRouteHosts(routes []config.Route) []string {
	hosts := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.RequiresAuth() {
			hosts = append(hosts, route.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

func sameServerConfig(a, b config.Config) bool {
	return a.Listen == b.Listen &&
		a.Admin.Listen == b.Admin.Listen &&
		a.TLS.Enabled == b.TLS.Enabled &&
		a.TLS.CacheDir == b.TLS.CacheDir &&
		a.TLS.Email == b.TLS.Email &&
		slices.Equal(a.TLS.Domains, b.TLS.Domains)
}

func cloneConfig(source config.Config) config.Config {
	return config.Clone(source)
}

func cloneRoute(route config.Route) config.Route {
	clone := route
	if route.Access != nil {
		access := *route.Access
		access.AllowedEmails = slices.Clone(route.Access.AllowedEmails)
		access.AllowedDomains = slices.Clone(route.Access.AllowedDomains)
		access.RequiredGroups = slices.Clone(route.Access.RequiredGroups)
		clone.Access = &access
	}
	return clone
}
