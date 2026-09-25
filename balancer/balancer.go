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
	"math/rand/v2"
	"sync"
	"sync/atomic"

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

// RoundRobin cycles through instances in a stable order (sorted by ID).
func RoundRobin() Strategy {
	return StrategyFunc(func() Picker {
		var n atomic.Uint64
		return PickerFunc(func(list []discovery.ServiceInstance) discovery.ServiceInstance {
			return list[(n.Add(1)-1)%uint64(len(list))]
		})
	})
}

// Random picks a uniformly random instance.
func Random() Strategy {
	return StrategyFunc(func() Picker {
		return PickerFunc(func(list []discovery.ServiceInstance) discovery.ServiceInstance {
			return list[rand.IntN(len(list))]
		})
	})
}

// Weighted picks randomly in proportion to ServiceInstance.Weight: the
// Consul "Passing" weight, or the "Warning" weight for instances in warning.
// Instances with weight 0 are skipped unless every weight is 0.
func Weighted() Strategy {
	return StrategyFunc(func() Picker {
		return PickerFunc(func(list []discovery.ServiceInstance) discovery.ServiceInstance {
			total := 0
			for _, i := range list {
				total += max(i.Weight(), 0)
			}
			if total == 0 {
				return list[rand.IntN(len(list))]
			}
			r := rand.IntN(total)
			for _, i := range list {
				if r -= max(i.Weight(), 0); r < 0 {
					return i
				}
			}
			return list[len(list)-1]
		})
	})
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

// Balancer implements LoadBalancer on top of discovery watches. It is safe
// for concurrent use.
type Balancer struct {
	ctx      context.Context
	cancel   context.CancelFunc
	d        *discovery.Client
	strategy Strategy
	query    func(*discovery.Client, string) discovery.Query

	mu       sync.Mutex
	services map[string]*entry
	closed   bool
}

type entry struct {
	watch  *discovery.Watch
	picker Picker
}

// New creates a Balancer. Its watches stop when ctx is done or Close is
// called.
func New(ctx context.Context, d *discovery.Client, s Strategy, opts ...Option) *Balancer {
	ctx, cancel := context.WithCancel(ctx)
	b := &Balancer{
		ctx: ctx, cancel: cancel, d: d, strategy: s,
		query:    func(d *discovery.Client, svc string) discovery.Query { return d.Service(svc) },
		services: map[string]*entry{},
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Next returns an instance of service. The first call for a service starts
// its watch and waits (bounded by ctx) for the initial state. It returns
// discovery.ErrServiceNotFound when no instance is available.
func (b *Balancer) Next(ctx context.Context, service string) (discovery.ServiceInstance, error) {
	e, err := b.entry(service)
	if err != nil {
		return discovery.ServiceInstance{}, err
	}
	select {
	case <-e.watch.Ready():
	case <-ctx.Done():
		return discovery.ServiceInstance{}, ctx.Err()
	case <-e.watch.Done():
		return discovery.ServiceInstance{}, ErrClosed
	}
	list := e.watch.Instances()
	if len(list) == 0 {
		return discovery.ServiceInstance{}, fmt.Errorf("%w: %s", discovery.ErrServiceNotFound, service)
	}
	return e.picker.Pick(list), nil
}

func (b *Balancer) entry(service string) (*entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.ctx.Err() != nil {
		return nil, ErrClosed
	}
	if e, ok := b.services[service]; ok {
		return e, nil
	}
	w, err := b.query(b.d, service).Watch(b.ctx)
	if err != nil {
		return nil, err
	}
	e := &entry{watch: w, picker: b.strategy.NewPicker()}
	b.services[service] = e
	return e, nil
}

// Close stops every watch and waits for them to exit.
func (b *Balancer) Close() error {
	b.mu.Lock()
	b.closed = true
	b.cancel()
	list := make([]*entry, 0, len(b.services))
	for _, e := range b.services {
		list = append(list, e)
	}
	b.mu.Unlock()
	for _, e := range list {
		_ = e.watch.Close()
	}
	return nil
}

var _ LoadBalancer = (*Balancer)(nil)
