// Package balancer implements client-side load balancing over instances
// discovered in Consul.
//
// A Balancer keeps one discovery watch per service (created on first use),
// so Next is a memory read, not a request to Consul. Strategies are small
// and pluggable: implement Strategy to add your own.
//
//	lb := consul.Balancer(balancer.RoundRobin())
//	inst, err := lb.Next(ctx, "payments")
package balancer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jhonsferg/consulx/discovery"
)

// LoadBalancer picks an instance of a service.
type LoadBalancer interface {
	Next(ctx context.Context, service string) (discovery.ServiceInstance, error)
}

// Picker selects one instance. instances is never empty and must not be
// modified. Implementations must be safe for concurrent use.
type Picker interface {
	Pick(instances []discovery.ServiceInstance) discovery.ServiceInstance
}

// Strategy creates the Picker used for one service, so stateful strategies
// such as round robin keep independent state per service.
type Strategy interface {
	NewPicker() Picker
}

// StrategyFunc adapts a function to Strategy.
type StrategyFunc func() Picker

// NewPicker implements Strategy.
func (f StrategyFunc) NewPicker() Picker { return f() }

// PickerFunc adapts a function to Picker.
type PickerFunc func([]discovery.ServiceInstance) discovery.ServiceInstance

// Pick implements Picker.
func (f PickerFunc) Pick(i []discovery.ServiceInstance) discovery.ServiceInstance { return f(i) }

// indexPicker is implemented by the built-in pickers. Selecting an index
// instead of an instance lets NextEndpoint return the endpoint formatted in
// advance, without copying the instance.
type indexPicker interface {
	pickIndex(list []discovery.ServiceInstance) int
}

// RoundRobin cycles through instances in a stable order (sorted by ID).
func RoundRobin() Strategy {
	return StrategyFunc(func() Picker { return new(roundRobin) })
}

type roundRobin struct{ n atomic.Uint64 }

func (p *roundRobin) pickIndex(list []discovery.ServiceInstance) int {
	return int((p.n.Add(1) - 1) % uint64(len(list))) // #nosec G115 -- the modulo is below len(list)
}

// Pick implements Picker.
func (p *roundRobin) Pick(list []discovery.ServiceInstance) discovery.ServiceInstance {
	return list[p.pickIndex(list)]
}

// Random picks a uniformly random instance.
func Random() Strategy {
	return StrategyFunc(func() Picker { return random{} })
}

type random struct{}

func (random) pickIndex(list []discovery.ServiceInstance) int {
	return rand.IntN(len(list)) // #nosec G404 -- load balancing needs no cryptographic randomness
}

// Pick implements Picker.
func (p random) Pick(list []discovery.ServiceInstance) discovery.ServiceInstance {
	return list[p.pickIndex(list)]
}

// Weighted picks randomly in proportion to ServiceInstance.Weight: the
// Consul "Passing" weight, or the "Warning" weight for instances in warning.
// Instances with weight 0 are skipped unless every weight is 0.
func Weighted() Strategy {
	return StrategyFunc(func() Picker { return weighted{} })
}

type weighted struct{}

// pickIndex walks the list by index: ranging by value would copy every
// instance, which dominated the cost with many instances.
func (weighted) pickIndex(list []discovery.ServiceInstance) int {
	total := 0
	for k := range list {
		total += max(list[k].Weight(), 0)
	}
	if total == 0 {
		return rand.IntN(len(list)) // #nosec G404 -- load balancing needs no cryptographic randomness
	}
	r := rand.IntN(total) // #nosec G404 -- load balancing needs no cryptographic randomness
	for k := range list {
		if r -= max(list[k].Weight(), 0); r < 0 {
			return k
		}
	}
	return len(list) - 1
}

// Pick implements Picker.
func (p weighted) Pick(list []discovery.ServiceInstance) discovery.ServiceInstance {
	return list[p.pickIndex(list)]
}

// ErrClosed is returned by Next after Close.
var ErrClosed = errors.New("consulx: balancer closed")

// Option configures a Balancer.
type Option func(*Balancer)

// WithQuery customises the discovery query used for each service, for
// example to require a tag. The default is d.Service(service), which
// returns passing instances only.
func WithQuery(fn func(d *discovery.Client, service string) discovery.Query) Option {
	return func(b *Balancer) { b.query = fn }
}

// WithStaleGrace keeps serving the last non-empty instance list for up to d
// when the current list becomes empty. A restarted Consul agent reports its
// services critical until their checks run again, which empties the list
// for a few seconds although every instance is alive; the grace period
// bridges that gap. If the instances really are gone, calls fail with
// connection errors during the grace period instead of ErrServiceNotFound.
// Zero (the default) disables it.
func WithStaleGrace(d time.Duration) Option {
	return func(b *Balancer) { b.grace = d }
}

// DefaultIdleTimeout is how long a service may go without Next calls before
// its watch is released. Watching again later only costs waiting for the
// first response, while keeping every service ever asked about would grow
// without bound when service names are built dynamically.
const DefaultIdleTimeout = 15 * time.Minute

