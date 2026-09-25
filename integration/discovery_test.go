package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/balancer"
)

func TestDiscoveryWatchAndBalancer(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	instance := func(id string, port int, tags ...string) *consulx.Client {
		c, err := consulx.New(consulx.WithConsulAddress(a.addr), consulx.WithServiceName(name),
			consulx.WithServiceID(id), consulx.WithServiceAddress(serviceHost), consulx.WithServicePort(port),
			consulx.WithTags(tags...), consulx.WithHealth(consulx.HealthConfig{TTL: 3 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		return c
	}
	blue := instance(name+"-blue", 8081, "blue")
	green := instance(name+"-green", 8082, "green")
	defer func() { _ = green.Stop(t.Context()) }()

	client, err := consulx.New(consulx.WithConsulAddress(a.addr), consulx.WithAutoRegister(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Stop(t.Context()) }()
	d := client.Discovery()

	eventually(t, 20*time.Second, "both instances passing", func() bool {
		all, err := d.Service(name).All(t.Context())
		return err == nil && len(all) == 2
	})
	tagged, err := d.Service(name).Tag("green").All(t.Context())
	if err != nil || len(tagged) != 1 || tagged[0].ID != name+"-green" || tagged[0].Port != 8082 {
		t.Fatalf("tag filter: %+v %v", tagged, err)
	}
	first, err := d.Service(name).Consistency(0).First(t.Context())
	if err != nil || first.Address != serviceHost || first.Scheme != "http" {
		t.Fatalf("first: %+v %v", first, err)
	}
	if _, err := d.Service(name + "-missing").First(t.Context()); !errors.Is(err, consulx.ErrServiceNotFound) {
		t.Fatalf("missing service: %v", err)
	}

	w, err := d.Watch(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	select {
	case ev := <-w.Events():
		if len(ev.Instances) != 2 {
			t.Fatalf("initial event %+v", ev)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("no initial event")
	}

	lb := client.Balancer(balancer.RoundRobin())
	seen := map[string]bool{}
	for range 4 {
		inst, err := lb.Next(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		seen[inst.ID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("round robin must use both instances: %v", seen)
	}

	// Deregistering one instance is pushed to the watch.
	if err := blue.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-w.Events():
			if len(ev.Instances) == 1 && len(ev.Removed) == 1 && ev.Removed[0].ID == name+"-blue" {
				return
			}
		case <-deadline:
			t.Fatal("removal not delivered by the watch")
		}
	}
}
