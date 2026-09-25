package consulx

import (
	"errors"
	"testing"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/balancer"
)

func TestDiscoveryAndBalancerThroughClient(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	a.SetHealth("payments", &api.ServiceEntry{
		Node:    &api.Node{Node: "n1", Address: "10.0.0.1"},
		Service: &api.AgentService{ID: "p1", Service: "payments", Port: 80},
		Checks:  api.HealthChecks{{Status: api.HealthPassing}},
	})
	var m countingMetrics
	c := agentClient(t, a, WithAutoRegister(false), WithMetrics(&m))
	first, second := c.Discovery(), c.Discovery()
	if first != second {
		t.Fatal("Discovery must return a shared client")
	}
	inst, err := c.Discovery().Service("payments").First(t.Context())
	if err != nil || inst.ID != "p1" {
		t.Fatalf("%+v %v", inst, err)
	}
	if _, err := c.Discovery().Service("missing").First(t.Context()); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("consulx.ErrServiceNotFound must match: %v", err)
	}
	if m.count(MetricDiscoveryRequestsTotal) != 2 {
		t.Fatalf("discovery requests not counted: %d", m.count(MetricDiscoveryRequestsTotal))
	}

	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	lb := c.Balancer(balancer.RoundRobin())
	if got, err := lb.Next(t.Context(), "payments"); err != nil || got.ID != "p1" {
		t.Fatalf("%+v %v", got, err)
	}
	// Stop must stop balancer watches (goleak in TestMain verifies no leak).
	stop(t, c)
	if _, err := lb.Next(t.Context(), "payments"); !errors.Is(err, balancer.ErrClosed) {
		t.Fatalf("balancer must be closed with the client: %v", err)
	}
}

func TestClientBalancerHasDefaultStaleGrace(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	a.SetHealth("payments", &api.ServiceEntry{
		Node:    &api.Node{Node: "n1", Address: "10.0.0.1"},
		Service: &api.AgentService{ID: "p1", Service: "payments", Port: 80},
		Checks:  api.HealthChecks{{Status: api.HealthPassing}},
	})
	c := agentClient(t, a, WithAutoRegister(false))
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)
	graceful := c.Balancer(balancer.RoundRobin())
	strict := c.Balancer(balancer.RoundRobin(), balancer.WithStaleGrace(0))
	for _, lb := range []*balancer.Balancer{graceful, strict} {
		if _, err := lb.Next(t.Context(), "payments"); err != nil {
			t.Fatal(err)
		}
	}

	a.SetHealth("payments") // every instance reported critical
	eventually(t, "strict balancer sees the empty list", func() bool {
		_, err := strict.Next(t.Context(), "payments")
		return errors.Is(err, ErrServiceNotFound)
	})
	if inst, err := graceful.Next(t.Context(), "payments"); err != nil || inst.ID != "p1" {
		t.Fatalf("default grace must keep serving the last instances: %+v %v", inst, err)
	}
}
