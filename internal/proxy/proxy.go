// Package proxy builds an http.Handler that reverse-proxies by Host header.
package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/pages"
	"github.com/octaviosantacruz/switchboard/internal/pipeline"
	"github.com/octaviosantacruz/switchboard/internal/routing"
	"github.com/octaviosantacruz/switchboard/internal/static"
)

// Option configures the proxy handler.
type Option func(*options)

type options struct {
	transport             http.RoundTripper
	connectorTransports   map[string]http.RoundTripper
	responseHeaderTimeout time.Duration
	logger                *slog.Logger
	devErrors             bool
	sink                  events.Sink
	audit                 events.AuditAppender
	trustedProxies        []netip.Prefix
	assertVisitor         bool
	tunnelTrust           bool
	identityLimiter       pipeline.Allower
	sites                 SiteDirs
}

// SiteDirs resolves a deployed site to what it currently serves: directory
// and rules (pages.Store implements it). Declared here, by its consumer.
type SiteDirs interface {
	Live(site string) (pages.Live, bool)
}

// WithSites enables "site" routes. Without it they fail construction.
func WithSites(sites SiteDirs) Option {
	return func(o *options) { o.sites = sites }
}

// WithIdentityLimiter adds the identity dimension of the ratelimit stage:
// after the access policy allows a request, one bucket per authenticated
// principal is charged. It is independent from the per-IP stage because the
// identity only exists after authentication.
func WithIdentityLimiter(limiter pipeline.Allower) Option {
	return func(o *options) { o.identityLimiter = limiter }
}

// WithVisitorAssertion makes the proxy tell its upstream who the visitor is
// through pipeline.VisitorIPHeader. Use it only when the upstream is a tunnel
// connector that runs WithAuthenticatedTunnelTrust: to any other upstream the
// header is meaningless, and applications get X-Forwarded-For.
func WithVisitorAssertion() Option {
	return func(o *options) { o.assertVisitor = true }
}

// WithAuthenticatedTunnelTrust is for a handler reachable only through the
// mutually authenticated tunnel (the connector side): the visitor address is
// taken from the edge's assertion instead of CF-Connecting-IP. It replaces
// WithTrustedProxies.
func WithAuthenticatedTunnelTrust() Option {
	return func(o *options) { o.tunnelTrust = true }
}

// WithResponseHeaderTimeout sets how long an upstream may take to start
// answering. It only applies to the built-in transport: a transport supplied
// with WithTransport keeps its own limits.
func WithResponseHeaderTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.responseHeaderTimeout = d
		}
	}
}

// WithLogger sets where the proxy writes its operational log (forwarded
// requests, upstream failures). The default is slog.Default() at New time.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// WithTransport supplies an outbound transport (for example, a tunnel).
// The trust and authorization stages still run before forwarding.
func WithTransport(transport http.RoundTripper) Option {
	return func(o *options) { o.transport = transport }
}

// WithConnectorTransports supplies only explicitly configured tunnel destinations.
// A missing connector transport fails construction; there is no TCP fallback.
func WithConnectorTransports(transports map[string]http.RoundTripper) Option {
	return func(o *options) { o.connectorTransports = transports }
}

// WithTrustedProxies allows only these TCP peers to supply CF-Connecting-IP.
// Empty is the default: never trust forwarding headers from a direct client.
func WithTrustedProxies(prefixes []netip.Prefix) Option {
	trusted := slices.Clone(prefixes)
	return func(o *options) { o.trustedProxies = trusted }
}

// WithDevErrors enables local-development error pages: the upstream address
// and a hint on 502, the known route hosts on 404. Never enable it on a
// public edge — it reveals internal addresses and hostnames.
func WithDevErrors() Option {
	return func(o *options) { o.devErrors = true }
}

// WithEventSink publishes access decisions to sink (events.Discard by default).
func WithEventSink(sink events.Sink) Option {
	return func(o *options) {
		if sink != nil {
			o.sink = sink
		}
	}
}

