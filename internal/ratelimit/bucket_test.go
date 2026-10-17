package ratelimit

import (
	"testing"
	"time"
)

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
