package balancer

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jhonsferg/consulx/discovery"
)

// WithEjection enables passive outlier detection: an instance reported
// through ReportFailure is skipped by Next and NextEndpoint for d, or until
// the discovery watch drops it, whichever comes first.
//
// Consul only learns that an instance died when its next health check fails,
// which can take a whole check interval; during that window a balancer
// without ejection keeps handing out the dead instance to one call in N.
// With ejection, the first caller that fails to connect takes the instance
// out of rotation for everybody immediately.
//
// Ejection fails open: when every instance of a service is ejected, the
// full list is used again, so a burst of failures (a network blip, a
// client-side problem) can never turn into "no instance available".
//
// Zero (the default) disables ejection and makes ReportFailure a no-op.
func WithEjection(d time.Duration) Option {
	return func(b *Balancer) { b.eject = d }
}

// ejectionSet maps an instance "host:port" to the time (unix nanoseconds)
// its ejection ends. It is immutable once published: ReportFailure builds a
// new set and swaps it in, so readers on the hot path never lock.
type ejectionSet map[string]int64

// ejectionState is the per-service ejection bookkeeping embedded in entry.
type ejectionState struct {
	mu  sync.Mutex // serialises writers (ReportFailure); readers use set only
	set atomic.Pointer[ejectionSet]
}

// ReportFailure ejects the instance of service listening on hostPort (the
// "host:port" returned in Endpoint.HostPort) for the WithEjection duration.
// Report only failures that say the instance itself is unreachable, such as
// a refused or reset connection - not HTTP error statuses, and not the
// caller's own cancellations or deadlines.
//
// It is safe for concurrent use and never blocks on Consul. Reports for a
// service the balancer is not watching, or with ejection disabled, are
// ignored.
func (b *Balancer) ReportFailure(service, hostPort string) {
	if b.eject <= 0 || hostPort == "" {
		return
	}
	cur := b.services.Load()
	if cur == nil {
		return
	}
	e, ok := (*cur)[service]
	if !ok {
		return
	}
	now := b.now().UnixNano()
	until := now + b.eject.Nanoseconds()

	e.ejection.mu.Lock()
	defer e.ejection.mu.Unlock()
	next := ejectionSet{hostPort: until}
	if old := e.ejection.set.Load(); old != nil {
		for k, v := range *old {
			if v > now && k != hostPort { // drop expired entries while copying
				next[k] = v
			}
		}
	}
	e.ejection.set.Store(&next)
}

// available returns the indexes of the instances in list that are not
// ejected at now. It returns nil when nothing needs filtering - no active
// ejection, none of the listed instances ejected, or all of them ejected
// (fail open) - so callers keep using list as is without allocating.
func (s *ejectionState) available(list []discovery.ServiceInstance, now int64) []int {
	set := s.set.Load()
	if set == nil || len(*set) == 0 {
		return nil
	}
	keep := make([]int, 0, len(list))
	for k := range list {
		if until, ok := (*set)[list[k].HostPort()]; ok && until > now {
			continue
		}
		keep = append(keep, k)
	}
	if len(keep) == len(list) || len(keep) == 0 {
		return nil
	}
	return keep
}

// subset copies the instances at keep, in order.
func subset(list []discovery.ServiceInstance, keep []int) []discovery.ServiceInstance {
	out := make([]discovery.ServiceInstance, len(keep))
	for i, k := range keep {
		out[i] = list[k]
	}
	return out
}

// For returns a resolver bound to one service. Its method set matches the
// discovery seam of HTTP clients such as github.com/jhonsferg/relay
// (Resolver and FailureReporter) without consulx importing them:
//
//	lb := consul.Balancer(balancer.RoundRobin(), balancer.WithEjection(5*time.Second))
//	client := relay.New(
//	    relay.WithBaseURL("http://integrator"), // host replaced per attempt
//	    relay.WithDiscovery(lb.For("integrator")),
//	)
//
// Every request attempt then goes to the next healthy instance known to the
// watch (updated by Consul blocking queries, so instances added or removed
// are seen within milliseconds), and a refused connection ejects the
// instance for every caller at once.
func (b *Balancer) For(service string) *ServiceResolver {
	return &ServiceResolver{b: b, service: service}
}

// ServiceResolver resolves one service through a Balancer. Create it with
// Balancer.For. It is safe for concurrent use.
type ServiceResolver struct {
	b       *Balancer
	service string
}

// Service returns the name of the service this resolver selects from.
func (r *ServiceResolver) Service() string { return r.service }

// Resolve returns the "host:port" of the next instance, following the
// balancer's strategy, stale grace and ejections. It returns
// discovery.ErrServiceNotFound when no instance is available.
func (r *ServiceResolver) Resolve(ctx context.Context) (string, error) {
	ep, err := r.b.NextEndpoint(ctx, r.service)
	if err != nil {
		return "", err
	}
	return ep.HostPort, nil
}

// ReportFailure ejects target ("host:port", as returned by Resolve). err is
// accepted for interface compatibility and not inspected: the caller decides
// what counts as an instance failure.
func (r *ServiceResolver) ReportFailure(target string, _ error) {
	r.b.ReportFailure(r.service, target)
}
