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
