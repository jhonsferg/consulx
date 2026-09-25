package consulx

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// idleClient needs no network: discovery-only, nothing to register.
func idleClient(t *testing.T) *Client {
	t.Helper()
	return newTestClient(t, WithAutoRegister(false))
}

func TestLifecycleStartStop(t *testing.T) {
	c := idleClient(t)
	if c.State() != StateIdle {
		t.Fatalf("state %s", c.State())
	}
	if err := c.Stop(t.Context()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("stop before start: %v", err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.State() != StateRunning {
		t.Fatalf("state %s", c.State())
	}
	if err := c.Start(t.Context()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second start: %v", err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done must be closed after Stop")
	}
	if _, open := <-c.Errors(); open {
		t.Fatal("Errors must be closed after Stop")
	}
	if c.State() != StateStopped {
		t.Fatalf("state %s", c.State())
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatalf("Stop must be idempotent: %v", err)
	}
	if err := c.Start(t.Context()); !errors.Is(err, ErrAlreadyStopped) {
		t.Fatalf("start after stop: %v", err)
	}
}

func TestStartContextDoesNotEndRuntime(t *testing.T) {
	c := idleClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-c.Done():
		t.Fatal("cancelling the Start context must not stop the runtime")
	case <-time.After(50 * time.Millisecond):
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	c := idleClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx) }()

	waitState(t, c, StateRunning)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run after cancellation must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	<-c.Done()
}

func TestRunReturnsWhenStoppedElsewhere(t *testing.T) {
	c := idleClient(t)
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(t.Context()) }()
	waitState(t, c, StateRunning)
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}

func TestConcurrentStop(t *testing.T) {
	c := idleClient(t)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := c.Stop(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	<-c.Done()
}

func TestStartFailureIsTerminal(t *testing.T) {
	// Auto-registration with port 0 cannot resolve a port.
	c := newTestClient(t, WithServiceName("a"), WithServer(&http.Server{Addr: ":0"}))
	if err := c.Start(t.Context()); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("got %v", err)
	}
	if c.State() != StateStopped {
		t.Fatalf("state %s", c.State())
	}
	<-c.Done()
	if err := c.Start(t.Context()); !errors.Is(err, ErrAlreadyStopped) {
		t.Fatalf("restart: %v", err)
	}
}

func TestStopMarksDraining(t *testing.T) {
	c := idleClient(t)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.Health().Ready(t.Context()).Status != "DOWN" {
		t.Fatal("readiness must be DOWN after Stop")
	}
}

func TestRuntimeTasksStopOnStop(t *testing.T) {
	c := idleClient(t)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	c.goRuntime("test", func(ctx context.Context) {
		<-ctx.Done()
		close(stopped)
	})
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Stop returned before the runtime task exited")
	}
}

func TestReportNeverBlocksNorPanicsAfterStop(t *testing.T) {
	var m countingMetrics
	c := newTestClient(t, WithAutoRegister(false), WithMetrics(&m))
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range errorsBuffer + 5 {
		c.report(errors.New("x"))
	}
	if got := m.count(MetricErrorsDroppedTotal); got != 5 {
		t.Fatalf("dropped %d", got)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.report(errors.New("after stop")) // must not panic
}

func TestStateString(t *testing.T) {
	if StateDegraded.String() != "degraded" || State(99).String() != "unknown" {
		t.Fatal("State.String")
	}
}

func waitState(t *testing.T, c *Client, s State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.State() != s {
		if time.Now().After(deadline) {
			t.Fatalf("state %s, want %s", c.State(), s)
		}
		time.Sleep(time.Millisecond)
	}
}

type countingMetrics struct {
	mu       sync.Mutex
	counters map[string]int
	gauges   map[string]float64
}

func (m *countingMetrics) IncCounter(name string, _ ...Label) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters == nil {
		m.counters = map[string]int{}
	}
	m.counters[name]++
}

func (m *countingMetrics) SetGauge(name string, v float64, _ ...Label) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauges == nil {
		m.gauges = map[string]float64{}
	}
	m.gauges[name] = v
}

func (m *countingMetrics) ObserveDuration(string, time.Duration, ...Label) {}

func (m *countingMetrics) count(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[name]
}
