package balancer

import (
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/discovery"
)

func TestNextEndpoint(t *testing.T) {
	a, d := setup(t)
	e := healthEntry("p1")
	e.Service.Address = "10.0.0.7"
	e.Service.Port = 8443
	e.Service.Meta = map[string]string{"secure": "true"}
	v6 := healthEntry("p2")
	v6.Service.Address = "2001:db8::1"
	a.SetHealth("payments", e, v6)
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()

	first, err := b.NextEndpoint(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	want := discovery.Endpoint{ID: "p1", Node: "n", Address: "10.0.0.7", Port: 8443, Scheme: "https",
		HostPort: "10.0.0.7:8443", URL: "https://10.0.0.7:8443"}
	if first != want {
		t.Fatalf("got %+v, want %+v", first, want)
	}
	second, _ := b.NextEndpoint(t.Context(), "payments")
	if second.HostPort != "[2001:db8::1]:80" || second.URL != "http://[2001:db8::1]:80" {
		t.Fatalf("IPv6 endpoint %+v", second)
	}
}

func TestNextEndpointSharesStateWithNext(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"), healthEntry("p2"))
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()
	inst, _ := b.Next(t.Context(), "payments")
	ep, _ := b.NextEndpoint(t.Context(), "payments")
	if inst.ID != "p1" || ep.ID != "p2" {
		t.Fatalf("both methods must advance the same round robin: %s then %s", inst.ID, ep.ID)
	}
}

func TestNextEndpointCustomPicker(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments", healthEntry("p1"), healthEntry("p2"))

	last := StrategyFunc(func() Picker {
		return PickerFunc(func(l []discovery.ServiceInstance) discovery.ServiceInstance { return l[len(l)-1] })
	})
	b := New(t.Context(), d, last)
	defer b.Close()
	if ep, err := b.NextEndpoint(t.Context(), "payments"); err != nil || ep.ID != "p2" {
		t.Fatalf("custom picker: %+v %v", ep, err)
	}

	// A picker may build an instance of its own; its endpoint is formatted
	// on the spot.
	made := StrategyFunc(func() Picker {
		return PickerFunc(func([]discovery.ServiceInstance) discovery.ServiceInstance {
			return discovery.ServiceInstance{ID: "external", Address: "192.0.2.1", Port: 9000, Scheme: "http"}
		})
	})
	b2 := New(t.Context(), d, made)
	defer b2.Close()
	if ep, err := b2.NextEndpoint(t.Context(), "payments"); err != nil || ep.URL != "http://192.0.2.1:9000" {
		t.Fatalf("foreign instance: %+v %v", ep, err)
	}
}

func TestNextEndpointNotFoundAndGrace(t *testing.T) {
	a, d := setup(t)
	a.SetHealth("payments")
	b := New(t.Context(), d, RoundRobin())
	defer b.Close()
	if _, err := b.NextEndpoint(t.Context(), "payments"); !errors.Is(err, discovery.ErrServiceNotFound) {
		t.Fatalf("got %v", err)
	}

	a.SetHealth("orders", healthEntry("o1"))
	g := New(t.Context(), d, RoundRobin(), WithStaleGrace(time.Minute))
	defer g.Close()
	if _, err := g.NextEndpoint(t.Context(), "orders"); err != nil {
		t.Fatal(err)
	}
	a.SetHealth("orders") // the list empties, as after an agent restart
	deadline := time.Now().Add(5 * time.Second)
	for {
		e, _ := g.entry("orders", g.now())
		if e.watch.Current().Len() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watch never saw the empty list")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ep, err := g.NextEndpoint(t.Context(), "orders"); err != nil || ep.ID != "o1" {
		t.Fatalf("grace must keep the last endpoint: %+v %v", ep, err)
	}
}

// TestNextEndpointDoesNotAllocate keeps the zero-allocation guarantee of
// NextEndpoint for every built-in strategy.
func TestNextEndpointDoesNotAllocate(t *testing.T) {
	a, d := setup(t)
	entries := make([]*api.ServiceEntry, 5)
	for i := range entries {
		entries[i] = healthEntry(string(rune('a' + i)))
		entries[i].Service.Meta = map[string]string{"version": "2"}
	}
	a.SetHealth("payments", entries...)
	for name, s := range map[string]Strategy{"round-robin": RoundRobin(), "random": Random(), "weighted": Weighted()} {
		b := New(t.Context(), d, s, WithStaleGrace(time.Minute))
		if _, err := b.NextEndpoint(t.Context(), "payments"); err != nil {
			t.Fatal(err)
		}
		allocs := testing.AllocsPerRun(100, func() { _, _ = b.NextEndpoint(t.Context(), "payments") })
		_ = b.Close()
		if allocs != 0 {
			t.Errorf("%s: NextEndpoint allocates %.1f times per call", name, allocs)
		}
	}
}
