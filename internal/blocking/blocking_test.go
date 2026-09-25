package blocking

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNextIndex(t *testing.T) {
	cases := []struct{ prev, got, want uint64 }{
		{0, 10, 10},
		{10, 12, 12},
		{12, 12, 12},
		{12, 5, 0}, // went backwards
		{0, 0, 1},  // never block on 0
		{5, 0, 0},  // backwards to 0: reset first
	}
	for _, c := range cases {
		if got := NextIndex(c.prev, c.got); got != c.want {
			t.Errorf("NextIndex(%d, %d) = %d, want %d", c.prev, c.got, got, c.want)
		}
	}
}

func TestLimiterAllowsBurstThenPaces(t *testing.T) {
	l := NewLimiter(time.Hour, 2)
	ctx := t.Context()
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third request within the interval must wait: %v", err)
	}
}

func TestLimiterRefills(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(time.Second, 2)
	l.now = func() time.Time { return now }
	for range 2 {
		if err := l.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Second) // one token back
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err != nil {
		t.Fatalf("refilled token not granted: %v", err)
	}
}

func TestLimiterDisabled(t *testing.T) {
	l := NewLimiter(0, 1)
	for range 100 {
		if err := l.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestTimeout(t *testing.T) {
	if got := RequestTimeout(16*time.Second, 10*time.Second); got != 27*time.Second {
		t.Fatalf("got %v", got)
	}
}
