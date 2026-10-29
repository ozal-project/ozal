// Package routemanager builds and atomically swaps complete routing snapshots.
package routemanager

import (
	"context"
	"encoding/json"
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

	"github.com/ozal-project/ozal/internal/auth"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/healthcheck"
	"github.com/ozal-project/ozal/internal/passkey"
	"github.com/ozal-project/ozal/internal/pipeline"
	"github.com/ozal-project/ozal/internal/proxy"
	"github.com/ozal-project/ozal/internal/ratelimit"
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

// WithAuditAppender installs the process-owned durable writer for access
// decisions. Its directory is fixed for this manager's lifetime.
func WithAuditAppender(appender events.AuditAppender) Option {
	return func(m *Manager) { m.audit = appender }
}

// WithSessionBackend supplies the session storage factory. The default is
// the in-memory store; the composition root passes the SQLite adapter to
// make sessions survive a restart. The factory is called again when the
// session lifetime changes, because a lifetime change is a policy change.
func WithSessionBackend(newBackend func(lifetime auth.Lifetime) auth.SessionBackend) Option {
	return func(m *Manager) {
		if newBackend != nil {
			m.newSessions = newBackend
		}
	}
}

var (
	ErrRouteExists   = errors.New("route already exists")
	ErrRouteNotFound = errors.New("route not found")
	ErrPersistence   = errors.New("config persistence failed")
)

// ConnectorRoutes is the process-owned tunnel adapter. Route construction
// validates enrolment; live attachment is projected separately from HTTP health.
type ConnectorRoutes interface {
	TransportFor(ctx context.Context, name string) (http.RoundTripper, error)
	ConnectorConnected(name string) bool
}

func WithConnectorRoutes(routes ConnectorRoutes) Option {
	return func(m *Manager) { m.connectorRoutes = routes }
}

// WithPasskeyDirectory supplies the user and passkey store that
// auth.passkeys needs (ID-003). Without it a config with passkeys fails.
func WithPasskeyDirectory(dir passkey.Directory) Option {
	return func(m *Manager) { m.passkeys = dir }
}

// WithSites serves "site" routes from the deployment store (PAGES-002).
func WithSites(sites proxy.SiteDirs) Option {
	return func(m *Manager) { m.sites = sites }
}

// RouteStatus combines the immutable route configuration with its latest
// health observation.
type RouteStatus struct {
	Route              config.Route
	Health             healthcheck.Status
	Upstreams          []healthcheck.TargetStatus
	ConnectorConnected bool
}

// Snapshot is immutable after construction except for the checker state. Its
// fields stay private so a request observes one complete routing generation.
type Snapshot struct {
	cfg config.Config
	// digest is config.Digest of the root and operational sources for this generation.
	digest        string
	handler       http.Handler
	checker       *healthcheck.Checker
	healthCancel  context.CancelFunc
	gate          loginGate
	gateCancel    context.CancelFunc
	sessions      auth.SessionBackend
	sessionCancel context.CancelFunc
	// transport carries every upstream request of this generation. It is the
	// previous generation's when the timeout is unchanged, so a reload keeps
	// reusing warm connections.
	transport        *http.Transport
	transportTimeout time.Duration
	// rateLimiters are the previous generation's when their settings are
	// unchanged: rebuilding the buckets would hand every client a fresh
	// burst on every reload.
	rateLimiters *rateLimiters
}

// Manager owns the current routing snapshot. Reloads and mutations are
// serialized while requests only pay for one atomic load.
type Manager struct {
	ctx                 context.Context
	configPath          string
	healthCheckInterval time.Duration
	reloadMu            sync.Mutex
	current             atomic.Pointer[Snapshot]
	gate                loginGate
	gateCancel          context.CancelFunc
	gateAuth            config.Auth
	gateHosts           []string
	sessions            auth.SessionBackend
	sessionLifetime     auth.Lifetime
	sessionCancel       context.CancelFunc
	newSessions         func(lifetime auth.Lifetime) auth.SessionBackend
	transport           *http.Transport
	transportTimeout    time.Duration
	rateLimiters        *rateLimiters
	sink                events.Sink
	audit               events.AuditAppender
	connectorRoutes     ConnectorRoutes
	sites               proxy.SiteDirs
	passkeys            passkey.Directory
}

// loginGate is what a protected snapshot needs from a login method: the
// proxy's AuthGate plus the /_/auth/ endpoints. The OIDC gate and the
// passkey gate both provide it.
type loginGate interface {
	proxy.AuthGate
	ServeAuthEndpoint(w http.ResponseWriter, r *http.Request) bool
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
		newSessions:         func(lifetime auth.Lifetime) auth.SessionBackend { return auth.NewSessionStore(lifetime) },
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
	cfg, digest, err := config.LoadWithDigest(m.configPath)
	if err != nil {
		return m.reloadError(err)
	}
	candidate, err := m.build(*cfg)
	if err != nil {
		return m.reloadError(err)
	}
	candidate.digest = digest
	m.install(candidate)
	return nil
}

// ConfigDigest is config.Digest of the root and operational sources behind the
// installed generation; after a failed reload it still names the prior sources.
func (m *Manager) ConfigDigest() string {
	if snapshot := m.current.Load(); snapshot != nil {
		return snapshot.digest
	}
	return ""
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

	cfg := config.Clone(current.cfg)
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

	cfg := config.Clone(current.cfg)
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
	return config.Clone(snapshot.cfg)
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
			Route:     config.CloneRoute(route),
			Health:    snapshot.checker.Status(route.Host),
			Upstreams: snapshot.checker.UpstreamStatuses(route.Host),
		}
		if route.Connector != "" && m.connectorRoutes != nil {
			statuses[i].ConnectorConnected = m.connectorRoutes.ConnectorConnected(route.Connector)
		}
	}
	return statuses
}

