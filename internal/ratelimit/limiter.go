package ratelimit

import (
	"sync"
	"time"
)

// Limiter keeps one Bucket per key (for example a client IP) and evicts keys
// that have been idle, so memory cannot grow without bound under a scan.
type Limiter struct {
	rate  float64
	burst int
	idle  time.Duration
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*Bucket
	lastSweep time.Time
}

// NewLimiter builds a Limiter. Buckets untouched for `idle` are dropped; a
// dropped bucket would have refilled to full anyway when idle >= burst/rate.
func NewLimiter(rate float64, burst int, idle time.Duration) *Limiter {
	return &Limiter{rate: rate, burst: burst, idle: idle, now: time.Now, buckets: map[string]*Bucket{}}
}

// Allow charges one token to key.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) >= l.idle {
		for k, b := range l.buckets {
			if now.Sub(b.last) >= l.idle {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = NewBucket(l.rate, l.burst, now)
		l.buckets[key] = b
	}
	return b.Allow(now)
}
