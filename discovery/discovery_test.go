package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"go.uber.org/goleak"

	"github.com/jhonsferg/consulx/internal/fakeconsul"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func entry(id, addr string, port int, status string, tags ...string) *api.ServiceEntry {
	return &api.ServiceEntry{
		Node: &api.Node{ID: "n-" + id, Node: "node-" + id, Address: "10.0.1." + id[len(id)-1:], Datacenter: "dc1"},
		Service: &api.AgentService{
			ID: id, Service: "payments", Address: addr, Port: port, Tags: tags,
			Meta:    map[string]string{"secure": "true", "version": "2"},
			Weights: api.AgentWeights{Passing: 3, Warning: 1},
		},
		Checks: api.HealthChecks{{CheckID: "service:" + id, Status: status, ServiceID: id, Type: "http"}},
	}
}

func setup(t *testing.T) (*fakeconsul.Agent, *Client) {
	t.Helper()
	a := fakeconsul.New("1.22.7")
	t.Cleanup(a.Close)
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		t.Fatal(err)
	}
	return a, New(raw, Config{MinInterval: time.Millisecond, Retry: fixedDelay(10 * time.Millisecond), WaitTime: time.Second})
}

func TestAllPassingByDefault(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments",
		entry("p2", "10.0.0.2", 8080, api.HealthPassing, "v2"),
		entry("p1", "", 8080, api.HealthPassing, "v1"),
		entry("p3", "10.0.0.3", 8080, api.HealthCritical, "v2"),
	)
	got, err := c.Service("payments").All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "p1" || got[1].ID != "p2" {
		t.Fatalf("passing instances sorted by ID expected, got %+v", got)
	}
	p1 := got[0]
	if p1.Address != "10.0.1.1" {
		t.Errorf("empty service address must fall back to node address: %q", p1.Address)
	}
	if p1.Scheme != "https" || p1.URL() != "https://10.0.1.1:8080" || p1.Weight() != 3 || p1.Status != StatusPassing {
		t.Errorf("instance %+v", p1)
	}
	if p1.Node.Name != "node-p1" || p1.Datacenter != "dc1" || p1.Meta["version"] != "2" || len(p1.Checks) != 1 {
		t.Errorf("instance details %+v", p1)
	}

	all, err := c.Service("payments").AnyStatus().All(t.Context())
	if err != nil || len(all) != 3 {
		t.Fatalf("AnyStatus: %d %v", len(all), err)
	}
}

func TestTagsAndQueryOptions(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing, "v1"), entry("p2", "10.0.0.2", 1, api.HealthPassing, "v2", "blue"))
	got, err := c.Service("payments").Tag("v2", "blue").Datacenter("dc2").Near("_agent").
		Consistency(Stale).Meta("version", "2").Filter(`Service.Port > 0`).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "p2" {
		t.Fatalf("got %+v", got)
	}
	q := a.HealthQueries()[0]
	if q.Get("dc") != "dc2" || q.Get("near") != "_agent" || !q.Has("stale") || !q.Has("passing") {
		t.Fatalf("query options not sent: %v", q)
	}
	if want := `Service.Meta["version"] == "2" and (Service.Port > 0)`; q.Get("filter") != want {
		t.Fatalf("filter %q, want %q", q.Get("filter"), want)
	}
}

func TestQueryIsImmutable(t *testing.T) {
	_, c := setup(t)
	base := c.Service("payments").Tag("a")
	x := base.Tag("x").Meta("k", "1")
	y := base.Tag("y")
	if len(base.tags) != 1 || base.meta != nil || x.tags[1] != "x" || y.tags[1] != "y" || y.meta != nil {
		t.Fatalf("builders share state: base=%v x=%v y=%v", base.tags, x.tags, y.tags)
	}
}

func TestFirstAndNotFound(t *testing.T) {
	a, c := setup(t)
	if _, err := c.Service("payments").First(t.Context()); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("got %v", err)
	}
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	inst, err := c.Service("payments").First(t.Context())
	if err != nil || inst.ID != "p1" {
		t.Fatalf("%+v %v", inst, err)
	}
	if _, err := c.Service("").All(t.Context()); err == nil {
		t.Fatal("empty name must fail")
	}
}

func TestErrorsAreWrapped(t *testing.T) {
	a, c := setup(t)
	a.SetFailing(http.StatusInternalServerError)
	_, err := c.Service("payments").All(t.Context())
	var se api.StatusError
	if !errors.As(err, &se) || se.Code != 500 {
		t.Fatalf("got %v", err)
	}
}

