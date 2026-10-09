package balancer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
)

// addrEntry is a passing instance with its own address, so instances are
// distinguishable by "host:port" (healthEntry shares the node address).
func addrEntry(id, addr string) *api.ServiceEntry {
	return &api.ServiceEntry{
		Node:    &api.Node{Node: "n", Address: "10.0.0.1"},
		Service: &api.AgentService{ID: id, Service: "payments", Address: addr, Port: 80},
		Checks:  api.HealthChecks{{Status: api.HealthPassing}},
	}
}

// fakeClock is a manually advanced clock for the balancer's now field.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func resolveN(t *testing.T, r *ServiceResolver, n int) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for range n {
		hp, err := r.Resolve(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		seen[hp]++
	}
	return seen
}

func TestEjectionSkipsReportedInstanceUntilItExpires(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", addrEntry("p1", "10.0.0.11"), addrEntry("p2", "10.0.0.12"), addrEntry("p3", "10.0.0.13"))
	clock := &fakeClock{t: time.Unix(1_000, 0)}
	b := New(t.Context(), d, RoundRobin(), WithEjection(5*time.Second))
	b.now = clock.now
	defer b.Close()
	r := b.For("payments")

	if seen := resolveN(t, r, 6); len(seen) != 3 {
		t.Fatalf("all instances expected before any failure, got %v", seen)
	}

	r.ReportFailure("10.0.0.12:80", errors.New("connection refused"))
	seen := resolveN(t, r, 30)
	if seen["10.0.0.12:80"] != 0 || len(seen) != 2 {
		t.Fatalf("ejected instance must be skipped, got %v", seen)
	}

	// NextEndpoint and Next honour the same ejections.
	for range 10 {
		if inst, err := b.Next(t.Context(), "payments"); err != nil || inst.ID == "p2" {
			t.Fatalf("Next returned %v err=%v", inst.ID, err)
		}
	}

	clock.add(5*time.Second + time.Millisecond)
	if seen := resolveN(t, r, 6); seen["10.0.0.12:80"] == 0 {
		t.Fatalf("instance must come back after the ejection window, got %v", seen)
	}
}

func TestEjectionFailsOpenWhenEveryInstanceIsEjected(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", addrEntry("p1", "10.0.0.11"), addrEntry("p2", "10.0.0.12"))
	b := New(t.Context(), d, RoundRobin(), WithEjection(time.Minute))
	defer b.Close()
	r := b.For("payments")
	if _, err := r.Resolve(t.Context()); err != nil { // start the watch
		t.Fatal(err)
	}

	r.ReportFailure("10.0.0.11:80", nil)
	r.ReportFailure("10.0.0.12:80", nil)
	if seen := resolveN(t, r, 4); len(seen) != 2 {
		t.Fatalf("with every instance ejected the full list must be used, got %v", seen)
	}
}

func TestEjectionDisabledByDefault(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", addrEntry("p1", "10.0.0.11"), addrEntry("p2", "10.0.0.12"))
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()
	r := b.For("payments")
	if _, err := r.Resolve(t.Context()); err != nil {
		t.Fatal(err)
	}

	r.ReportFailure("10.0.0.11:80", nil)
	if seen := resolveN(t, r, 4); seen["10.0.0.11:80"] != 2 {
		t.Fatalf("without WithEjection reports are ignored, got %v", seen)
	}
}

func TestReportFailureForUnknownServiceIsIgnored(t *testing.T) {
	_, d := setup(t)
	b := New(t.Context(), d, RoundRobin(), WithEjection(time.Second))
	defer b.Close()
	b.ReportFailure("never-watched", "10.0.0.1:80") // must not panic or start a watch
	if b.watched() != 0 {
		t.Fatal("ReportFailure must not start watches")
	}
}

func TestEjectionConcurrentReportsAndPicks(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", addrEntry("p1", "10.0.0.11"), addrEntry("p2", "10.0.0.12"), addrEntry("p3", "10.0.0.13"))
	b := New(t.Context(), d, RoundRobin(), WithEjection(10*time.Millisecond))
	defer b.Close()
	r := b.For("payments")
	if _, err := r.Resolve(t.Context()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			targets := []string{"10.0.0.11:80", "10.0.0.12:80", "10.0.0.13:80"}
			for ctx.Err() == nil {
				if i%2 == 0 {
					r.ReportFailure(targets[i%3], nil)
				} else if _, err := r.Resolve(ctx); err != nil && ctx.Err() == nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestServiceResolverReturnsNotFound(t *testing.T) {
	_, d := setup(t)
	b := New(t.Context(), d, RoundRobin(), WithEjection(time.Second))
	defer b.Close()
	r := b.For("payments")
	if r.Service() != "payments" {
		t.Fatalf("Service() = %q", r.Service())
	}
	if _, err := r.Resolve(t.Context()); err == nil {
		t.Fatal("expected an error with no instances")
	}
}
