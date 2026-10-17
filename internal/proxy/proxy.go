// Package proxy builds an http.Handler that reverse-proxies by Host header.
package proxy

import (
	"fmt"
	"log"
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
	"github.com/octaviosantacruz/switchboard/internal/pipeline"
	"github.com/octaviosantacruz/switchboard/internal/static"
)

// Option configures the proxy handler.
type Option func(*options)

type options struct {
	devErrors      bool
	sink           events.Sink
	trustedProxies []netip.Prefix
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

// New builds a host-routed reverse proxy handler from the given routes.
// It fails fast on invalid or duplicate hosts and targets. checker and gate may be nil.
func New(routes []config.Route, checker HealthChecker, gate AuthGate, opts ...Option) (http.Handler, error) {
	cfg := options{sink: events.Discard}
	for _, opt := range opts {
		opt(&cfg)
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
		handler, err := upstreamHandler(r, cfg.devErrors)
		if err != nil {
			return nil, err
		}
		policy := config.AccessPolicy{}
		if r.Access != nil {
			policy = *r.Access
		}
		byHost[host] = route{
			target:       r.Destination(),
			handler:      handler,
			authRequired: r.RequiresAuth(),
			access:       policy,
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
			cfg.sink.Emit(events.Event{
				Time:       time.Now(),
				Type:       events.AccessAllowed,
				Actor:      principal.Subject,
				Host:       host,
				RemoteAddr: req.RemoteAddr,
			})
			if principal.Email != "" {
				req.Header.Set(pipeline.IdentityEmailHeader, principal.Email)
			}
			if principal.Subject != "" {
				req.Header.Set(pipeline.IdentityUserHeader, principal.Subject)
			}
		}
		log.Printf("%s %s%s -> %s", req.Method, host, req.URL.Path, rt.target)
		rt.handler.ServeHTTP(w, req)
	})

	// The trust stage runs before anything reads the request: spoofed
	// identity headers never survive to the forwarding handler.
	return pipeline.Chain(pipeline.TrustWithProxies(cfg.trustedProxies))(forward), nil
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
func upstreamHandler(r config.Route, devErrors bool) (http.Handler, error) {
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
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The identify stage already read the session cookie; the outbound
			// request is where it stops, so no upstream ever sees the edge
			// session. Other cookies are preserved.
			if cookie := stripCookie(pr.Out.Header.Get("Cookie"), auth.SessionCookieName); cookie != "" {
				pr.Out.Header.Set("Cookie", cookie)
			} else {
				pr.Out.Header.Del("Cookie")
			}
			// A client-supplied forwarding chain is never trusted: SetXForwarded
			// appends to an existing X-Forwarded-For, so drop it first.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.SetURL(target)
			pr.SetXForwarded()
			// Forward a single address from Trust's decision, never an
			// incoming chain. RemoteAddr still identifies the TCP peer.
			pr.Out.Header.Del("X-Forwarded-For")
			if ip, ok := pipeline.ClientIP(pr.In.Context()); ok {
				pr.Out.Header.Set("X-Forwarded-For", ip.String())
			}
			// SetURL clears the outbound Host (the transport would use the
			// internal target); keep the public host the client asked for.
			pr.Out.Host = pr.In.Host
		},
	}
	if devErrors {
		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "upstream %s is unreachable: %v\nhint: is the app running and listening on that port?\n", target, err)
		}
	}
	return proxy, nil
}