// WithAuditAppender persists access decisions before the request is denied,
// redirected for step-up, or forwarded. It is independent from the live UI
// event sink, which remains available when durable storage fails.
func WithAuditAppender(appender events.AuditAppender) Option {
	return func(o *options) { o.audit = appender }
}

type route struct {
	target        string
	handler       http.Handler // static routes have one fixed handler
	upstreams     []routeUpstream
	selector      *routeSelector
	authRequired  bool
	access        config.AccessPolicy
	resource      *config.RouteResource
	auditRequired bool
}

type routeUpstream struct {
	target  string
	handler http.Handler
}

type routeSelector struct {
	mu   sync.Mutex
	next int
}

func (s *routeSelector) selectIndex(upstreams []routeUpstream, checker HealthChecker, host string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := routing.Select(s.next, len(upstreams), func(index int) bool {
		return upstreamHealthy(checker, host, upstreams[index].target)
	})
	if ok {
		s.next = (index + 1) % len(upstreams)
	}
	return index, ok
}

func upstreamHealthy(checker HealthChecker, host, target string) bool {
	if checker == nil {
		return true
	}
	if targetChecker, ok := checker.(interface {
		HealthyTarget(host, target string) bool
	}); ok {
		return targetChecker.HealthyTarget(host, target)
	}
	return checker.Healthy(host)
}

type passiveFailureReporter interface {
	ReportFailure(host, target string)
}

func cloneRouteResource(resource *config.RouteResource) *config.RouteResource {
	if resource == nil {
		return nil
	}
	copy := *resource
	copy.RiskyActions = slices.Clone(resource.RiskyActions)
	return &copy
}

// HealthChecker reports whether the upstream for a route's host is
// currently healthy. A nil HealthChecker is treated as "always healthy" —
// callers that don't run health checks can pass nil.
type HealthChecker interface {
	Healthy(host string) bool
}

// AuthGate authenticates requests to protected routes and starts the login
// flow when no valid session exists. A nil AuthGate is fine as long as no
// route requires authentication; config validation already enforces that.
type AuthGate interface {
	Authenticate(r *http.Request) (auth.Principal, bool)
	RedirectToLogin(w http.ResponseWriter, r *http.Request)
}

// StepUpGate is implemented by a gate whose login page can say why an
// already signed-in visitor must log in again (code is auth.Fresh's; methods
// are the route's accepted login methods, possibly none).
type StepUpGate interface {
	RedirectToStepUp(w http.ResponseWriter, r *http.Request, code auth.DecisionCode, methods []string)
}

// DeniedGate is implemented by a gate that can answer a signed-in person whom
// policy refused with a page that explains and offers a way forward, instead
// of a bare "access denied" (CTX-007). It reports whether it wrote the
// response. Only a browser navigation gets the page: an API client keeps the
// plain 403 it can parse.
type DeniedGate interface {
	ServeDenied(w http.ResponseWriter, r *http.Request, p auth.Principal, code auth.DecisionCode) bool
}

