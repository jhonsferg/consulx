package discovery

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jhonsferg/consulx/internal/blocking"
)

// Event is a new view of a service.
type Event struct {
	// Instances is the full current set of matching instances.
	Instances []ServiceInstance
	// Added, Removed and Changed are relative to the previous Event the
	// consumer received (not to intermediate states it never saw).
	Added   []ServiceInstance
	Removed []ServiceInstance
	Changed []ServiceInstance
}

// Watch follows a service with blocking queries. Events are coalesced: a
// slow consumer always receives the latest state and never a backlog, and
// the watch never blocks on it. Close it when done, or cancel the context
// given to Query.Watch.
type Watch struct {
	events chan Event
	errs   chan error
	cancel context.CancelFunc
	done   chan struct{}

	// current is the latest snapshot. It is published atomically instead of
	// under a read-write lock so Pick never contends on a shared lock line
	// with the other callers of the same watch.
	current atomic.Pointer[snapshot]
	ready   chan struct{}
	once    sync.Once
}

// snapshot is one immutable state: the instances and their endpoints,
// aligned by index. A new state is always a new snapshot.
type snapshot struct {
	list      []ServiceInstance
	endpoints []Endpoint
}

// newSnapshot formats the endpoints of list once, so handing one out later
// costs nothing.
func newSnapshot(list []ServiceInstance) *snapshot {
	eps := make([]Endpoint, len(list))
	for i := range list {
		eps[i] = list[i].Endpoint()
	}
	return &snapshot{list: list, endpoints: eps}
}

// Snapshot is one immutable state of a Watch. Obtaining it and reading it
// cost no allocation; only the copies it returns do. The zero value is an
// empty state.
type Snapshot struct{ s *snapshot }

// Len returns the number of instances.
func (s Snapshot) Len() int {
	if s.s == nil {
		return 0
	}
	return len(s.s.list)
}

// Same reports whether s and o are the same state.
func (s Snapshot) Same(o Snapshot) bool { return s.s == o.s }

func (s Snapshot) list() []ServiceInstance {
	if s.s == nil {
		return nil
	}
	return s.s.list
}

// Instances returns an independent copy of every instance.
func (s Snapshot) Instances() []ServiceInstance { return cloneAll(s.list()) }

// Pick calls pick with the instances and returns an independent copy of the
// instance it selects; ok is false when pick selects none. The slice is
// immutable: pick must not modify it, but may keep it. Only the selected
// instance is copied, so the cost does not grow with the number of
// instances.
func (s Snapshot) Pick(pick func([]ServiceInstance) (ServiceInstance, bool)) (ServiceInstance, bool) {
	inst, ok := pick(s.list())
	if !ok {
		return ServiceInstance{}, false
	}
	return inst.clone(), true
}

// PickEndpoint calls pick with the instances and returns the endpoint of
// the index it selects; ok is false when pick selects none or an index out
// of range. The slice is immutable: pick must not modify it. Endpoints are
// formatted when the state is received, so this costs no allocation.
func (s Snapshot) PickEndpoint(pick func([]ServiceInstance) (int, bool)) (Endpoint, bool) {
	i, ok := pick(s.list())
	if !ok || i < 0 || i >= s.Len() {
		return Endpoint{}, false
	}
	return s.s.endpoints[i], true
}

// Endpoints returns the endpoint of every instance, in the order of
// Instances. The endpoints are values, so the result is independent.
func (s Snapshot) Endpoints() []Endpoint {
	if s.s == nil {
		return nil
	}
	return slices.Clone(s.s.endpoints)
}

