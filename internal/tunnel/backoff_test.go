package tunnel

import (
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	const (
		lo = 0.0
		hi = 0.999999
	)
	b := func(r float64) Backoff {
		return Backoff{Base: time.Second, Max: 8 * time.Second, Rand: func() float64 { return r }}
	}
	tests := []struct {
		name       string
		attempt    int
		minDelay   time.Duration // rand = 0
		maxDelayLT time.Duration // rand just under 1
	}{
		{"first retry is 0.5-1s", 0, 500 * time.Millisecond, time.Second},
		{"doubles", 1, time.Second, 2 * time.Second},
		{"keeps doubling", 2, 2 * time.Second, 4 * time.Second},
		{"capped at max", 3, 4 * time.Second, 8 * time.Second},
		{"stays capped", 50, 4 * time.Second, 8 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := b(lo).Delay(tt.attempt); got != tt.minDelay {
				t.Errorf("min: got %v, want %v", got, tt.minDelay)
			}
			if got := b(hi).Delay(tt.attempt); got < tt.minDelay || got >= tt.maxDelayLT {
				t.Errorf("max: got %v, want in [%v, %v)", got, tt.minDelay, tt.maxDelayLT)
			}
		})
	}
}

func TestBackoffDefaultRandIsBounded(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if d := DefaultBackoff.Delay(2); d < 2*time.Second || d >= 4*time.Second {
			t.Fatalf("delay %v outside [2s,4s)", d)
		}
	}
}

func TestBackoffDelayDoesNotOverflowOrSpin(t *testing.T) {
	const maxDuration = time.Duration(1<<63 - 1)
	for _, backoff := range []Backoff{
		{Base: maxDuration/2 + 10, Max: maxDuration, Rand: func() float64 { return 0 }},
		{Base: time.Second, Max: 0, Rand: func() float64 { return 0 }},
	} {
		delay := backoff.Delay(100)
		if delay <= 0 {
			t.Fatalf("Delay(%+v) = %v, want a positive retry delay", backoff, delay)
		}
		if backoff.Max >= backoff.Base && delay > backoff.Max {
			t.Fatalf("Delay(%+v) = %v, exceeds cap", backoff, delay)
		}
	}
}

// FuzzBackoffDelay checks that exponential growth saturates instead of
// overflowing, and that jitter always stays within its calculated ceiling.
func FuzzBackoffDelay(f *testing.F) {
	f.Add(int64(1), int64(1), uint8(0), uint64(0))
	f.Add(int64(time.Second), int64(30*time.Second), uint8(40), uint64(^uint64(0)))
	f.Add(int64(1<<62), int64(1<<63-1), uint8(255), uint64(1<<63))

	f.Fuzz(func(t *testing.T, rawBase, rawMax int64, attempt uint8, rawRand uint64) {
		const largestDuration = uint64(1<<63 - 1)
		baseNanos := uint64(rawBase) & largestDuration
		if baseNanos == 0 {
			baseNanos = 1
		}
		maxSpan := largestDuration - baseNanos
		maxNanos := baseNanos + uint64(rawMax)&maxSpan
		// Force Max to be at least Base while still exploring caps near
		// MaxInt64, where naive exponential multiplication can overflow.
		if maxNanos < baseNanos {
			maxNanos = baseNanos
		}
		base, max := time.Duration(baseNanos), time.Duration(maxNanos)
		randValue := float64(rawRand>>11) / float64(uint64(1)<<53)
		got := (Backoff{Base: base, Max: max, Rand: func() float64 { return randValue }}).Delay(int(attempt))

		ceil := base
		for i := 0; i < int(attempt) && ceil < max; i++ {
			if ceil > max/2 {
				ceil = max
				break
			}
			ceil *= 2
		}
		if ceil > max {
			ceil = max
		}
		half := ceil / 2
		if got < half || got > ceil || got <= 0 {
			t.Fatalf("Delay(base=%v max=%v attempt=%d) = %v, outside [%v,%v]", base, max, attempt, got, half, ceil)
		}
		if half > 0 && got >= ceil {
			t.Fatalf("Delay(base=%v max=%v attempt=%d) = %v, reached exclusive ceiling %v", base, max, attempt, got, ceil)
		}
	})
}
