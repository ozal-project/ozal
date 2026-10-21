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
	"time"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/pages"
	"github.com/octaviosantacruz/switchboard/internal/pipeline"
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

type route struct {
	target       string
	handler      http.Handler // reverse proxy, or static site for root routes
	authRequired bool
	access       config.AccessPolicy
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
		if r.Connector != "" {
			if r.Target != "" || r.IsStatic() {
				return nil, fmt.Errorf("route %s: unsupported connector destination", r.Host)
			}
			transport = cfg.connectorTransports[r.Connector]
			if transport == nil {
				return nil, fmt.Errorf("route %s: connector transport unavailable", r.Host)
			}
			destination.Target = "http://tunnel.invalid"
			assertVisitor = true
		}
		handler, err := upstreamHandler(destination, cfg.devErrors, assertVisitor, transport, cfg.sites, cfg.logger)
		if err != nil {
			return nil, err
		}
		byHost[host] = route{
			target:       r.Destination(),
			handler:      handler,
			authRequired: r.RequiresAuth(),
			access:       r.Policy(),
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
			decision := auth.Authorize(principal, rt.access)
			if !decision.Allowed {
				cfg.sink.Emit(events.Event{
					Time:       time.Now(),
					Type:       events.AccessDenied,
					Actor:      principal.Subject,
					Host:       host,
					Code:       string(decision.Code),
					Reason:     decision.Reason,
					RemoteAddr: req.RemoteAddr,
				})
				http.Error(w, "access denied", http.StatusForbidden)
				return
			}
			// Step-up: allowed, but the login is too old or used a synced
			// passkey on a route that needs a device-bound one. The new login
			// replaces the session, which rotates its ID.
			if fresh := auth.Fresh(principal, rt.access, time.Now()); !fresh.Allowed {
				cfg.sink.Emit(events.Event{
					Time:       time.Now(),
					Type:       events.AccessStepUp,
					Actor:      principal.Subject,
					Host:       host,
					Code:       string(fresh.Code),
					Reason:     fresh.Reason,
					RemoteAddr: req.RemoteAddr,
				})
				if stepUp, ok := gate.(StepUpGate); ok {
					stepUp.RedirectToStepUp(w, req, fresh.Code, rt.access.Methods)
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
					pipeline.WriteRetryAfter(w, wait)
					cfg.sink.Emit(events.Event{
						Time:       time.Now(),
						Type:       events.AccessRateLimited,
						Actor:      principal.Subject,
						Host:       host,
						Code:       "identity",
						Reason:     "rate limit reached",
						RemoteAddr: req.RemoteAddr,
					})
					http.Error(w, "too many requests", http.StatusTooManyRequests)
					return
				}
			}
			cfg.sink.Emit(events.Event{
				Time:       time.Now(),
				Type:       events.AccessAllowed,
				Actor:      principal.Subject,
				Host:       host,
				RemoteAddr: req.RemoteAddr,
			})
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
		// Path only: a query string can carry tokens and must never be logged.
		cfg.logger.LogAttrs(req.Context(), slog.LevelInfo, "proxy.forward",
			slog.String("method", req.Method),
			slog.String("host", host),
			slog.String("path", req.URL.Path),
			slog.String("target", rt.target),
			slog.String("client_ip", clientIP),
		)
		rt.handler.ServeHTTP(w, req)
	})

	// The trust stage runs before anything reads the request: spoofed
	// identity headers never survive to the forwarding handler.
	trust := pipeline.TrustWithProxies(cfg.trustedProxies)
	if cfg.tunnelTrust {
		trust = pipeline.TrustAuthenticatedTunnel()
	}
	return pipeline.Chain(trust)(forward), nil
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
func upstreamHandler(r config.Route, devErrors, assertVisitor bool, transport http.RoundTripper, sites SiteDirs, logger *slog.Logger) (http.Handler, error) {
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
		Transport: transport,
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
			status := http.StatusBadGateway
			if isTimeout(err) {
				status = http.StatusGatewayTimeout
			}
			// The failure goes to the operator's log; the visitor only gets
			// the error class unless development errors are on.
			logger.LogAttrs(req.Context(), slog.LevelWarn, "proxy.upstream_error",
				slog.String("host", req.Host),
				slog.String("target", target.String()),
				slog.Int("status", status),
				slog.String("error", err.Error()),
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