// Watch starts watching the query. The first Event carries the initial
// state. Retries follow the client's retry policy; failures are reported on
// Errors and the watch keeps going until closed.
func (q Query) Watch(ctx context.Context) (*Watch, error) {
	if q.name == "" {
		return nil, errNoName
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &Watch{
		events: make(chan Event, 1),
		errs:   make(chan error, 1),
		cancel: cancel,
		done:   make(chan struct{}),
		ready:  make(chan struct{}),
	}
	go w.run(ctx, q)
	return w, nil
}

// Events delivers state changes. It is closed when the watch stops.
func (w *Watch) Events() <-chan Event { return w.events }

// Errors delivers request failures. Only the latest undelivered error is
// kept. It is closed when the watch stops.
func (w *Watch) Errors() <-chan error { return w.errs }

// Done is closed when the watch has stopped.
func (w *Watch) Done() <-chan struct{} { return w.done }

// Close stops the watch and waits for its goroutine to exit.
func (w *Watch) Close() error {
	w.cancel()
	<-w.done
	return nil
}

// Instances returns the latest known instances, independently of Events.
func (w *Watch) Instances() []ServiceInstance { return w.Current().Instances() }

// Current returns the latest state, empty before the first response. It
// costs no allocation.
func (w *Watch) Current() Snapshot { return Snapshot{w.current.Load()} }

// Pick calls pick with the current instances and returns an independent
// copy of the instance it selects; ok is false when pick selects none. The
// slice is an immutable snapshot: pick must not modify it, but may keep it,
// because a newer state is published as a new slice. Unlike Instances, Pick
// copies only the selected instance, so its cost does not grow with the
// number of instances. The balancer uses it on every call.
func (w *Watch) Pick(pick func([]ServiceInstance) (ServiceInstance, bool)) (ServiceInstance, bool) {
	return w.Current().Pick(pick)
}

// Ready is closed once the first successful response arrived.
func (w *Watch) Ready() <-chan struct{} { return w.ready }

// run is the watch goroutine.
//
// Purpose: issue blocking queries following Consul's index rules, publish
// state changes, retry failures with backoff.
// Exit condition: its context is cancelled (Close or parent context).
func (w *Watch) run(ctx context.Context, q Query) {
	defer func() {
		close(w.events)
		close(w.errs)
		close(w.done)
	}()
	c := q.c
	limiter := blocking.NewLimiter(c.cfg.MinInterval, 2)
	var (
		index     uint64
		failures  int
		delivered []ServiceInstance // what the consumer has seen
		lastSent  []ServiceInstance // what is (or was) in the channel
		haveState bool
	)
	for {
		if limiter.Wait(ctx) != nil {
			return
		}
		// Bound each blocking request (see blocking.RequestTimeout).
		rctx, cancel := context.WithTimeout(ctx, blocking.RequestTimeout(c.cfg.WaitTime, c.cfg.RequestTimeout))
		opts := q.options(rctx)
		opts.WaitIndex = index
		opts.WaitTime = c.cfg.WaitTime
		instances, meta, err := q.fetch(opts)
		cancel()
		c.cfg.Observe("watch", err)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			w.publishErr(err)
			delay, ok := c.cfg.Retry.NextDelay(failures)
			if !ok {
				delay = c.cfg.WaitTime / 10
			}
			c.cfg.Logger.Warn("discovery watch failed, retry scheduled",
				slog.String("service", q.name), slog.Int("attempt", failures),
				slog.Duration("delay", delay), slog.Any("error", err))
			if sleep(ctx, delay) != nil {
				return
			}
			index = 0 // after an outage, resync from scratch
			continue
		}
		if failures > 0 {
			c.cfg.Logger.Info("discovery watch recovered", slog.String("service", q.name))
			failures = 0
		}
		index = blocking.NextIndex(index, meta.LastIndex)

		if haveState && slices.EqualFunc(instances, lastSent, equalInstance) {
			continue // the index moved but nothing we expose changed
		}
		haveState = true
		w.current.Store(newSnapshot(instances))
		w.once.Do(func() { close(w.ready) })

		// Coalesce: if the previous event is still unread, take it back and
		// diff against what the consumer actually saw.
		select {
		case <-w.events:
		default:
			delivered = lastSent
		}
		w.events <- diff(delivered, instances)
		lastSent = instances
	}
}

// publishErr keeps only the newest undelivered error.
func (w *Watch) publishErr(err error) {
	select {
	case <-w.errs:
	default:
	}
	w.errs <- err
}

// diff builds an event from the consumer's last view to the new state.
func diff(prev, cur []ServiceInstance) Event {
	ev := Event{Instances: cloneAll(cur)}
	old := make(map[instanceKey]int, len(prev)) // key -> index in prev
	for n, i := range prev {
		old[key(i)] = n
	}
	for _, i := range cur {
		k := key(i)
		n, ok := old[k]
		switch {
		case !ok:
			ev.Added = append(ev.Added, i.clone())
		case !equalInstance(prev[n], i):
			ev.Changed = append(ev.Changed, i.clone())
		}
		delete(old, k)
	}
	for _, i := range prev {
		if _, gone := old[key(i)]; gone {
			ev.Removed = append(ev.Removed, i.clone())
		}
	}
	return ev
}

// instanceKey identifies an instance across nodes (IDs are unique per node
// only). A struct key needs no string concatenation per lookup.
type instanceKey struct{ node, id string }

func key(i ServiceInstance) instanceKey { return instanceKey{i.Node.Name, i.ID} }

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
