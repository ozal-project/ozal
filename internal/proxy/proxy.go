// Package proxy builds an http.Handler that reverse-proxies by Host header.
package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/static"
)

const (
	authEmailHeader = "X-Auth-Request-Email"
	authUserHeader  = "X-Auth-Request-User"
)

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
func New(routes []config.Route, checker HealthChecker, gate AuthGate) (http.Handler, error) {
	byHost := make(map[string]route, len(routes))

	for _, r := range routes {
		host, err := config.NormalizeHost(r.Host)
		if err != nil {
			return nil, fmt.Errorf("route %q has invalid host: %w", r.Host, err)
		}
		if _, exists := byHost[host]; exists {
			return nil, fmt.Errorf("duplicate route host %q", host)
		}
		handler, err := upstreamHandler(r)
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

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Header.Del(authEmailHeader)
		req.Header.Del(authUserHeader)

		host, err := config.NormalizeHost(req.Host)
		if err != nil {
			http.Error(w, "invalid request host", http.StatusBadRequest)
			return
		}
		rt, ok := byHost[host]
		if !ok {
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
				log.Printf("access denied for %s on %s: %s", principal.Subject, host, decision.Code)
				http.Error(w, "access denied", http.StatusForbidden)
				return
			}
			if principal.Email != "" {
				req.Header.Set(authEmailHeader, principal.Email)
			}
			if principal.Subject != "" {
				req.Header.Set(authUserHeader, principal.Subject)
			}
		}
		log.Printf("%s %s%s -> %s", req.Method, host, req.URL.Path, rt.target)
		rt.handler.ServeHTTP(w, req)
	}), nil
}

// upstreamHandler builds what a route forwards to. A static root must already
// exist as a directory so a typo fails at startup instead of serving 404s.
func upstreamHandler(r config.Route) (http.Handler, error) {
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
	return httputil.NewSingleHostReverseProxy(target), nil
}
