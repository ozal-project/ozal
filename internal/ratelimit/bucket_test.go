package ratelimit

import (
	"testing"
	"time"
)

func TestBucketRetryAfter(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		rate  float64
		burst int
		drain int
		after time.Duration
		want  time.Duration
	}{
		{"full bucket needs no wait", 1, 1, 0, 0, 0},
		{"one token at 2/s is 500ms", 2, 1, 1, 0, 500 * time.Millisecond},
		{"two tokens at 0.5/s is 2s", 0.5, 2, 2, 0, 2 * time.Second},
		{"wait shrinks with refill", 1, 1, 1, 250 * time.Millisecond, 750 * time.Millisecond},
		{"refilled bucket needs no wait", 1, 1, 1, time.Second, 0},
		{"fractional wait rounds up", 3, 1, 1, 0, 333333334 * time.Nanosecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBucket(tt.rate, tt.burst, t0)
			for i := 0; i < tt.drain; i++ {
				b.Allow(t0)
			}
			if got := b.RetryAfter(t0.Add(tt.after)); got != tt.want {
				t.Fatalf("RetryAfter = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBucketRetryAfterNeverEarlierThanRefill(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	b := NewBucket(3, 1, t0)
	b.Allow(t0)
	wait := b.RetryAfter(t0)
	if tokens := wait.Seconds() * 3; tokens < 1 {
		t.Fatalf("wait = %v refills only %f tokens, want at least one", wait, tokens)
	}
	c := NewBucket(3, 1, t0)
	c.Allow(t0)
	if !c.Allow(t0.Add(wait)) {
		t.Fatalf("a request at the advertised wait (%v) was still refused", wait)
	}
}

func TestBucketAllow(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return t0.Add(d) }

	tests := []struct {
		name  string
		rate  float64
		burst int
		calls []struct {
			after time.Duration
			want  bool
		}
	}{
		{
			name: "burst then deny", rate: 1, burst: 2,
			calls: []struct {
				after time.Duration
				want  bool
			}{{0, true}, {0, true}, {0, false}},
		},
		{
			name: "refills over time", rate: 1, burst: 1,
			calls: []struct {
				after time.Duration
				want  bool
			}{{0, true}, {500 * time.Millisecond, false}, {1 * time.Second, true}},
		},
		{
			name: "refill is capped at burst", rate: 10, burst: 2,
			calls: []struct {
				after time.Duration
				want  bool
			}{{0, true}, {time.Hour, true}, {time.Hour, true}, {time.Hour, false}},
		},
		{
			name: "clock going backwards refills nothing", rate: 1, burst: 1,
			calls: []struct {
				after time.Duration
				want  bool
			}{{time.Second, true}, {0, false}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBucket(tt.rate, tt.burst, t0)
			for i, c := range tt.calls {
				if got := b.Allow(at(c.after)); got != c.want {
					t.Fatalf("call %d at +%v: got %v, want %v", i, c.after, got, c.want)
				}
			}
		})
	}
}
