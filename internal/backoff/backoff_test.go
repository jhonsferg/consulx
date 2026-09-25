package backoff

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPolicyNextDelayGrowsAndCaps(t *testing.T) {
	p := Policy{Initial: 100 * time.Millisecond, Max: time.Second, Multiplier: 2}
	want := []time.Duration{100, 200, 400, 800, 1000, 1000}
	for i, w := range want {
		got, ok := p.NextDelay(i + 1)
		if !ok {
			t.Fatalf("attempt %d: unexpected stop", i+1)
		}
		if got != w*time.Millisecond {
			t.Errorf("attempt %d: got %v, want %v", i+1, got, w*time.Millisecond)
		}
	}
}

func TestPolicyNextDelayHugeAttemptDoesNotOverflow(t *testing.T) {
	p := Policy{Initial: time.Second, Max: 30 * time.Second, Multiplier: 10}
	got, ok := p.NextDelay(10_000)
	if !ok || got != 30*time.Second {
		t.Fatalf("got %v %v, want 30s true", got, ok)
	}
}

func TestPolicyJitterStaysWithinHalfAndFull(t *testing.T) {
	for _, r := range []float64{0, 0.5, 0.999999} {
		p := Policy{Initial: time.Second, Max: time.Minute, Multiplier: 2, Jitter: true, rand: func() float64 { return r }}
		got, _ := p.NextDelay(1)
		if got < 500*time.Millisecond || got > time.Second {
			t.Errorf("rand=%v: delay %v outside [500ms, 1s]", r, got)
		}
	}
}

func TestPolicyMaxAttemptsCountsTotalAttempts(t *testing.T) {
	p := Policy{Initial: time.Millisecond, Multiplier: 1, MaxAttempts: 3}
	if _, ok := p.NextDelay(2); !ok {
		t.Fatal("attempt 2 must allow a retry")
	}
	if _, ok := p.NextDelay(3); ok {
		t.Fatal("attempt 3 must be the last one")
	}
}

func TestRetrySucceedsAfterFailures(t *testing.T) {
	calls := 0
	var notified []int
	err := Retry(t.Context(), Policy{Initial: time.Millisecond, Multiplier: 1}, 0,
		func(attempt int, _ time.Duration, _ error) { notified = append(notified, attempt) },
		func(context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("boom")
			}
			return nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 || len(notified) != 2 {
		t.Fatalf("calls=%d notified=%v", calls, notified)
	}
}

func TestRetryStopsOnPermanent(t *testing.T) {
	sentinel := errors.New("fatal")
	calls := 0
	err := Retry(t.Context(), Policy{Initial: time.Millisecond}, 0, nil, func(context.Context) error {
		calls++
		return Permanent(sentinel)
	})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if _, isPerm := errors.AsType[*permanentError](err); isPerm {
		t.Fatal("permanent wrapper must be removed")
	}
}

func TestRetryHonoursMaxAttempts(t *testing.T) {
	calls := 0
	err := Retry(t.Context(), Policy{Initial: time.Millisecond, MaxAttempts: 4}, 0, nil, func(context.Context) error {
		calls++
		return errors.New("boom")
	})
	if err == nil || calls != 4 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRetryHonoursMaxElapsed(t *testing.T) {
	calls := 0
	err := Retry(t.Context(), Policy{Initial: time.Hour}, time.Second, nil, func(context.Context) error {
		calls++
		return errors.New("boom")
	})
	if err == nil || calls != 1 {
		t.Fatalf("a delay beyond the budget must stop retries: err=%v calls=%d", err, calls)
	}
}

func TestRetryCancellationWrapsBothErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cause := errors.New("unreachable")
	done := make(chan error, 1)
	go func() {
		done <- Retry(ctx, Policy{Initial: time.Hour}, 0, nil, func(context.Context) error { return cause })
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v must wrap cause and context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Retry did not return after cancellation")
	}
}

func TestSleepReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
