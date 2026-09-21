// Package proxy builds an http.Handler that reverse-proxies by Host header.
package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/octaviosantacruz/switchboard/internal/config"
)

type route struct {
	target string
	proxy  *httputil.ReverseProxy
}

// HealthChecker reports whether the upstream for a route's host is
// currently healthy. A nil HealthChecker is treated as "always healthy" —
// callers that don't run health checks can pass nil.
type HealthChecker interface {
	Healthy(host string) bool
}

// New builds a host-routed reverse proxy handler from the given routes.
// It fails fast if any target URL is invalid. checker may be nil.
func New(routes []config.Route, checker HealthChecker) (http.Handler, error) {
	byHost := make(map[string]route, len(routes))

	for _, r := range routes {
		target, err := url.Parse(r.Target)
		if err != nil {
			return nil, fmt.Errorf("route %s: invalid target %q: %w", r.Host, r.Target, err)
		}
		byHost[r.Host] = route{target: r.Target, proxy: httputil.NewSingleHostReverseProxy(target)}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rt, ok := byHost[req.Host]
		if !ok {
			http.Error(w, "no route for host: "+req.Host, http.StatusNotFound)
			return
		}
		if checker != nil && !checker.Healthy(req.Host) {
			http.Error(w, "upstream unhealthy: "+req.Host, http.StatusServiceUnavailable)
			return
		}
		log.Printf("%s %s%s -> %s", req.Method, req.Host, req.URL.Path, rt.target)
		rt.proxy.ServeHTTP(w, req)
	}), nil
}