// wantsPage reports whether the request is a browser asking for a document.
func wantsPage(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// New builds a host-routed reverse proxy handler from the given routes.
// It fails fast on invalid or duplicate hosts and targets. checker and gate may be nil.
func New(routes []config.Route, checker HealthChecker, gate AuthGate, opts ...Option) (http.Handler, error) {
	cfg := options{sink: events.Discard, responseHeaderTimeout: config.DefaultUpstreamTimeout, logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.transport == nil {
		// One transport for every route: connections to the same upstream are
		// reused, and the limits are defined in one place.
		cfg.transport = NewUpstreamTransport(cfg.responseHeaderTimeout)
	}
	byHost := make(map[string]route, len(routes))
	responseBuffers := newResponseBufferPool()

	for _, r := range routes {
		host, err := config.NormalizeHost(r.Host)
		if err != nil {
			return nil, fmt.Errorf("route %q has invalid host: %w", r.Host, err)
		}
		if _, exists := byHost[host]; exists {
			return nil, fmt.Errorf("duplicate route host %q", host)
		}
		transport, assertVisitor := cfg.transport, cfg.assertVisitor
		destination := r
		targets := r.UpstreamTargets()
		if r.Connector != "" {
			if r.Target != "" || len(r.Upstreams) > 0 || r.IsStatic() {
				return nil, fmt.Errorf("route %s: unsupported connector destination", r.Host)
			}
			transport = cfg.connectorTransports[r.Connector]
			if transport == nil {
				return nil, fmt.Errorf("route %s: connector transport unavailable", r.Host)
			}
			destination.Target = "http://tunnel.invalid"
			targets = []string{destination.Target}
			assertVisitor = true
		}
		var handler http.Handler
		var routeUpstreams []routeUpstream
		if r.IsStatic() {
			handler, err = upstreamHandler(r, cfg.devErrors, assertVisitor, transport, cfg.sites, cfg.logger, responseBuffers, nil)
			if err != nil {
				return nil, err
			}
		} else {
			if len(targets) == 0 {
				return nil, fmt.Errorf("route %s: HTTP route requires a target or at least two upstreams", r.Host)
			}
			routeUpstreams = make([]routeUpstream, 0, len(targets))
			for _, target := range targets {
				upstreamRoute := destination
				upstreamRoute.Target = target
				upstreamRoute.Upstreams = nil
				var reportFailure func()
				if reporter, ok := checker.(passiveFailureReporter); ok && r.Connector == "" {
					reportFailure = func() { reporter.ReportFailure(host, target) }
				}
				upstreamHandler, err := upstreamHandler(upstreamRoute, cfg.devErrors, assertVisitor, transport, cfg.sites, cfg.logger, responseBuffers, reportFailure)
				if err != nil {
					return nil, err
				}
				routeUpstreams = append(routeUpstreams, routeUpstream{target: target, handler: upstreamHandler})
			}
		}
		byHost[host] = route{
			target:        r.Destination(),
			handler:       handler,
			upstreams:     routeUpstreams,
			selector:      &routeSelector{},
			authRequired:  r.RequiresAuth(),
			access:        r.Policy(),
			resource:      cloneRouteResource(r.Resource),
			auditRequired: r.AuditRequired(),
		}
	}

	forward := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host, err := config.NormalizeHost(req.Host)
		if err != nil {
			http.Error(w, "invalid request host", http.StatusBadRequest)
			return
		}
		rt, ok := byHost[host]
		if !ok {
			// EDGE-003: a route declared without a port answers on any
			// listener port; an exact host:port route always wins.
			if bare, found := withoutPort(host); found {
				if rt, ok = byHost[bare]; ok {
					host = bare
				}
			}
		}
		if !ok {
			if cfg.devErrors {
				writeDevNotFound(w, req.Host, byHost)
				return
			}
			http.Error(w, "no route for host: "+req.Host, http.StatusNotFound)
			return
		}
		if checker != nil && !checker.Healthy(host) {
			http.Error(w, "upstream unhealthy: "+host, http.StatusServiceUnavailable)
			return
		}
		selectedIndex := -1
		if rt.authRequired {
			if gate == nil {
				http.Error(w, "auth misconfigured for this route", http.StatusInternalServerError)
				return
			}
			principal, ok := gate.Authenticate(req)
			if !ok {
				gate.RedirectToLogin(w, req)
				return
			}
			observedAt := time.Now().UTC()
			decisionContext := pipeline.RequestPolicyContext(req, host, &principal, observedAt, rt.resource)
			decision := auth.AuthorizeWithContext(principal, decisionContext, rt.access)
			if decision.Code == auth.CodeContextStepUpRequired {
				event := events.Event{
					Time:       observedAt,
					Type:       events.AccessStepUp,
					Actor:      principal.Subject,
					Host:       host,
					Code:       string(decision.Code),
					Reason:     decision.Reason,
					RemoteAddr: req.RemoteAddr,
					Context:    &decisionContext,
				}
				if !emitAccessEvent(w, req, cfg, event, rt.auditRequired) {
					return
				}
				if stepUp, ok := gate.(StepUpGate); ok {
					stepUp.RedirectToStepUp(w, req, decision.Code, []string{auth.MethodPasskey})
				} else {
					gate.RedirectToLogin(w, req)
				}
				return
			}
			if !decision.Allowed {
				event := events.Event{
					Time:       observedAt,
					Type:       events.AccessDenied,
					Actor:      principal.Subject,
					Host:       host,
					Code:       string(decision.Code),
					Reason:     decision.Reason,
					RemoteAddr: req.RemoteAddr,
					Context:    &decisionContext,
				}
				if !emitAccessEvent(w, req, cfg, event, rt.auditRequired) {
					return
				}
				if page, ok := gate.(DeniedGate); ok && wantsPage(req) && page.ServeDenied(w, req, principal, decision.Code) {
					return
				}
				http.Error(w, "access denied", http.StatusForbidden)
				return
			}
			// Step-up: allowed, but the login is too old or used a synced
			// passkey on a route that needs a device-bound one. The new login
			// replaces the session, which rotates its ID.
			if fresh := auth.Fresh(principal, rt.access, time.Now()); !fresh.Allowed {
				event := events.Event{
					Time:       observedAt,
					Type:       events.AccessStepUp,
					Actor:      principal.Subject,
					Host:       host,
					Code:       string(fresh.Code),
					Reason:     fresh.Reason,
					RemoteAddr: req.RemoteAddr,
					Context:    &decisionContext,
				}
				if !emitAccessEvent(w, req, cfg, event, rt.auditRequired) {
					return
				}
				if stepUp, ok := gate.(StepUpGate); ok {
					stepUp.RedirectToStepUp(w, req, fresh.Code, auth.RequiredMethods(rt.access))
				} else {
					gate.RedirectToLogin(w, req)
				}
				return
			}
			// The identity dimension of the ratelimit stage: it runs here
			// because only now is the identity known to be authentic. A
			// refused request never counts as allowed.
			if cfg.identityLimiter != nil {
				if allowed, wait := cfg.identityLimiter.Allow("identity|" + principal.Subject); !allowed {
					event := events.Event{
						Time:       observedAt,
						Type:       events.AccessRateLimited,
						Actor:      principal.Subject,
						Host:       host,
						Code:       "identity",
						Reason:     "rate limit reached",
						RemoteAddr: req.RemoteAddr,
						Context:    &decisionContext,
					}
					if !emitAccessEvent(w, req, cfg, event, rt.auditRequired) {
						return
					}
					pipeline.WriteRetryAfter(w, wait)
					http.Error(w, "too many requests", http.StatusTooManyRequests)
					return
				}
			}
			if len(rt.upstreams) > 0 {
				index, available := rt.selector.selectIndex(rt.upstreams, checker, host)
				if !available {
					http.Error(w, "upstream unhealthy: "+host, http.StatusServiceUnavailable)
					return
				}
				selectedIndex = index
			}
			event := events.Event{
				Time:       observedAt,
				Type:       events.AccessAllowed,
				Actor:      principal.Subject,
				Host:       host,
				RemoteAddr: req.RemoteAddr,
				Context:    &decisionContext,
			}
			if !emitAccessEvent(w, req, cfg, event, rt.auditRequired) {
				return
			}
			req = req.WithContext(pipeline.WithVisitor(req.Context(), pipeline.Visitor{Subject: principal.Subject, Email: principal.Email}))
		}
		// The identity the application trusts: the one this edge just
		// authorized, or on a connector the one the edge asserted through
		// the tunnel. The trust stage already removed client copies.
		if visitor, ok := pipeline.VisitorFrom(req.Context()); ok {
			pipeline.SetIdentityHeaders(req.Header, visitor)
		}
		// The address the trust stage selected, never a header read here: a
		// peer outside trusted_proxies cannot choose what is logged.
		clientIP := ""
		if ip, ok := pipeline.ClientIP(req.Context()); ok {
			clientIP = ip.String()
		}
		handler := rt.handler
		target := rt.target
		if len(rt.upstreams) > 0 {
			if selectedIndex < 0 {
				var available bool
				selectedIndex, available = rt.selector.selectIndex(rt.upstreams, checker, host)
				if !available {
					http.Error(w, "upstream unhealthy: "+host, http.StatusServiceUnavailable)
					return
				}
			}
			handler = rt.upstreams[selectedIndex].handler
			target = rt.upstreams[selectedIndex].target
		}
		// Path only: a query string can carry tokens and must never be logged.
		cfg.logger.LogAttrs(req.Context(), slog.LevelInfo, "proxy.forward",
			slog.String("method", req.Method),
			slog.String("host", host),
			slog.String("path", req.URL.Path),
			slog.String("target", targetLogLabel(target)),
			slog.String("client_ip", clientIP),
		)
		handler.ServeHTTP(w, req)
	})

	// The trust stage runs before anything reads the request: spoofed
	// identity headers never survive to the forwarding handler.
	trust := pipeline.TrustWithProxies(cfg.trustedProxies)
	if cfg.tunnelTrust {
		trust = pipeline.TrustAuthenticatedTunnel()
	}
	return pipeline.Chain(trust)(forward), nil
}

