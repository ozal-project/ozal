package ratelimit

import (
	"testing"
	"time"
)

func BenchmarkLimiterAllowExistingKey(b *testing.B) {
	limiter := NewLimiter(1e12, 1e12, time.Hour)
	limiter.Allow("client")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		limiter.Allow("client")
	}
}

func BenchmarkLimiterParallelSameKey(b *testing.B) {
	limiter := NewLimiter(1e12, 1e12, time.Hour)
	limiter.Allow("client")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			limiter.Allow("client")
		}
	})
}
