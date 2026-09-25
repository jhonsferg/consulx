package discovery

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
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

	mu      sync.RWMutex
	current []ServiceInstance
	ready   chan struct{}
	once    sync.Once
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
func (w *Watch) Instances() []ServiceInstance {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return cloneAll(w.current)
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

		if haveState && reflect.DeepEqual(instances, lastSent) {
			continue // the index moved but nothing we expose changed
		}
		haveState = true
		w.mu.Lock()
		w.current = instances
		w.mu.Unlock()
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
	old := make(map[string]ServiceInstance, len(prev))
	for _, i := range prev {
		old[key(i)] = i
	}
	for _, i := range cur {
		p, ok := old[key(i)]
		switch {
		case !ok:
			ev.Added = append(ev.Added, i.clone())
		case !reflect.DeepEqual(p, i):
			ev.Changed = append(ev.Changed, i.clone())
		}
		delete(old, key(i))
	}
	for _, i := range prev {
		if _, gone := old[key(i)]; gone {
			ev.Removed = append(ev.Removed, i.clone())
		}
	}
	return ev
}

// key identifies an instance across nodes (IDs are unique per node only).
func key(i ServiceInstance) string { return i.Node.Name + "/" + i.ID }

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
