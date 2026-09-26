package consulx

import (
	"sync"
	"time"

	"github.com/jhonsferg/consulx/balancer"
	"github.com/jhonsferg/consulx/discovery"
)

// DefaultStaleGrace is the grace period applied by Client.Balancer. Measured
// against a restarted Consul 1.22 client agent, the list of passing instances
// stayed empty for 4 to 6 seconds although every instance was alive.
const DefaultStaleGrace = 10 * time.Second

// services holds the discovery client and the balancers of a Client.
type services struct {
	once sync.Once
	d    *discovery.Client

	mu        sync.Mutex
	balancers []*balancer.Balancer
}

// Discovery returns the discovery client. It shares ConsulX's connection,
// token, datacenter, timeouts, retry policy, logger and metrics.
//
//	instances, err := consul.Discovery().Service("payments").All(ctx)
func (c *Client) Discovery() *discovery.Client {
	c.svc.once.Do(func() {
		c.svc.d = discovery.New(c.api, discovery.Config{
			WaitTime:       c.cfg.Consul.WaitTime,
			RequestTimeout: c.cfg.Consul.RequestTimeout,
			Retry:          c.retry,
			Logger:         c.log,
			Observe: func(op string, err error) {
				c.metrics.IncCounter(MetricDiscoveryRequestsTotal, operationLabels(op)...)
				if err != nil {
					c.metrics.IncCounter(MetricDiscoveryErrorsTotal, operationLabels(op)...)
				}
			},
		})
	})
	return c.svc.d
}

// Balancer returns a client-side load balancer using strategy, for example
// balancer.RoundRobin(). It keeps one watch per service it is asked about
// and stops when the Client stops or when its Close method is called.
//
// It applies balancer.WithStaleGrace(DefaultStaleGrace) before opts, so an
// agent restart (which briefly reports every local service critical) does
// not fail calls to instances that are alive. Pass
// balancer.WithStaleGrace(0) to disable it.
//
//	lb := consul.Balancer(balancer.RoundRobin())
//	inst, err := lb.Next(ctx, "payments")
func (c *Client) Balancer(strategy balancer.Strategy, opts ...balancer.Option) *balancer.Balancer {
	opts = append([]balancer.Option{balancer.WithStaleGrace(DefaultStaleGrace)}, opts...)
	b := balancer.New(c.bgCtx, c.Discovery(), strategy, opts...)
	c.svc.mu.Lock()
	c.svc.balancers = append(c.svc.balancers, b)
	c.svc.mu.Unlock()
	return b
}

// closeServices stops every balancer created by the Client.
func (c *Client) closeServices() {
	c.bgCancel()
	c.svc.mu.Lock()
	list := c.svc.balancers
	c.svc.balancers = nil
	c.svc.mu.Unlock()
	for _, b := range list {
		_ = b.Close()
	}
}
