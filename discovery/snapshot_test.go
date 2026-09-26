package discovery

import (
	"testing"

	"github.com/hashicorp/consul/api"
)

func TestZeroSnapshotIsEmpty(t *testing.T) {
	var s Snapshot
	if s.Len() != 0 || s.Instances() != nil || s.Endpoints() != nil {
		t.Fatal("the zero snapshot must be empty")
	}
	if _, ok := s.Pick(func(l []ServiceInstance) (ServiceInstance, bool) { return ServiceInstance{}, len(l) > 0 }); ok {
		t.Fatal("Pick on an empty snapshot")
	}
	if _, ok := s.PickEndpoint(func([]ServiceInstance) (int, bool) { return 0, true }); ok {
		t.Fatal("PickEndpoint must reject an index out of range")
	}
}

func TestSnapshotEndpoints(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments",
		entry("p1", "10.0.0.1", 8443, api.HealthPassing),
		entry("p2", "2001:db8::2", 8080, api.HealthPassing),
	)
	w, err := c.Watch(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.Ready()

	s := w.Current()
	if !s.Same(w.Current()) || s.Len() != 2 {
		t.Fatalf("snapshot %d", s.Len())
	}
	eps := s.Endpoints()
	if eps[0].URL != "https://10.0.0.1:8443" || eps[1].HostPort != "[2001:db8::2]:8080" || eps[1].Node != "node-p2" {
		t.Fatalf("endpoints %+v", eps)
	}
	eps[0].URL = "mutated"
	if s.Endpoints()[0].URL != "https://10.0.0.1:8443" {
		t.Fatal("Endpoints must return a copy")
	}
	fresh := s.Endpoints()
	for i, inst := range s.Instances() {
		if inst.Endpoint() != fresh[i] {
			t.Fatalf("Endpoint() and the precomputed endpoint differ: %+v", inst.Endpoint())
		}
	}

	ep, ok := s.PickEndpoint(func(l []ServiceInstance) (int, bool) { return 1, true })
	if !ok || ep.ID != "p2" {
		t.Fatalf("PickEndpoint %+v %v", ep, ok)
	}
	if n := testing.AllocsPerRun(100, func() {
		_, _ = w.Current().PickEndpoint(func([]ServiceInstance) (int, bool) { return 0, true })
	}); n != 0 {
		t.Fatalf("PickEndpoint allocates %.1f times", n)
	}
}
