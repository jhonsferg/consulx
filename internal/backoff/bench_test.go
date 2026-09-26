//go:build bench

// Benchmarks of the retry helpers, excluded from normal builds by the bench
// build tag. See docs/benchmarks.md.
package backoff

import (
	"context"
	"errors"
	"testing"
	"time"
)

func BenchmarkNextDelay(b *testing.B) {
	for _, tc := range []struct {
		name string
		p    Policy
	}{
		{"plain", Policy{Initial: 500 * time.Millisecond, Max: 30 * time.Second, Multiplier: 2}},
		{"jitter", Policy{Initial: 500 * time.Millisecond, Max: 30 * time.Second, Multiplier: 2, Jitter: true}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			attempt := 0
			b.ReportAllocs()
			for b.Loop() {
				attempt = attempt%10 + 1
				_, _ = tc.p.NextDelay(attempt)
			}
		})
	}
}

// BenchmarkRetry measures the retry wrapper around a call that succeeds at
// once, the normal case of every registration.
func BenchmarkRetry(b *testing.B) {
	ctx := context.Background()
	p := Policy{Initial: time.Millisecond, Max: time.Second, Multiplier: 2}
	ok := func(context.Context) error { return nil }
	b.Run("success", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Retry(ctx, p, 0, nil, ok)
		}
	})
	errBoom := errors.New("boom")
	fail := func(context.Context) error { return Permanent(errBoom) }
	b.Run("permanent", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Retry(ctx, p, 0, nil, fail)
		}
	})
}
