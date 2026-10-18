package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiter(1, 1, time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("key a: want allow")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("key a: want deny")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("key b must not share a's bucket")
	}
}

func TestLimiterEvictsIdleKeys(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(1, 1, time.Minute, WithNow(func() time.Time { return now }))
	l.Allow("a")
	now = now.Add(2 * time.Minute)
	l.Allow("b")
	if _, ok := l.buckets["a"]; ok {
		t.Fatal("idle key a was not evicted")
	}
}

func TestLimiterReportsTheRealWait(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(2, 1, time.Minute, WithNow(func() time.Time { return now }))
	if ok, wait := l.Allow("a"); !ok || wait != 0 {
		t.Fatalf("first request: ok=%v wait=%v", ok, wait)
	}
	ok, wait := l.Allow("a")
	if ok {
		t.Fatal("second request must be refused")
	}
	if wait != 500*time.Millisecond {
		t.Fatalf("wait = %v, want 500ms at 2 tokens/s", wait)
	}
	// The wait shrinks as the bucket refills, and the next request at the
	// advertised instant is allowed.
	now = now.Add(250 * time.Millisecond)
	if ok, wait := l.Allow("a"); ok || wait != 250*time.Millisecond {
		t.Fatalf("halfway: ok=%v wait=%v, want 250ms", ok, wait)
	}
	now = now.Add(250 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("the advertised wait must be enough")
	}
}

func TestLimiterCapacityIsFailClosed(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(0.001, 1, time.Hour, WithMaxKeys(2), WithNow(func() time.Time { return now }))

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("key a must be allowed")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("key b must be allowed")
	}
	// At capacity with every bucket still owing a restriction, a new key is
	// refused: an attacker cannot buy tokens by inventing keys.
	if ok, wait := l.Allow("c"); ok || wait != 0 {
		t.Fatalf("key c = ok %v wait %v, want a refusal", ok, wait)
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("key a must keep its restriction while the map is at capacity")
	}
	if l.CapacityDenials() != 1 {
		t.Fatalf("capacity denials = %d, want 1", l.CapacityDenials())
	}
	if l.Keys() != 2 {
		t.Fatalf("keys = %d, want the cap of 2", l.Keys())
	}

	// Once the existing buckets refill completely, they are free to drop and
	// new keys are accepted again.
	now = now.Add(2 * time.Hour)
	if ok, _ := l.Allow("d"); !ok {
		t.Fatal("key d must be allowed once full buckets can be reclaimed")
	}
	if l.CapacityDenials() != 1 {
		t.Fatalf("capacity denials grew to %d after reclaim", l.CapacityDenials())
	}
}

func TestLimiterNeverEvictsRestrictedBuckets(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(0.001, 1, time.Hour, WithMaxKeys(1), WithNow(func() time.Time { return now }))
	l.Allow("victim") // victim's bucket is now empty and owes a restriction
	if ok, _ := l.Allow("attacker"); ok {
		t.Fatal("the limiter must not evict a restricted bucket to admit a new key")
	}
	if _, ok := l.buckets["victim"]; !ok {
		t.Fatal("the restricted bucket was evicted")
	}
	if ok, _ := l.Allow("victim"); ok {
		t.Fatal("the evicted victim would have been handed a fresh burst")
	}
}

func TestLimiterConcurrentKeys(t *testing.T) {
	l := NewLimiter(1000, 10, time.Minute, WithMaxKeys(4))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				l.Allow(string(rune('a' + i)))
			}
		}(i)
	}
	wg.Wait()
	if keys := l.Keys(); keys > 4 {
		t.Fatalf("keys = %d, want at most the cap of 4", keys)
	}
}
