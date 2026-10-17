package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiter(1, 1, time.Minute)
	if !l.Allow("a") || l.Allow("a") {
		t.Fatal("key a: want allow then deny")
	}
	if !l.Allow("b") {
		t.Fatal("key b must not share a's bucket")
	}
}

func TestLimiterEvictsIdleKeys(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(1, 1, time.Minute)
	l.now = func() time.Time { return now }
	l.Allow("a")
	now = now.Add(2 * time.Minute)
	l.Allow("b")
	if _, ok := l.buckets["a"]; ok {
		t.Fatal("idle key a was not evicted")
	}
}
