// Package ratelimit holds the token bucket behind the pipeline's ratelimit
// stage (SHD-001). The bucket is pure: time is passed in, so tests need no
// sleeps and no fake clock.
package ratelimit

import (
	"math"
	"time"
)

// Bucket allows `burst` requests at once and refills at `rate` tokens per
// second. The zero value is not usable; build one with NewBucket.
// A Bucket is not safe for concurrent use: the caller (a keyed limiter)
// guards it.
type Bucket struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// NewBucket returns a full bucket created at `now`. rate must be > 0.
func NewBucket(rate float64, burst int, now time.Time) *Bucket {
	return &Bucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: now}
}

// Allow reports whether one request at `now` may proceed, and spends a token
// when it does. A clock that goes backwards refills nothing.
func (b *Bucket) Allow(now time.Time) bool {
	b.refill(now)
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// RetryAfter returns how long a caller at `now` must wait until one token is
// available again — the honest figure behind a Retry-After header. A full
// bucket returns zero; a partially refilled one rounds up to whole
// nanoseconds so the answer is never earlier than the real refill.
func (b *Bucket) RetryAfter(now time.Time) time.Duration {
	if b.rate <= 0 {
		return 0
	}
	b.refill(now)
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration(math.Ceil((1 - b.tokens) / b.rate * float64(time.Second)))
}

// refill advances the bucket to `now` without spending a token. It is the
// one place that converts elapsed time into tokens.
func (b *Bucket) refill(now time.Time) {
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
		b.last = now
	}
}
