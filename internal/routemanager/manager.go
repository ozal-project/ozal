// Package routemanager builds and atomically swaps complete routing snapshots.
package routemanager

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/healthcheck"
	"github.com/octaviosantacruz/switchboard/internal/proxy"
)

// Snapshot is immutable after construction. Its fields stay private so a
// request can only observe the complete handler/config pair that was swapped
// into place.
type Snapshot struct {
	cfg     config.Config
	handler http.Handler
	cancel  context.CancelFunc
}

// Manager owns the current routing snapshot. Reloads are serialized while
// requests only pay for one atomic load.
type Manager struct {
	ctx                 context.Context
	healthCheckInterval time.Duration
	reloadMu            sync.Mutex
	current             atomic.Pointer[Snapshot]
}

// New builds and installs the initial snapshot from configPath.
func New(ctx context.Context, configPath string, healthCheckInterval time.Duration) (*Manager, error) {
	if healthCheckInterval <= 0 {
		return nil, fmt.Errorf("health check interval must be positive")
	}
	manager := &Manager{
		ctx:                 ctx,
		healthCheckInterval: healthCheckInterval,
	}
	if err := manager.Reload(configPath); err != nil {
		return nil, err
	}
	return manager, nil
}

// Reload builds a complete candidate before atomically installing it. A
// failed candidate never changes the snapshot serving requests.
func (m *Manager) Reload(configPath string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if err := m.ctx.Err(); err != nil {
		return fmt.Errorf("route manager context is closed: %w", err)
	}

	candidate, err := m.build(configPath)
	if err != nil {
		if m.current.Load() != nil {
			log.Printf("route reload rejected; keeping last-good snapshot: %v", err)
		}
		return err
	}

	previous := m.current.Swap(candidate)
	if previous != nil {
		previous.cancel()
	}
	return nil
}

// Config returns a deep copy of the active configuration.
func (m *Manager) Config() config.Config {
	snapshot := m.current.Load()
	if snapshot == nil {
		return config.Config{}
	}
	return cloneConfig(snapshot.cfg)
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

func (m *Manager) build(configPath string) (_ *Snapshot, err error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if current := m.current.Load(); current != nil && !sameServerConfig(current.cfg, *cfg) {
		return nil, fmt.Errorf("listen and TLS settings cannot change without restarting Switchboard")
	}

	snapshotCtx, cancel := context.WithCancel(m.ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	var gate proxy.AuthGate
	var oidcGate *auth.Gate
	if hasProtectedRoute(cfg.Routes) {
		hosts := make([]string, len(cfg.Routes))
		for i, route := range cfg.Routes {
			hosts[i] = route.Host
		}
		oidcGate, err = auth.NewGate(snapshotCtx, cfg.Auth, hosts)
		if err != nil {
			return nil, fmt.Errorf("build auth gate: %w", err)
		}
		gate = oidcGate
	}

	checker := healthcheck.New()
	proxyHandler, err := proxy.New(cfg.Routes, checker, gate)
	if err != nil {
		return nil, fmt.Errorf("build proxy: %w", err)
	}
	handler := withAuthEndpoints(proxyHandler, oidcGate)
	checker.Start(snapshotCtx, cfg.Routes, m.healthCheckInterval)

	return &Snapshot{
		cfg:     cloneConfig(*cfg),
		handler: handler,
		cancel:  cancel,
	}, nil
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

func hasProtectedRoute(routes []config.Route) bool {
	for _, route := range routes {
		if route.RequiresAuth() {
			return true
		}
	}
	return false
}

func sameServerConfig(a, b config.Config) bool {
	return a.Listen == b.Listen &&
		a.TLS.Enabled == b.TLS.Enabled &&
		a.TLS.CacheDir == b.TLS.CacheDir &&
		a.TLS.Email == b.TLS.Email &&
		slices.Equal(a.TLS.Domains, b.TLS.Domains)
}

func cloneConfig(source config.Config) config.Config {
	clone := source
	clone.TLS.Domains = slices.Clone(source.TLS.Domains)
	clone.Routes = make([]config.Route, len(source.Routes))
	for i, route := range source.Routes {
		clone.Routes[i] = route
		if route.Access != nil {
			access := *route.Access
			access.AllowedEmails = slices.Clone(route.Access.AllowedEmails)
			access.AllowedDomains = slices.Clone(route.Access.AllowedDomains)
			access.RequiredGroups = slices.Clone(route.Access.RequiredGroups)
			clone.Routes[i].Access = &access
		}
	}
	return clone
}
