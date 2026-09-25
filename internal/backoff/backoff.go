// Package backoff implements capped exponential backoff with jitter and a
// context-aware retry loop.
package backoff

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Delayer computes the wait before a retry. attempt starts at 1 for the
// first retry. ok is false when no further attempt must be made.
type Delayer interface {
	NextDelay(attempt int) (delay time.Duration, ok bool)
}

// Policy is exponential backoff: Initial * Multiplier^(attempt-1), capped at
// Max. With Jitter the delay d becomes a uniform value in [d/2, d] ("equal
// jitter"): instances that failed together spread out, and the wait never
// collapses towards zero, which would turn an outage into a hot loop.
type Policy struct {
	Initial     time.Duration
	Max         time.Duration
	Multiplier  float64
	Jitter      bool
	MaxAttempts int // 0 means unlimited

	// rand returns a value in [0,1). nil uses math/rand/v2. Tests set it.
	rand func() float64
}

// NextDelay implements Delayer.
func (p Policy) NextDelay(attempt int) (time.Duration, bool) {
	if attempt < 1 {
		attempt = 1
	}
	if p.MaxAttempts > 0 && attempt >= p.MaxAttempts {
		return 0, false
	}
	mult := max(p.Multiplier, 1)
	d := float64(p.Initial) * math.Pow(mult, float64(attempt-1))
	if p.Max > 0 && (d > float64(p.Max) || math.IsInf(d, 0) || math.IsNaN(d)) {
		d = float64(p.Max)
	}
	if p.Jitter && d > 0 {
		r := p.rand
		if r == nil {
			r = rand.Float64
		}
		d = d/2 + r()*d/2
	}
	return time.Duration(d), true
}

// permanentError marks an error that must not be retried.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent wraps err so Retry returns it immediately. Retry unwraps it.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// Notify is called before each wait. It must not block.
type Notify func(attempt int, delay time.Duration, err error)

// Retry calls fn until it succeeds, returns a Permanent error, the Delayer
// gives up, maxElapsed (if positive) is exhausted or ctx is done. The last
// error of fn is returned; when ctx ends the wait, the result wraps both the
// last error and ctx.Err(), so errors.Is works for either.
func Retry(ctx context.Context, d Delayer, maxElapsed time.Duration, notify Notify, fn func(context.Context) error) error {
	start := time.Now()
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if pe, ok := errors.AsType[*permanentError](err); ok {
			return pe.err
		}
		if ctx.Err() != nil {
			return fmt.Errorf("%w (retry aborted: %w)", err, ctx.Err())
		}
		delay, ok := d.NextDelay(attempt)
		if !ok {
			return err
		}
		if maxElapsed > 0 && time.Since(start)+delay > maxElapsed {
			return err
		}
		if notify != nil {
			notify(attempt, delay, err)
		}
		if werr := Sleep(ctx, delay); werr != nil {
			return fmt.Errorf("%w (retry aborted: %w)", err, werr)
		}
	}
}

// Sleep waits for d or until ctx is done, whichever happens first. It
// returns ctx.Err() when interrupted. The timer is always released.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
