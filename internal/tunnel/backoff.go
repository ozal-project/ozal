package tunnel

import (
	"math"
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
	if b.Base <= 0 {
		return 0
	}
	max := b.Max
	if max < b.Base {
		// A malformed cap must not turn a configured retry loop into a
		// zero-delay spin. Treat Base as the smallest usable cap.
		max = b.Base
	}
	ceil := b.Base
	for i := 0; i < attempt && ceil < max; i++ {
		// Compare before doubling so large, valid durations cannot wrap
		// negative and escape the configured cap.
		if ceil > max/2 {
			ceil = max
			break
		}
		ceil *= 2
	}
	if ceil > max {
		ceil = max
	}
	r := b.Rand
	if r == nil {
		r = rand.Float64
	}
	half := ceil / 2
	if half == 0 {
		return ceil
	}
	value := r()
	if math.IsNaN(value) || value < 0 {
		value = 0
	} else if value >= 1 {
		value = math.Nextafter(1, 0)
	}
	jitter := time.Duration(value * float64(half))
	// Float64 cannot represent every nanosecond near MaxInt64; clamp a
	// rounded endpoint so the result still stays below the exponential cap.
	if jitter >= half {
		jitter = half - 1
	}
	return half + jitter
}
