package tunnel

import (
	"math/rand/v2"
	"time"
)

// Backoff computes reconnect delays: exponential up to Max, with "equal
// jitter" (half fixed, half random) so many connectors restarting together do
// not retry in lockstep, and no delay is ever near zero.
type Backoff struct {
	Base time.Duration
	Max  time.Duration
	// Rand returns a value in [0,1). Nil uses math/rand/v2; tests inject a
	// constant to make delays deterministic.
	Rand func() float64
}

// DefaultBackoff retries after 0.5-1s, then 1-2s... capped at 15-30s.
var DefaultBackoff = Backoff{Base: time.Second, Max: 30 * time.Second}

// Delay returns the wait before retry number `attempt` (0 for the first).
func (b Backoff) Delay(attempt int) time.Duration {
	ceil := b.Base
	for i := 0; i < attempt && ceil < b.Max; i++ {
		ceil *= 2
	}
	if ceil > b.Max {
		ceil = b.Max
	}
	r := b.Rand
	if r == nil {
		r = rand.Float64
	}
	half := ceil / 2
	return half + time.Duration(r()*float64(half))
}