// emitAccessEvent appends before publishing to the live event stream. A
// required-route failure prevents any response or upstream forwarding; a
// best-effort failure is reported by the appender wrapper and remains visible
// in the live stream while normal request handling continues.
func emitAccessEvent(w http.ResponseWriter, r *http.Request, cfg options, event events.Event, required bool) bool {
	if cfg.audit != nil {
		if err := cfg.audit.Append(r.Context(), event); err != nil && required {
			cfg.sink.Emit(event)
			http.Error(w, "audit unavailable", http.StatusServiceUnavailable)
			return false
		}
	}
	cfg.sink.Emit(event)
	return true
}

// stripCookie returns a Cookie header value without the named cookie, keeping
// the other pairs in order. It returns "" when nothing is left; the caller
// decides whether that means removing the header.
func stripCookie(header, name string) string {
	var kept []string
	for _, pair := range strings.Split(header, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		cookieName, _, _ := strings.Cut(pair, "=")
		if strings.TrimSpace(cookieName) == name {
			continue
		}
		kept = append(kept, pair)
	}
	return strings.Join(kept, "; ")
}

// writeDevNotFound lists the configured hosts for a local developer who
// typed the wrong one; production keeps the terse message.
func writeDevNotFound(w http.ResponseWriter, requested string, byHost map[string]route) {
	hosts := make([]string, 0, len(byHost))
	for host := range byHost {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "no route for host %q\nknown hosts:\n", requested)
	for _, host := range hosts {
		fmt.Fprintf(w, "  %s\n", host)
	}
}

