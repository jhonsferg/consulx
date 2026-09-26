//go:build bench

// Benchmarks of the blocking query helpers, excluded from normal builds by
// the bench build tag. They run once per blocking request of every watch.
// See docs/benchmarks.md.
package blocking

import (
	"context"
	"testing"
	"time"
)

func BenchmarkLimiterWait(b *testing.B) {
	ctx := context.Background()
	b.Run("token-available", func(b *testing.B) {
		// The clock advances one interval per call, so the bucket always has
		// a token: the no-wait path of a watch whose agent blocks normally.
		l := NewLimiter(time.Second, 2)
		now := time.Now()
		l.now = func() time.Time { now = now.Add(time.Second); return now }
		b.ReportAllocs()
		for b.Loop() {
			_ = l.Wait(ctx)
		}
	})
	b.Run("disabled", func(b *testing.B) {
		l := NewLimiter(0, 2)
		b.ReportAllocs()
		for b.Loop() {
			_ = l.Wait(ctx)
		}
	})
}

func BenchmarkIndex(b *testing.B) {
	b.Run("NextIndex", func(b *testing.B) {
		var idx uint64
		b.ReportAllocs()
		for b.Loop() {
			idx = NextIndex(idx, idx+1)
		}
	})
	b.Run("RequestTimeout", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = RequestTimeout(5*time.Minute, 10*time.Second)
		}
	})
}
