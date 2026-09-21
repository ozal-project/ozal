// Package routemanager builds and atomically swaps complete routing snapshots.
package routemanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/healthcheck"
	"github.com/octaviosantacruz/switchboard/internal/proxy"
)

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
	cfg          config.Config
	handler      http.Handler
	checker      *healthcheck.Checker
	healthCancel context.CancelFunc
	gate         *auth.Gate
	gateCancel   context.CancelFunc
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
}

// New builds and installs the initial snapshot from configPath.
func New(ctx context.Context, configPath string, healthCheckInterval time.Duration) (*Manager, error) {
	if healthCheckInterval <= 0 {
		return nil, fmt.Errorf("health check interval must be positive")
	}
	manager := &Manager{
		ctx:                 ctx,
		configPath:          configPath,
		healthCheckInterval: healthCheckInterval,
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
	defer func() {
		if err != nil {
			healthCancel()
			if gateCancel != nil {
				gateCancel()
			}
		}
	}()

	var gate proxy.AuthGate
	var oidcGate *auth.Gate
	protectedHosts := protectedRouteHosts(cfg.Routes)
	if len(protectedHosts) > 0 {
		if m.gate != nil && m.gateAuth == cfg.Auth && slices.Equal(m.gateHosts, protectedHosts) {
			oidcGate = m.gate
		} else {
			gateCtx, cancelGate := context.WithCancel(m.ctx)
			oidcGate, err = auth.NewGate(gateCtx, cfg.Auth, protectedHosts)
			if err != nil {
				cancelGate()
				return nil, fmt.Errorf("build auth gate: %w", err)
			}
			gateCancel = cancelGate
		}
		gate = oidcGate
	}

	checker := healthcheck.New()
	proxyHandler, err := proxy.New(cfg.Routes, checker, gate)
	if err != nil {
		return nil, fmt.Errorf("build proxy: %w", err)
	}
	handler := withAuthEndpoints(proxyHandler, oidcGate)
	checker.Start(healthCtx, cfg.Routes, m.healthCheckInterval)
	if err := m.contextError(); err != nil {
		return nil, err
	}

	return &Snapshot{
		cfg:          cloneConfig(*cfg),
		handler:      handler,
		checker:      checker,
		healthCancel: healthCancel,
		gate:         oidcGate,
		gateCancel:   gateCancel,
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
}

func (m *Manager) discard(candidate *Snapshot) {
	candidate.healthCancel()
	if candidate.gateCancel != nil {
		candidate.gateCancel()
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

func withAuthEndpoints(next http.Handler, gate *auth.Gate) http.Handler {
	if gate == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_/auth/login":
			gate.HandleLogin(w, r)
		case "/_/auth/callback":
			gate.HandleCallback(w, r)
		case "/_/auth/logout":
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