// upstreamHandler builds what a route forwards to. A static root must already
// exist as a directory so a typo fails at startup instead of serving 404s.
func upstreamHandler(r config.Route, devErrors, assertVisitor bool, transport http.RoundTripper, sites SiteDirs, logger *slog.Logger, responseBuffers *responseBufferPool, reportFailure func()) (http.Handler, error) {
	if r.Site != "" {
		// A site may have no deployment yet: that is a 503 at request time,
		// not a startup error, so the first deploy can follow the route.
		if sites == nil {
			return nil, fmt.Errorf("route %s: site routes require pages to be enabled", r.Host)
		}
		site := r.Site
		return static.Handler{Live: func() (pages.Live, bool) { return sites.Live(site) }, SPA: r.SPA}, nil
	}
	if r.IsStatic() {
		info, err := os.Stat(r.Root)
		if err != nil {
			return nil, fmt.Errorf("route %s: static root: %w", r.Host, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("route %s: static root %q is not a directory", r.Host, r.Root)
		}
		return static.Handler{Dir: r.Root, SPA: r.SPA}, nil
	}
	target, err := config.ParseTarget(r.Target)
	if err != nil {
		return nil, fmt.Errorf("route %s: invalid target %q: %w", r.Host, r.Target, err)
	}
	proxy := &httputil.ReverseProxy{
		Transport:  transport,
		BufferPool: responseBuffers,
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The identify stage already read the session cookie; the outbound
			// request is where it stops, so no upstream ever sees the edge
			// session. Other cookies are preserved.
			if cookie := stripCookie(pr.Out.Header.Get("Cookie"), auth.SessionCookieName); cookie != "" {
				pr.Out.Header.Set("Cookie", cookie)
			} else {
				pr.Out.Header.Del("Cookie")
			}
			pr.SetURL(target)
			pr.SetXForwarded()
			// A client-supplied forwarding chain is never trusted: forward
			// a single address from Trust's decision, never an incoming
			// chain. RemoteAddr still identifies the TCP peer.
			pr.Out.Header.Del("X-Forwarded-For")
			if ip, ok := pipeline.ClientIP(pr.In.Context()); ok {
				pr.Out.Header.Set("X-Forwarded-For", ip.String())
				if assertVisitor {
					pr.Out.Header.Set(pipeline.VisitorIPHeader, ip.String())
				}
			}
			// Into the tunnel the identity travels as an assertion the
			// connector believes; the application-facing headers would be
			// stripped there as client input anyway.
			if assertVisitor {
				pr.Out.Header.Del(pipeline.IdentityEmailHeader)
				pr.Out.Header.Del(pipeline.IdentityUserHeader)
				if visitor, ok := pipeline.VisitorFrom(pr.In.Context()); ok {
					pr.Out.Header.Set(pipeline.VisitorUserHeader, visitor.Subject)
					if visitor.Email != "" {
						pr.Out.Header.Set(pipeline.VisitorEmailHeader, visitor.Email)
					}
				}
			}
			// SetURL clears the outbound Host (the transport would use the
			// internal target); keep the public host the client asked for.
			pr.Out.Host = pr.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			if req.Context().Err() != nil {
				return
			}
			if reportFailure != nil {
				reportFailure()
			}
			status := http.StatusBadGateway
			if isTimeout(err) {
				status = http.StatusGatewayTimeout
			}
			// Logs keep a stable failure class and the target authority only.
			// Transport error strings can contain credentials, paths or tokens.
			logger.LogAttrs(req.Context(), slog.LevelWarn, "proxy.upstream_error",
				slog.String("host", req.Host),
				slog.String("target_host", target.Host),
				slog.Int("status", status),
				slog.String("failure_class", upstreamFailureClass(err)),
			)
			if devErrors {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(status)
				fmt.Fprintf(w, "upstream %s is unreachable or too slow: %v\nhint: is the app running and listening on that port?\n", target, err)
				return
			}
			http.Error(w, http.StatusText(status), status)
		},
	}
	return proxy, nil
}

