package balancer

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"go.uber.org/goleak"

	"github.com/jhonsferg/consulx/discovery"
	"github.com/jhonsferg/consulx/internal/fakeconsul"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func inst(id string, passing, warning int, status discovery.Status) discovery.ServiceInstance {
	return discovery.ServiceInstance{ID: id, Weights: discovery.Weights{Passing: passing, Warning: warning}, Status: status}
}

func TestRoundRobin(t *testing.T) {
	p := RoundRobin().NewPicker()
	list := []discovery.ServiceInstance{inst("a", 1, 1, "passing"), inst("b", 1, 1, "passing"), inst("c", 1, 1, "passing")}
	var got []string
	for range 6 {
		got = append(got, p.Pick(list).ID)
	}
	if want := "abcabc"; join(got) != want {
		t.Fatalf("got %s want %s", join(got), want)
	}
	// Independent state per picker (per service).
	if RoundRobin().NewPicker().Pick(list).ID != "a" {
		t.Fatal("pickers must not share state")
	}
}

func TestRandomCoversAll(t *testing.T) {
	p := Random().NewPicker()
	list := []discovery.ServiceInstance{inst("a", 1, 1, "passing"), inst("b", 1, 1, "passing")}
	seen := map[string]bool{}
	for range 200 {
		seen[p.Pick(list).ID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("seen %v", seen)
	}
}

func TestWeightedDistribution(t *testing.T) {
	p := Weighted().NewPicker()
	list := []discovery.ServiceInstance{
		inst("heavy", 9, 1, "passing"),
		inst("light", 1, 1, "passing"),
		inst("warn", 9, 0, "warning"), // warning weight 0: never picked
	}
	counts := map[string]int{}
	const n = 20000
	for range n {
		counts[p.Pick(list).ID]++
	}
	if counts["warn"] != 0 {
		t.Fatalf("zero-weight instance picked: %v", counts)
	}
	if ratio := float64(counts["heavy"]) / n; ratio < 0.85 || ratio > 0.95 {
		t.Fatalf("heavy ratio %.3f, want about 0.9 (%v)", ratio, counts)
	}
	// All weights zero: uniform fallback instead of never answering.
	zero := []discovery.ServiceInstance{inst("x", 0, 0, "warning"), inst("y", 0, 0, "warning")}
	if id := p.Pick(zero).ID; id != "x" && id != "y" {
		t.Fatal(id)
	}
}

func TestPickersAreConcurrencySafe(t *testing.T) {
	list := []discovery.ServiceInstance{inst("a", 1, 1, "passing"), inst("b", 2, 1, "passing")}
	for _, s := range []Strategy{RoundRobin(), Random(), Weighted()} {
		p := s.NewPicker()
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 1000 {
					_ = p.Pick(list)
				}
			})
		}
		wg.Wait()
	}
}

func healthEntry(id string) *api.ServiceEntry {
	return &api.ServiceEntry{
		Node:    &api.Node{Node: "n", Address: "10.0.0.1"},
		Service: &api.AgentService{ID: id, Service: "payments", Port: 80, Tags: []string{"v2"}},
		Checks:  api.HealthChecks{{Status: api.HealthPassing}},
	}
}

func setup(t *testing.T) (*fakeconsul.Agent, *discovery.Client) {
	t.Helper()
	a := fakeconsul.New("1.22.7")
	t.Cleanup(a.Close)
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		t.Fatal(err)
	}
	return a, discovery.New(raw, discovery.Config{MinInterval: time.Millisecond, WaitTime: time.Second})
}

