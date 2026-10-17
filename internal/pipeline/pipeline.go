// Package pipeline assembles the inbound request stages in the order fixed by
// docs/architecture/design.md:
//
//	trust → ratelimit → shield → identify → authorize → route → forward
//
// Only the trust stage exists as a middleware today; identify, authorize,
// route and forward live inside the proxy's forwarding handler until later
// work lifts them out. New stages slot into Chain at their canonical position
// without changing the order of the rest.
package pipeline

import (
	"net/http"
	"strings"
)

// Identity headers a protected upstream trusts. Switchboard injects them only
// after authentication; client-supplied values (in any spelling) never reach
// an upstream.
const (
	IdentityEmailHeader = "X-Auth-Request-Email"
	IdentityUserHeader  = "X-Auth-Request-User"
)

// Stage is one middleware step of the inbound pipeline.
type Stage func(http.Handler) http.Handler

// Chain applies stages in order: the first listed is the outermost, so it
// sees the request first and the response last.
func Chain(stages ...Stage) Stage {
	return func(next http.Handler) http.Handler {
		handler := next
		for i := len(stages) - 1; i >= 0; i-- {
			handler = stages[i](handler)
		}
		return handler
	}
}

// Trust is the first stage: it removes every client-supplied header whose
// name normalizes to an identity header. WSGI, PHP and Rack map `_` back to
// `-`, so `X_Auth_Request_Email` would otherwise become trusted identity at
// the upstream. delete() uses the raw map key: a non-canonical name written
// straight into the header map would survive Header.Del's canonicalization.
func Trust(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stripIdentityHeaders(r.Header)
		next.ServeHTTP(w, r)
	})
}

func stripIdentityHeaders(h http.Header) {
	for name := range h {
		if isIdentityHeader(name) {
			delete(h, name)
		}
	}
}

func isIdentityHeader(name string) bool {
	normalized := strings.ReplaceAll(name, "_", "-")
	return strings.EqualFold(normalized, IdentityEmailHeader) ||
		strings.EqualFold(normalized, IdentityUserHeader)
}