// WithIdleTimeout releases the watch of a service that has not been used for
// d; the next call for it starts a new watch. Zero keeps every watch until
// Close. Default DefaultIdleTimeout.
func WithIdleTimeout(d time.Duration) Option {
	return func(b *Balancer) { b.idle = d }
}

// Balancer implements LoadBalancer on top of discovery watches. It is safe
// for concurrent use.
type Balancer struct {
	ctx      context.Context
	cancel   context.CancelFunc
	d        *discovery.Client
	strategy Strategy
	query    func(*discovery.Client, string) discovery.Query
	grace    time.Duration
	now      func() time.Time
	idle     time.Duration

	janitor sync.Once
	wg      sync.WaitGroup // the janitor goroutine

	// services is an immutable map swapped as a whole, so Next reads it
	// without taking a lock: with many goroutines calling Next for the same
	// service, a shared mutex would serialise all of them. mu guards the
	// swaps (create, release, close) and is never taken on the hot path.
	mu       sync.Mutex
	services atomic.Pointer[map[string]*entry]
	closed   atomic.Bool
}

// entry is the state kept for one service.
type entry struct {
	watch  *discovery.Watch
	picker Picker

	// lastUsed is when Next last picked this service, as nanoseconds from
	// Balancer.now. Written on every call, read by the janitor.
	lastUsed atomic.Int64

	// Stale grace state. last holds the most recent non-empty snapshot,
	// stored only when the snapshot actually changes, and lastSeen is when a
	// non-empty list was last observed. Both are read and written with single
	// atomic operations, so the grace bookkeeping adds no lock to Next.
	last     atomic.Pointer[discovery.Snapshot]
	lastSeen atomic.Int64
}

// withGrace returns s, or the last non-empty snapshot while it is younger
// than grace. Snapshots are immutable, so keeping one is safe. Concurrent
// callers race benignly on last, whose last writer wins, just as the
// locking version did.
func (e *entry) withGrace(s discovery.Snapshot, grace time.Duration, now time.Time) discovery.Snapshot {
	if grace <= 0 {
		return s
	}
	if s.Len() > 0 {
		e.lastSeen.Store(now.UnixNano())
		if last := e.last.Load(); last == nil || !last.Same(s) {
			kept := s // copied here, so only a change allocates
			e.last.Store(&kept)
		}
		return s
	}
	last := e.last.Load()
	if last == nil || now.Sub(time.Unix(0, e.lastSeen.Load())) > grace {
		return s
	}
	return *last
}

// New creates a Balancer. Its watches stop when ctx is done or Close is
// called.
func New(ctx context.Context, d *discovery.Client, s Strategy, opts ...Option) *Balancer {
	ctx, cancel := context.WithCancel(ctx)
	b := &Balancer{
		ctx: ctx, cancel: cancel, d: d, strategy: s,
		query: func(d *discovery.Client, svc string) discovery.Query { return d.Service(svc) },
		now:   time.Now,
		idle:  DefaultIdleTimeout,
	}
	b.services.Store(&map[string]*entry{})
	for _, o := range opts {
		o(b)
	}
	return b
}

// Next returns an instance of service. The first call for a service starts
// its watch and waits (bounded by ctx) for the initial state. It returns
// discovery.ErrServiceNotFound when no instance is available. The instance
// is an independent copy; when only its address is needed, NextEndpoint
// avoids copying it.
func (b *Balancer) Next(ctx context.Context, service string) (discovery.ServiceInstance, error) {
	e, snap, err := b.current(ctx, service)
	if err != nil {
		return discovery.ServiceInstance{}, err
	}
	inst, ok := snap.Pick(func(list []discovery.ServiceInstance) (discovery.ServiceInstance, bool) {
		if len(list) == 0 {
			return discovery.ServiceInstance{}, false
		}
		return e.picker.Pick(list), true
	})
	if !ok {
		return discovery.ServiceInstance{}, fmt.Errorf("%w: %s", discovery.ErrServiceNotFound, service)
	}
	return inst, nil
}

// NextEndpoint is Next for callers that only need where to connect: it
// returns the address, port, scheme, "host:port" and URL of the selected
// instance, formatted when the watch received the state, so a call costs no
// allocation. It follows the same strategy, grace period and errors as Next.
func (b *Balancer) NextEndpoint(ctx context.Context, service string) (discovery.Endpoint, error) {
	e, snap, err := b.current(ctx, service)
	if err != nil {
		return discovery.Endpoint{}, err
	}
	// A custom picker may return an instance that is not in the list.
	var foreign discovery.ServiceInstance
	isForeign := false
	ep, ok := snap.PickEndpoint(func(list []discovery.ServiceInstance) (int, bool) {
		if len(list) == 0 {
			return 0, false
		}
		i, found := pickIndex(e.picker, list)
		if !found {
			foreign, isForeign = i.inst, true
		}
		return i.index, found
	})
	switch {
	case isForeign:
		return foreign.Endpoint(), nil
	case !ok:
		return discovery.Endpoint{}, fmt.Errorf("%w: %s", discovery.ErrServiceNotFound, service)
	}
	return ep, nil
}

