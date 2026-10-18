package ratelimit

import (
	"sync"
	"time"
)

// DefaultMaxKeys bounds how many buckets one limiter keeps when no explicit
// capacity is configured. A scan that varies its key (client IP, hostname)
// must not be able to grow the map without bound.
const DefaultMaxKeys = 10_000

// Limiter keeps one Bucket per key (for example a client IP, a route key or
// an authenticated identity) and evicts keys that have been idle, so memory
// cannot grow without bound under a scan.
type Limiter struct {
	rate    float64
	burst   int
	idle    time.Duration
	maxKeys int
	now     func() time.Time

	mu        sync.Mutex
	buckets   map[string]*Bucket
	lastSweep time.Time
	capacity  int
}

// Option configures a Limiter.
type Option func(*Limiter)

// WithMaxKeys caps how many distinct keys a limiter holds. Reaching the cap
// never frees budget for existing keys; see Allow.
func WithMaxKeys(n int) Option {
	return func(l *Limiter) {
		if n > 0 {
			l.maxKeys = n
		}
	}
}

// WithNow injects the clock; tests use it instead of sleeping.
func WithNow(now func() time.Time) Option {
	return func(l *Limiter) {
		if now != nil {
			l.now = now
		}
	}
}

// NewLimiter builds a Limiter. Buckets untouched for `idle` are dropped; a
// dropped bucket would have refilled to full anyway when idle >= burst/rate.
func NewLimiter(rate float64, burst int, idle time.Duration, opts ...Option) *Limiter {
	l := &Limiter{rate: rate, burst: burst, idle: idle, maxKeys: DefaultMaxKeys, now: time.Now, buckets: map[string]*Bucket{}}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Allow charges one token to key. When the request is refused, the second
// result is how long to wait before retrying (zero when unknown).
//
// At capacity the limiter is fail closed: it first reclaims buckets that are
// already refilled to full — dropping one of those gives nothing away — and
// otherwise refuses the new key outright. Evicting a bucket that still owes
// its key a restriction would let an attacker with many keys reset a victim's
// budget, so that never happens.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) >= l.idle {
		l.sweep(now)
	}
	bucket, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			if !l.reclaim(now) {
				l.capacity++
				return false, 0
			}
		}
		bucket = NewBucket(l.rate, l.burst, now)
		l.buckets[key] = bucket
	}
	if bucket.Allow(now) {
		return true, 0
	}
	return false, bucket.RetryAfter(now)
}

// sweep drops every bucket that has been idle for at least the limiter's
// idle window. Idle buckets are full again, so no restriction is lost.
func (l *Limiter) sweep(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.last) >= l.idle {
			delete(l.buckets, key)
		}
	}
	l.lastSweep = now
}

// reclaim drops the buckets that are already refilled to full and reports
// whether it freed at least one key.
func (l *Limiter) reclaim(now time.Time) bool {
	freed := false
	for key, bucket := range l.buckets {
		bucket.refill(now)
		if bucket.tokens >= bucket.burst {
			delete(l.buckets, key)
			freed = true
		}
	}
	return freed
}

// Keys reports how many buckets the limiter currently holds.
func (l *Limiter) Keys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// CapacityDenials reports how many requests were refused because the key cap
// was reached and no bucket could be reclaimed. A number that keeps growing
// means the cap is too small for the traffic, not that clients are abusive.
func (l *Limiter) CapacityDenials() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.capacity
}
