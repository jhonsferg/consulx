// Package blocking implements the client-side rules of Consul blocking
// queries (https://developer.hashicorp.com/consul/api-docs/features/blocking):
// index sanity checks and request rate limiting.
package blocking

import (
	"context"
	"sync"
	"time"
)

// NextIndex returns the WaitIndex for the next request given the index of
// the previous request (prev) and the X-Consul-Index just received (got).
//
//   - An index that goes backwards (snapshot restore, leader change) resets
//     to 0 so no update is missed.
//   - 0 is never used as a wait index after the first request, because
//     Consul would answer immediately and the watch would spin; 1 is always
//     safe.
func NextIndex(prev, got uint64) uint64 {
	if got < prev {
		return 0
	}
	if got == 0 {
		return 1
	}
	return got
}

// Limiter is a token bucket. Consul recommends a small burst so rapid
// updates are delivered quickly while a misbehaving endpoint answering
// without blocking cannot turn a watch into a busy loop. It is safe for
// concurrent use.
type Limiter struct {
	interval time.Duration
	burst    float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewLimiter allows burst requests at once and one more per interval.
func NewLimiter(interval time.Duration, burst int) *Limiter {
	return &Limiter{interval: interval, burst: float64(burst), tokens: float64(burst), now: time.Now}
}

// Wait blocks until a request is allowed or ctx is done.
func (l *Limiter) Wait(ctx context.Context) error {
	if l.interval <= 0 {
		return ctx.Err()
	}
	l.mu.Lock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens = min(l.burst, l.tokens+float64(now.Sub(l.last))/float64(l.interval))
	}
	l.last = now
	var wait time.Duration
	if l.tokens >= 1 {
		l.tokens--
	} else {
		wait = time.Duration((1 - l.tokens) * float64(l.interval))
		l.tokens = 0
		l.last = now.Add(wait) // the token is consumed when the wait ends
	}
	l.mu.Unlock()

	if wait <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
