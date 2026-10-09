package consulx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func startRegistered(t *testing.T, opts ...Option) (*Client, func() []string) {
	t.Helper()
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a, opts...)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "registration", func() bool { return c.Registration().Registered })
	return c, a.Deregisters
}

func TestStopWaitsDrainDelayAfterDeregistering(t *testing.T) {
	const delay = 150 * time.Millisecond
	c, deregisters := startRegistered(t, WithDrainDelay(delay))

	start := time.Now()
	stop(t, c)
	if elapsed := time.Since(start); elapsed < delay {
		t.Fatalf("Stop returned after %v, before the %v drain delay", elapsed, delay)
	}
	if len(deregisters()) != 1 {
		t.Fatalf("the instance must be deregistered before draining, got %v", deregisters())
	}
}

func TestDrainDelayIsBoundedByStopContext(t *testing.T) {
	c, deregisters := startRegistered(t, WithDrainDelay(time.Minute))

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("running out of drain time is not an error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("drain ignored the stop context: %v", elapsed)
	}
	if len(deregisters()) != 1 {
		t.Fatalf("deregistrations %v", deregisters())
	}
}

func TestNoDrainDelayByDefault(t *testing.T) {
	c, _ := startRegistered(t)
	start := time.Now()
	stop(t, c)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Stop without DrainDelay took %v", elapsed)
	}
}

func TestWithDrainDelayRejectsNegative(t *testing.T) {
	_, err := New(WithConsulAddress("http://127.0.0.1:1"), WithLogger(nil), WithDrainDelay(-time.Second))
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Field != "Lifecycle.DrainDelay" {
		t.Fatalf("want a Lifecycle.DrainDelay ConfigError, got %v", err)
	}
}

func TestDrainDelayFromEnv(t *testing.T) {
	t.Setenv(EnvDrainDelay, "3s")
	c, errs := ConfigFromEnv()
	if errs != nil {
		t.Fatal(errs)
	}
	if c.Lifecycle.DrainDelay != 3*time.Second {
		t.Fatalf("DrainDelay = %v", c.Lifecycle.DrainDelay)
	}
}