func TestReturnedInstancesAreCopies(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	w, err := c.Watch(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	<-w.Ready()
	got := w.Instances()
	got[0].Meta["version"] = "mutated"
	if w.Instances()[0].Meta["version"] != "2" {
		t.Fatal("Instances exposes internal state")
	}
}

func recv(t *testing.T, w *Watch) Event {
	t.Helper()
	select {
	case ev, ok := <-w.Events():
		if !ok {
			t.Fatal("events closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func TestWatchDeliversChanges(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	w, err := c.Watch(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ev := recv(t, w)
	if len(ev.Instances) != 1 || len(ev.Added) != 1 {
		t.Fatalf("initial event %+v", ev)
	}

	// An index bump without a data change produces no event.
	a.BumpIndex()
	select {
	case ev := <-w.Events():
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	a.SetHealth("payments", entry("p1", "10.0.0.9", 1, api.HealthPassing), entry("p2", "10.0.0.2", 1, api.HealthPassing))
	ev = recv(t, w)
	if len(ev.Instances) != 2 || len(ev.Added) != 1 || ev.Added[0].ID != "p2" || len(ev.Changed) != 1 || ev.Changed[0].Address != "10.0.0.9" {
		t.Fatalf("change event %+v", ev)
	}

	a.SetHealth("payments", entry("p2", "10.0.0.2", 1, api.HealthPassing), entry("p1", "10.0.0.9", 1, api.HealthCritical))
	ev = recv(t, w)
	if len(ev.Instances) != 1 || len(ev.Removed) != 1 || ev.Removed[0].ID != "p1" {
		t.Fatalf("an instance turning critical must be removed from a passing watch: %+v", ev)
	}
}

func TestWatchCoalescesForSlowConsumers(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	w, err := c.Watch(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	recv(t, w) // consumer saw {p1}

	// Several changes while the consumer is not reading.
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing), entry("p2", "10.0.0.2", 1, api.HealthPassing))
	time.Sleep(50 * time.Millisecond)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing), entry("p2", "10.0.0.2", 1, api.HealthPassing), entry("p3", "10.0.0.3", 1, api.HealthPassing))
	time.Sleep(50 * time.Millisecond)
	a.SetHealth("payments", entry("p3", "10.0.0.3", 1, api.HealthPassing))
	eventuallyTrue(t, func() bool {
		cur := w.Instances()
		return len(cur) == 1 && cur[0].ID == "p3"
	})

	// Exactly one pending event: the latest state, diffed against {p1}.
	last := recv(t, w)
	if len(last.Instances) != 1 || last.Instances[0].ID != "p3" {
		t.Fatalf("latest state not delivered: %+v", last)
	}
	if len(last.Added) != 1 || last.Added[0].ID != "p3" || len(last.Removed) != 1 || last.Removed[0].ID != "p1" {
		t.Fatalf("diff must be against the consumer's view: %+v", last)
	}
	select {
	case ev := <-w.Events():
		t.Fatalf("backlog delivered: %+v", ev)
	default:
	}
}

func TestWatchRetriesAndReportsErrors(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	a.SetFailing(http.StatusInternalServerError)
	w, err := c.Watch(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	select {
	case err := <-w.Errors():
		if err == nil {
			t.Fatal("nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no error reported")
	}
	a.SetFailing(0)
	if ev := recv(t, w); len(ev.Instances) != 1 {
		t.Fatalf("no recovery: %+v", ev)
	}
}

func TestWatchCloseAndContextCancel(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	w, err := c.Watch(t.Context(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	recv(t, w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-w.Events(); ok {
		t.Fatal("events must be closed")
	}
	if _, ok := <-w.Errors(); ok {
		t.Fatal("errors must be closed")
	}
	_ = w.Close() // idempotent

	ctx, cancel := contextWithCancel(t)
	w2, _ := c.Watch(ctx, "payments")
	cancel()
	select {
	case <-w2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop on context cancellation")
	}
}

func TestConcurrentQueries(t *testing.T) {
	a, c := setup(t)
	a.SetHealth("payments", entry("p1", "10.0.0.1", 1, api.HealthPassing))
	q := c.Service("payments")
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := q.Tag("x").AnyStatus().All(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func BenchmarkFromEntry(b *testing.B) {
	e := entry("p1", "10.0.0.1", 8080, api.HealthPassing, "v1", "blue")
	b.ReportAllocs()
	for b.Loop() {
		_ = fromEntry(e)
	}
}

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithCancel(t.Context())
}

func eventuallyTrue(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func BenchmarkWatchPick(b *testing.B) {
	a := fakeconsul.New("1.22.7")
	defer a.Close()
	entries := make([]*api.ServiceEntry, 20)
	for i := range entries {
		entries[i] = entry(fmt.Sprintf("p%02d", i), "10.0.0.1", 80, api.HealthPassing, "v1")
	}
	a.SetHealth("payments", entries...)
	raw, _ := api.NewClient(&api.Config{Address: a.URL()})
	w, err := New(raw, Config{}).Watch(context.Background(), "payments")
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	<-w.Ready()
	pick := func(l []ServiceInstance) (ServiceInstance, bool) { return l[0], len(l) > 0 }
	b.ReportAllocs()
	for b.Loop() {
		_, _ = w.Pick(pick)
	}
}
