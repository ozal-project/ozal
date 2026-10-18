package pipeline

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
)

// Allower decides whether a request keyed by it may proceed. When it may
// not, the second result is how long the caller should wait before retrying;
// zero means the limiter cannot estimate.
type Allower interface {
	Allow(key string) (bool, time.Duration)
}

// RateLimitOption configures the ratelimit stage.
type RateLimitOption func(*rateLimitConfig)

type rateLimitConfig struct {
	sink events.Sink
}

// WithRateLimitSink publishes an event for every refused request. Without it
// the stage decides silently.
func WithRateLimitSink(sink events.Sink) RateLimitOption {
	return func(cfg *rateLimitConfig) {
		if sink != nil {
			cfg.sink = sink
		}
	}
}

// RateLimit is the ratelimit stage. It runs after Trust, so the key is the
// resolved client IP, never a client-supplied header, and it names the route
// by its host so one busy hostname cannot spend another's budget. Requests
// under authPrefix use the stricter `auth` limiter. A request with no
// resolvable IP is limited under one shared key rather than skipped.
func RateLimit(general, auth Allower, authPrefix string, opts ...RateLimitOption) Stage {
	cfg := rateLimitConfig{sink: events.Discard}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "unknown"
			if ip, ok := ClientIP(r.Context()); ok {
				key = ip.String()
			}
			host, err := config.NormalizeHost(r.Host)
			if err != nil {
				http.Error(w, "invalid host", http.StatusBadRequest)
				return
			}
			key = key + "|" + host

			limiter, code := general, "ip_route"
			if authPrefix != "" && strings.HasPrefix(r.URL.Path, authPrefix) {
				limiter, code = auth, "auth_path"
			}
			if limiter != nil {
				if allowed, wait := limiter.Allow(key); !allowed {
					WriteRetryAfter(w, wait)
					cfg.sink.Emit(events.Event{
						Time:       time.Now(),
						Type:       events.AccessRateLimited,
						Host:       r.Host,
						Code:       code,
						Reason:     "rate limit reached",
						RemoteAddr: r.RemoteAddr,
					})
					http.Error(w, "too many requests", http.StatusTooManyRequests)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteRetryAfter sets the Retry-After header from a real wait and writes
// the 429 body. RFC 9110 wants integer seconds; a wait under a second or an
// unknown wait still advertises one second, because zero would read as
// "retry immediately".
func WriteRetryAfter(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(wait)))
}

// RetryAfterSeconds rounds a wait up to whole seconds with a floor of one.
func RetryAfterSeconds(wait time.Duration) int {
	if wait <= 0 {
		return 1
	}
	return max(1, int(math.Ceil(wait.Seconds())))
}
