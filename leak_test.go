package consulx

import (
	"fmt"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/balancer"
	"github.com/jhonsferg/consulx/kvconfig"
)

// TestNoLeakUnderChurn drives a complete Client through hundreds of
// re-registrations, instance changes, configuration reloads and balancer
// calls, and checks that goroutines and live heap return to their level
// after warm-up.
func TestNoLeakUnderChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("long-running leak test")
	}
	a := fakeAgent(t, "1.22.7")
	a.PutKV("config/orders-api/database/host", "db0")
	c := agentClient(t, a, WithHealth(HealthConfig{TTL: time.Second}))
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)
	type cfg struct {
		Database struct {
			Host string `consul:"host"`
		} `consul:"database"`
	}
	w, err := kvconfig.Watch[cfg](t.Context(), c.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	lb := c.Balancer(balancer.RoundRobin())

	cycle := func(i int) {
		entries := make([]*api.ServiceEntry, 1+i%5)
		for j := range entries {
			entries[j] = &api.ServiceEntry{
				Node:    &api.Node{Node: "n", Address: "10.0.0.1"},
				Service: &api.AgentService{ID: fmt.Sprintf("p%d", j), Service: "payments", Port: 80, Meta: map[string]string{"i": fmt.Sprint(i)}},
				Checks:  api.HealthChecks{{Status: api.HealthPassing}},
			}
		}
		a.SetHealth("payments", entries...)
		a.PutKV("config/orders-api/database/host", fmt.Sprintf("db%d", i))
		if i%10 == 0 {
			a.Forget() // forces a re-registration
		}
		for range 20 {
			_, _ = lb.Next(t.Context(), "payments")
		}
		time.Sleep(5 * time.Millisecond)
	}
	measure := func() (owned, total int, heap uint64) {
		time.Sleep(300 * time.Millisecond) // let watches settle
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ownedGoroutines(), runtime.NumGoroutine(), ms.HeapAlloc
	}

	for i := range 100 { // warm-up: caches, connection pools, first watches
		cycle(i)
	}
	o0, g0, h0 := measure()
	for i := 100; i < 600; i++ {
		cycle(i)
	}
	o1, g1, h1 := measure()
	t.Logf("after warm-up: %d consulx goroutines (%d total), %d KiB heap; after 500 more cycles: %d (%d total), %d KiB heap",
		o0, g0, h0/1024, o1, g1, h1/1024)
	if o1 != o0 {
		t.Errorf("consulx goroutines grew from %d to %d", o0, o1)
	}
	// Other goroutines belong to HTTP keep-alive connections (client and fake
	// agent side). The pool fills up to its per-host limit depending on
	// concurrency, then stays there: a plateau, not a leak.
	if g1 > g0+3*(runtime.GOMAXPROCS(0)+1) {
		t.Errorf("goroutines grew from %d to %d, beyond the connection pool bound", g0, g1)
	}
	if h1 > h0+512*1024 {
		t.Errorf("live heap grew by %d KiB over 500 cycles", (h1-h0)/1024)
	}
	if c.State() != StateRunning || !c.Registration().Registered {
		t.Fatalf("client not healthy after churn: %s", c.State())
	}
}

// ownedGoroutines counts goroutines running ConsulX code, excluding the test
// infrastructure (fake agent, tests) and idle HTTP connections.
func ownedGoroutines() int {
	var b strings.Builder
	_ = pprof.Lookup("goroutine").WriteTo(&b, 1)
	n := 0
	for _, block := range strings.Split(b.String(), "\n\n") {
		if !strings.Contains(block, "github.com/jhonsferg/consulx") ||
			strings.Contains(block, "internal/fakeconsul") || strings.Contains(block, "_test.go") {
			continue
		}
		var count int
		if _, err := fmt.Sscanf(block, "%d @", &count); err == nil {
			n += count
		}
	}
	return n
}