// current returns the entry of service and the snapshot to pick from, with
// the stale grace applied, waiting for the first state of a new watch.
func (b *Balancer) current(ctx context.Context, service string) (*entry, discovery.Snapshot, error) {
	now := b.now() // one clock reading per call, shared by entry and grace
	for attempt := 0; ; attempt++ {
		e, err := b.entry(service, now)
		if err != nil {
			return nil, discovery.Snapshot{}, err
		}
		select {
		case <-e.watch.Ready():
			return e, e.withGrace(e.watch.Current(), b.grace, now), nil
		case <-ctx.Done():
			return nil, discovery.Snapshot{}, ctx.Err()
		case <-e.watch.Done():
			// Released as idle between entry and here: watch it again.
			if attempt == 0 && !b.isClosed() {
				continue
			}
			return nil, discovery.Snapshot{}, ErrClosed
		}
	}
}

// picked is the result of pickIndex: the index of the selected instance, or
// the instance itself when a custom picker returned one not in the list.
type picked struct {
	index int
	inst  discovery.ServiceInstance
}

// pickIndex selects with p and returns the index of the selection. Built-in
// pickers select by index directly; for others the returned instance is
// located in the list, and found is false when it is not there.
func pickIndex(p Picker, list []discovery.ServiceInstance) (res picked, found bool) {
	if ip, ok := p.(indexPicker); ok {
		return picked{index: ip.pickIndex(list)}, true
	}
	inst := p.Pick(list)
	for k := range list {
		if list[k].ID == inst.ID && list[k].Node.Name == inst.Node.Name &&
			list[k].Address == inst.Address && list[k].Port == inst.Port {
			return picked{index: k}, true
		}
	}
	return picked{inst: inst}, false
}

// entry returns the entry of service, starting its watch on first use. now is
// recorded as the last use, so the hot path reads the clock only once.
func (b *Balancer) entry(service string, now time.Time) (*entry, error) {
	if b.closed.Load() || b.ctx.Err() != nil {
		return nil, ErrClosed
	}
	if cur := b.services.Load(); cur != nil {
		if e, ok := (*cur)[service]; ok {
			e.lastUsed.Store(now.UnixNano())
			return e, nil
		}
	}
	// First use of this service: create the entry under the lock.
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed.Load() || b.ctx.Err() != nil {
		return nil, ErrClosed
	}
	var current map[string]*entry
	if cur := b.services.Load(); cur != nil {
		current = *cur
	}
	if e, ok := current[service]; ok { // created while we waited for the lock
		e.lastUsed.Store(now.UnixNano())
		return e, nil
	}
	w, err := b.query(b.d, service).Watch(b.ctx)
	if err != nil {
		return nil, err
	}
	e := &entry{watch: w, picker: b.strategy.NewPicker()}
	e.lastUsed.Store(now.UnixNano())
	next := make(map[string]*entry, len(current)+1)
	maps.Copy(next, current)
	next[service] = e
	b.services.Store(&next)
	if b.idle > 0 {
		b.janitor.Do(func() { b.wg.Go(b.releaseIdle) })
	}
	return e, nil
}

// releaseIdle is the janitor goroutine.
//
// Purpose: close the watches of services unused for the idle timeout.
// Exit condition: the balancer context ends (Close or parent context).
func (b *Balancer) releaseIdle() {
	ticker := time.NewTicker(max(b.idle/2, 10*time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
		}
		now := b.now().UnixNano()
		var idle []*entry
		b.mu.Lock()
		if cur := b.services.Load(); cur != nil {
			next := make(map[string]*entry, len(*cur))
			for name, e := range *cur {
				if now-e.lastUsed.Load() >= b.idle.Nanoseconds() {
					idle = append(idle, e)
					continue
				}
				next[name] = e
			}
			if len(idle) > 0 {
				b.services.Store(&next)
			}
		}
		b.mu.Unlock()
		for _, e := range idle {
			_ = e.watch.Close()
		}
	}
}

func (b *Balancer) isClosed() bool {
	return b.closed.Load() || b.ctx.Err() != nil
}

// watched returns the number of watched services (tests).
func (b *Balancer) watched() int {
	if cur := b.services.Load(); cur != nil {
		return len(*cur)
	}
	return 0
}

// Close stops every watch and waits for them to exit.
func (b *Balancer) Close() error {
	b.mu.Lock()
	b.closed.Store(true)
	b.cancel()
	var list []*entry
	if cur := b.services.Load(); cur != nil {
		list = make([]*entry, 0, len(*cur))
		for _, e := range *cur {
			list = append(list, e)
		}
	}
	b.mu.Unlock()
	for _, e := range list {
		_ = e.watch.Close()
	}
	b.wg.Wait()
	return nil
}

var _ LoadBalancer = (*Balancer)(nil)
