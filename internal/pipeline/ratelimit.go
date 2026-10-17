package pipeline

import (
	"net/http"
	"strings"
)

// Allower decides whether a request keyed by client IP may proceed.
type Allower interface {
	Allow(key string) bool
}

// RateLimit is the ratelimit stage. It runs after Trust, so the key is the
// resolved client IP, never a client-supplied header. Requests under
// authPrefix use the stricter `auth` limiter. A request with no resolvable IP
// is limited under one shared key rather than skipped.
func RateLimit(general, auth Allower, authPrefix string) Stage {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "unknown"
			if ip, ok := ClientIP(r.Context()); ok {
				key = ip.String()
			}
			limiter := general
			if authPrefix != "" && strings.HasPrefix(r.URL.Path, authPrefix) {
				limiter = auth
			}
			if limiter != nil && !limiter.Allow(key) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