// NewUpstreamTransport builds the transport every route without a custom one
// forwards through. The route manager builds its own so it can reuse it across
// reloads and close its idle connections when it is replaced. It is explicit instead of a copy of http.DefaultTransport:
// routes point at internal hosts, so HTTP_PROXY style variables must never
// reroute them, and every phase before the body has a limit. The body itself
// is unbounded on purpose, so downloads and streams keep working.
func NewUpstreamTransport(responseHeaderTimeout time.Duration) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
	}
}

// isTimeout reports whether err means the upstream was too slow, as opposed
// to refusing the connection or being unreachable.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func upstreamFailureClass(err error) string {
	if isTimeout(err) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "network"
	}
	return "transport"
}

func targetLogLabel(raw string) string {
	if strings.HasPrefix(raw, "connector:") || strings.HasPrefix(raw, "site:") {
		return raw
	}
	target, err := config.ParseTarget(raw)
	if err != nil {
		return "static"
	}
	return target.Scheme + "://" + target.Host
}

// withoutPort returns a normalized host without its port, and whether it had
// one. IPv6 literals keep their brackets, as NormalizeHost writes them.
func withoutPort(host string) (string, bool) {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		return "", false
	}
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	bare, err := config.NormalizeHost(h)
	return bare, err == nil
}