func TestBalancerFollowsCatalog(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"), healthEntry("p2"))
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()

	first, err := b.Next(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := b.Next(t.Context(), "payments")
	if first.ID == second.ID {
		t.Fatal("round robin repeated an instance")
	}

	a.SetHealth("payments", healthEntry("p3"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		i, err := b.Next(t.Context(), "payments")
		if err == nil && i.ID == "p3" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("balancer did not follow the catalog")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(a.HealthQueries()); n > 10 {
		t.Fatalf("Next must not query Consul each time: %d queries", n)
	}
}

func TestBalancerNoInstancesAndCustomQuery(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"))
	b := New(t.Context(), d, Random(), WithQuery(func(d *discovery.Client, s string) discovery.Query {
		return d.Service(s).Tag("v3") // no instance has v3
	}))
	defer b.Close()
	if _, err := b.Next(t.Context(), "payments"); !errors.Is(err, discovery.ErrServiceNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestBalancerNextHonoursContext(t *testing.T) {
	a, d := setup(t)
	a.SetFailing(500) // never ready
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := b.Next(ctx, "payments"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestBalancerClose(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"))
	b := New(t.Context(), d, RoundRobin())
	if _, err := b.Next(t.Context(), "payments"); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Next(t.Context(), "payments"); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func BenchmarkNext(b *testing.B) {
	a := fakeconsul.New("1.22.7")
	defer a.Close()
	a.SetHealth("payments", healthEntry("p1"), healthEntry("p2"), healthEntry("p3"))
	raw, _ := api.NewClient(&api.Config{Address: a.URL()})
	lb := New(context.Background(), discovery.New(raw, discovery.Config{}), RoundRobin())
	defer lb.Close()
	ctx := context.Background()
	if _, err := lb.Next(ctx, "payments"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = lb.Next(ctx, "payments")
	}
}

func join(s []string) string {
	out := ""
	for _, x := range s {
		out += x
	}
	return out
}

func TestStaleGraceBridgesEmptyLists(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"))
	now := time.Unix(1000, 0)
	b := New(t.Context(), d, RoundRobin(), WithStaleGrace(10*time.Second))
	b.now = func() time.Time { return now }
	defer b.Close()
	if _, err := b.Next(t.Context(), "payments"); err != nil {
		t.Fatal(err)
	}

	// The list empties (for example a restarted agent reports critical).
	a.SetHealth("payments")
	deadline := time.Now().Add(5 * time.Second)
	for {
		e, _ := b.entry("payments", b.now())
		if len(e.watch.Instances()) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watch did not observe the empty list")
		}
		time.Sleep(5 * time.Millisecond)
	}
	now = now.Add(5 * time.Second)
	if inst, err := b.Next(t.Context(), "payments"); err != nil || inst.ID != "p1" {
		t.Fatalf("within grace the last instances must be served: %+v %v", inst, err)
	}
	now = now.Add(6 * time.Second)
	if _, err := b.Next(t.Context(), "payments"); !errors.Is(err, discovery.ErrServiceNotFound) {
		t.Fatalf("after the grace period the empty list must win: %v", err)
	}
}

func TestNoGraceByDefault(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"))
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()
	if _, err := b.Next(t.Context(), "payments"); err != nil {
		t.Fatal(err)
	}
	a.SetHealth("payments")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := b.Next(t.Context(), "payments"); errors.Is(err, discovery.ErrServiceNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("without grace an empty list must yield ErrServiceNotFound")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Services asked about once must not keep a watch (a goroutine and a
// connection) forever: callers that build service names dynamically would
// otherwise grow without bound.
func TestIdleServicesAreReleased(t *testing.T) {
	a, d := setup(t)
	for _, s := range []string{"s1", "s2", "s3"} {
		a.SetHealth(s, healthEntry(s+"-1"))
	}
	b := New(t.Context(), d, RoundRobin(), WithIdleTimeout(100*time.Millisecond))
	defer b.Close()
	before := runtime.NumGoroutine()
	for _, s := range []string{"s1", "s2", "s3"} {
		if _, err := b.Next(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.watched() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("idle services not released: %d left", b.watched())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grown := runtime.NumGoroutine() - before; grown > 1 { // the janitor itself
		t.Fatalf("released watches left %d goroutines", grown)
	}
	// A released service is watched again on demand.
	if inst, err := b.Next(t.Context(), "s2"); err != nil || inst.ID != "s2-1" {
		t.Fatalf("service must be watched again after release: %+v %v", inst, err)
	}
}

func TestBusyServicesAreKept(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("hot", healthEntry("hot-1"))
	b := New(t.Context(), d, RoundRobin(), WithIdleTimeout(100*time.Millisecond))
	defer b.Close()
	end := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(end) {
		if _, err := b.Next(t.Context(), "hot"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.watched() != 1 {
		t.Fatal("a service in use must keep its watch")
	}
}

func BenchmarkNext20Instances(b *testing.B) {
	a := fakeconsul.New("1.22.7")
	defer a.Close()
	entries := make([]*api.ServiceEntry, 20)
	for i := range entries {
		e := healthEntry(fmt.Sprintf("p%02d", i))
		e.Service.Meta = map[string]string{"version": "2", "zone": "a"}
		e.Service.Tags = []string{"v2", "blue"}
		entries[i] = e
	}
	a.SetHealth("payments", entries...)
	raw, _ := api.NewClient(&api.Config{Address: a.URL()})
	lb := New(context.Background(), discovery.New(raw, discovery.Config{}), RoundRobin())
	defer lb.Close()
	ctx := context.Background()
	if _, err := lb.Next(ctx, "payments"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = lb.Next(ctx, "payments")
	}
}

func TestNextReturnsIndependentCopies(t *testing.T) {
	a, d := setup(t)
	e := healthEntry("p1")
	e.Service.Meta = map[string]string{"version": "2"}
	a.SetHealth("payments", e)
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()
	first, err := b.Next(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	first.Meta["version"] = "mutated"
	first.Tags[0] = "mutated"
	second, _ := b.Next(t.Context(), "payments")
	if second.Meta["version"] != "2" || second.Tags[0] != "v2" {
		t.Fatalf("Next must return copies, got %+v", second)
	}
}

// BenchmarkNextStaleGrace uses the same instances as BenchmarkNext with the
// stale grace consulx.Client.Balancer applies by default, so the difference
// between the two is exactly what the grace bookkeeping costs per call.
func BenchmarkNextStaleGrace(b *testing.B) {
	lb := benchBalancer(b, true)
	defer lb.Close()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = lb.Next(ctx, "payments")
	}
}

// BenchmarkNextParallel measures Next under concurrent load, the way an
// HTTP handler using the balancer calls it. It reports how much the shared
// bookkeeping of the watch and the entry costs when many goroutines pick
// instances of the same service at once.
func BenchmarkNextParallel(b *testing.B) {
	b.Run("grace", func(b *testing.B) { parallelNext(b, true) })
	b.Run("nograce", func(b *testing.B) { parallelNext(b, false) })
}

func parallelNext(b *testing.B, grace bool) {
	lb := benchBalancer(b, grace)
	defer lb.Close()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			_, _ = lb.Next(ctx, "payments")
		}
	})
}

// benchBalancer starts a fake agent with three instances and returns a
// balancer whose watch is already ready.
func benchBalancer(b *testing.B, grace bool) *Balancer {
	b.Helper()
	a := fakeconsul.New("1.22.7")
	b.Cleanup(a.Close)
	a.SetHealth("payments", healthEntry("p1"), healthEntry("p2"), healthEntry("p3"))
	raw, _ := api.NewClient(&api.Config{Address: a.URL()})
	var opts []Option
	if grace {
		opts = append(opts, WithStaleGrace(10*time.Second))
	}
	lb := New(context.Background(), discovery.New(raw, discovery.Config{}), RoundRobin(), opts...)
	b.Cleanup(func() { _ = lb.Close() })
	if _, err := lb.Next(context.Background(), "payments"); err != nil {
		b.Fatal(err)
	}
	return lb
}