// protectedRoutes are the routes behind a login in the installed snapshot,
// read when asked: the passkey gate outlives a reload that changes only
// policies, and the portal must list what is allowed now.
func (m *Manager) protectedRoutes() []config.Route {
	current := m.current.Load()
	if current == nil {
		return nil
	}
	var routes []config.Route
	for _, route := range current.cfg.Routes {
		if route.RequiresAuth() {
			routes = append(routes, route)
		}
	}
	return routes
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
	// Editors outside this process do not hold reloadMu. Refuse a stale
	// root/operational source before writing anything on their behalf.
	_, sourceDigest, sourceErr := config.LoadWithDigest(m.configPath)
	current := m.current.Load()
	if sourceErr != nil || current == nil || sourceDigest != current.digest {
		return ErrConfigChanged
	}
	candidate, err := m.build(cfg)
	if err != nil {
		return err
	}
	if err := config.PersistOperating(m.configPath, candidate.cfg); err != nil {
		m.discard(candidate)
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	// Re-read both sources: an external root edit may have raced the write.
	diskConfig, digest, err := config.LoadWithDigest(m.configPath)
	if err != nil {
		m.discard(candidate)
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	expectedJSON, expectedErr := json.Marshal(candidate.cfg)
	installedJSON, installedErr := json.Marshal(diskConfig)
	if expectedErr != nil || installedErr != nil || string(expectedJSON) != string(installedJSON) {
		m.discard(candidate)
		return ErrConfigChanged
	}
	candidate.digest = digest
	m.install(candidate)
	return nil
}

// rateLimiters groups the limiter instances the request pipeline consumes.
// The identity limiter is nil when the configuration does not define an
// identity budget.
type rateLimiters struct {
	general  pipeline.Allower
	auth     pipeline.Allower
	identity pipeline.Allower
	config   config.RateLimit
}

// limitersFor returns the installed limiters while their settings are
// unchanged and builds a fresh set otherwise. Buckets survive every reload
// that does not touch rate_limit, so a client cannot reset its budget by
// forcing a reload.
func (m *Manager) limitersFor(settings *config.RateLimit) *rateLimiters {
	if settings == nil {
		return nil
	}
	if installed := m.rateLimiters; installed != nil && sameRateLimitSettings(installed.config, *settings) {
		return installed
	}
	const idle = 10 * time.Minute
	maxKeys := settings.MaxKeys
	if maxKeys <= 0 {
		maxKeys = ratelimit.DefaultMaxKeys
	}
	limiters := &rateLimiters{
		general: ratelimit.NewLimiter(settings.General.Rate, settings.General.Burst, idle, ratelimit.WithMaxKeys(maxKeys)),
		auth:    ratelimit.NewLimiter(settings.Auth.Rate, settings.Auth.Burst, idle, ratelimit.WithMaxKeys(maxKeys)),
		config:  cloneRateLimit(*settings),
	}
	if settings.Identity != nil {
		limiters.identity = ratelimit.NewLimiter(settings.Identity.Rate, settings.Identity.Burst, idle, ratelimit.WithMaxKeys(maxKeys))
	}
	return limiters
}

func sameRateLimitSettings(a, b config.RateLimit) bool {
	if a.General != b.General || a.Auth != b.Auth || a.MaxKeys != b.MaxKeys {
		return false
	}
	switch {
	case a.Identity == nil && b.Identity == nil:
		return true
	case a.Identity == nil || b.Identity == nil:
		return false
	default:
		return *a.Identity == *b.Identity
	}
}

func cloneRateLimit(source config.RateLimit) config.RateLimit {
	clone := source
	if source.Identity != nil {
		identity := *source.Identity
		clone.Identity = &identity
	}
	return clone
}

func (m *Manager) build(raw config.Config) (_ *Snapshot, err error) {
	cfg, err := config.Validate(raw)
	if err != nil {
		return nil, err
	}
	if current := m.current.Load(); current != nil && !config.SameServer(current.cfg, *cfg) {
		return nil, fmt.Errorf("listen, admin listen, TLS, tunnel, and notification settings cannot change without restarting Ozal")
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
	var builtGate loginGate
	var sessions auth.SessionBackend
	protectedHosts := protectedRouteHosts(cfg.Routes)
	if len(protectedHosts) > 0 {
		// The session store outlives gate rebuilds: adding a protected route
		// must not log everyone out. A changed session lifetime is a policy change,
		// so it gets a fresh store; the candidate is promoted only on install.
		sessions = m.sessions
		if sessions == nil || m.sessionLifetime != auth.SessionLifetime(cfg.Auth) {
			storeCtx, cancelStore := context.WithCancel(m.ctx)
			sessions = m.newSessions(auth.SessionLifetime(cfg.Auth))
			go auth.SweepLoop(storeCtx, sessions, auth.SessionCleanupInterval)
			sessionCancel = cancelStore
		}
		if m.gate != nil && sameAuth(m.gateAuth, cfg.Auth) && slices.Equal(m.gateHosts, protectedHosts) {
			builtGate = m.gate
		} else if cfg.Auth.Passkeys != nil {
			if m.passkeys == nil {
				return nil, fmt.Errorf("auth.passkeys requires the state database: start Ozal with -db")
			}
			passkeyGate, err := passkey.NewGate(cfg.Auth, protectedHosts, m.passkeys, sessions, m.sink)
			if err != nil {
				return nil, fmt.Errorf("build passkey gate: %w", err)
			}
			if cfg.Auth.Issuer != "" {
				// ID-007: the upstream provider is offered on the passkey
				// login page; both share the session store and cookie.
				oidcGate, cancelGate, err := m.buildOIDCGate(cfg.Auth, protectedHosts, sessions)
				if err != nil {
					return nil, err
				}
				gateCancel = cancelGate
				passkeyGate.WithOIDC(oidcGate, cfg.Auth.ProviderName)
			}
			passkeyGate.WithApps(m.protectedRoutes)
			builtGate = passkeyGate
		} else {
			oidcGate, cancelGate, err := m.buildOIDCGate(cfg.Auth, protectedHosts, sessions)
			if err != nil {
				return nil, err
			}
			gateCancel = cancelGate
			builtGate = oidcGate
		}
		gate = builtGate
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
	connectorTransports := make(map[string]http.RoundTripper)
	for _, route := range cfg.Routes {
		if route.Connector == "" || connectorTransports[route.Connector] != nil {
			continue
		}
		if m.connectorRoutes == nil {
			return nil, fmt.Errorf("connector route %s requires an active tunnel adapter", route.Host)
		}
		outbound, err := m.connectorRoutes.TransportFor(m.ctx, route.Connector)
		if err != nil {
			return nil, fmt.Errorf("connector route %s: %w", route.Host, err)
		}
		if outbound == nil {
			return nil, fmt.Errorf("connector route %s: missing transport", route.Host)
		}
		connectorTransports[route.Connector] = outbound
	}
	proxyOpts = append(proxyOpts, proxy.WithConnectorTransports(connectorTransports))
	if m.sites != nil {
		proxyOpts = append(proxyOpts, proxy.WithSites(m.sites))
	}
	proxyOpts = append(proxyOpts, proxy.WithTrustedProxies(trustedProxies), proxy.WithEventSink(m.sink), proxy.WithAuditAppender(m.audit), proxy.WithTransport(transport))
	if cfg.DevErrors {
		proxyOpts = append(proxyOpts, proxy.WithDevErrors())
	}
	limiters := m.limitersFor(cfg.RateLimit)
	if limiters != nil && limiters.identity != nil {
		// The identity dimension runs inside the proxy, after the policy
		// allowed the request: only there is the principal known.
		proxyOpts = append(proxyOpts, proxy.WithIdentityLimiter(limiters.identity))
	}
	proxyHandler, err := proxy.New(cfg.Routes, checker, gate, proxyOpts...)
	if err != nil {
		return nil, fmt.Errorf("build proxy: %w", err)
	}
	handler := withAuthEndpoints(proxyHandler, builtGate, authEndpointHosts(*cfg, protectedHosts))
	handler = withLoginRoot(handler, *cfg)
	if limiters != nil {
		// Outside withAuthEndpoints so /_/auth/* is limited too. Trust runs
		// first for the client IP; the copy inside the proxy is a no-op.
		handler = pipeline.Chain(
			pipeline.TrustWithProxies(trustedProxies),
			pipeline.RateLimit(limiters.general, limiters.auth, "/_/auth/",
				pipeline.WithRateLimitSink(m.sink), pipeline.WithRouteCatalog(cfg.Routes), pipeline.WithRateLimitAudit(m.audit)),
		)(handler)
	}
	checker.Start(healthCtx, cfg.Routes, m.healthCheckInterval)
	if err := m.contextError(); err != nil {
		return nil, err
	}

	return &Snapshot{
		cfg:           config.Clone(*cfg),
		handler:       handler,
		checker:       checker,
		healthCancel:  healthCancel,
		gate:          builtGate,
		gateCancel:    gateCancel,
		sessions:      sessions,
		sessionCancel: sessionCancel,

		transport:        transport,
		transportTimeout: transportTimeout,
		rateLimiters:     limiters,
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
		m.sessionLifetime = auth.SessionLifetime(candidate.cfg.Auth)
		m.sessionCancel = candidate.sessionCancel
		candidate.sessionCancel = nil
		if oldSessionCancel != nil {
			oldSessionCancel()
		}
	}
	// Limiters carry no goroutine to stop, but they do carry accumulated
	// state: keep the installed ones across reloads and drop them only when
	// rate limiting is disabled or reconfigured.
	m.rateLimiters = candidate.rateLimiters
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

// authEndpointHosts returns the hosts where /_/auth/* belongs to Ozal:
// the protected routes, plus the redirect_url host where the OIDC callback
// lands. On any other host the path is the upstream's, not ours.
func authEndpointHosts(cfg config.Config, protectedHosts []string) map[string]bool {
	hosts := make(map[string]bool, len(protectedHosts)+1)
	for _, host := range protectedHosts {
		hosts[host] = true
	}
	loginURL := cfg.Auth.RedirectURL
	if cfg.Auth.Passkeys != nil {
		loginURL = cfg.Auth.Passkeys.LoginURL
	}
	if login, err := url.Parse(loginURL); err == nil && login.Host != "" {
		if host, err := config.NormalizeHost(login.Host); err == nil {
			hosts[host] = true
		}
	}
	return hosts
}

// sameAuth compares auth settings by value: Passkeys is a pointer, and a
// reload always produces a new one.
func sameAuth(a, b config.Auth) bool {
	pa, pb := a.Passkeys, b.Passkeys
	a.Passkeys, b.Passkeys = nil, nil
	if a != b || (pa == nil) != (pb == nil) {
		return false
	}
	return pa == nil || *pa == *pb
}

func withAuthEndpoints(next http.Handler, gate loginGate, hosts map[string]bool) http.Handler {
	if gate == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := config.NormalizeHost(r.Host)
		if err != nil || !hosts[host] || !gate.ServeAuthEndpoint(w, r) {
			next.ServeHTTP(w, r)
		}
	})
}

// withLoginRoot sends the root of a dedicated passkey login host to the
// login page: someone who types auth.example.com gets a sign-in page, not
// "no route for host". When a route serves the login host (local
// development), the route keeps its root.
func withLoginRoot(next http.Handler, cfg config.Config) http.Handler {
	if cfg.Auth.Passkeys == nil {
		return next
	}
	login, err := url.Parse(cfg.Auth.Passkeys.LoginURL)
	if err != nil {
		return next
	}
	loginHost, err := config.NormalizeHost(login.Host)
	if err != nil {
		return next
	}
	for _, r := range cfg.Routes {
		if host, err := config.NormalizeHost(r.Host); err == nil && (host == loginHost || host == login.Hostname()) {
			return next
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, err := config.NormalizeHost(r.Host); err == nil && host == loginHost && r.URL.Path == "/" {
			http.Redirect(w, r, "/_/auth/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
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

// buildOIDCGate builds the upstream provider's login. The secret is resolved
// only for the consumer; the config keeps the raw reference so control-plane
// writes persist it unchanged.
func (m *Manager) buildOIDCGate(cfg config.Auth, protectedHosts []string, sessions auth.SessionBackend) (*auth.Gate, context.CancelFunc, error) {
	resolved, err := config.ResolveSecret(cfg.ClientSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve auth.client_secret: %w", err)
	}
	cfg.ClientSecret = resolved
	gateCtx, cancel := context.WithCancel(m.ctx)
	gate, err := auth.NewGate(gateCtx, cfg, protectedHosts, sessions, m.sink)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("build auth gate: %w", err)
	}
	return gate, cancel, nil
}
