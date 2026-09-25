// Package discovery finds service instances registered in Consul.
//
// Queries use the Health API, so instance health is always considered.
// By default only instances whose checks are all passing are returned;
// AnyStatus opts out.
//
//	instances, err := consul.Discovery().
//		Service("payments").
//		Tag("v2").
//		Datacenter("dc1").
//		All(ctx)
//
// Watch keeps an up-to-date view using Consul blocking queries.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
)

// ErrServiceNotFound is returned by First when no instance matches. The
// consulx package exposes the same value as consulx.ErrServiceNotFound.
var ErrServiceNotFound = errors.New("consulx: service not found")

var errNoName = errors.New("consulx: discovery query without a service name")

// Delayer computes retry delays; consulx passes its RetryPolicy.
type Delayer interface {
	NextDelay(attempt int) (time.Duration, bool)
}

// Config configures a Client. consulx builds it from its own configuration.
type Config struct {
	// WaitTime is the server-side wait of blocking queries.
	WaitTime time.Duration
	// RequestTimeout bounds non-blocking queries (All, First).
	RequestTimeout time.Duration
	// MinInterval paces blocking queries (token bucket, burst 2).
	MinInterval time.Duration
	// Retry computes delays after failed watch requests.
	Retry Delayer
	// Logger receives watch events. nil discards them.
	Logger *slog.Logger
	// Observe is called after every request to Consul with the operation
	// name ("query" or "watch") and the error, if any. It must not block.
	Observe func(op string, err error)
}

// Client runs discovery queries. It is safe for concurrent use.
type Client struct {
	api *api.Client
	cfg Config
}

// New returns a discovery client on top of the official client.
func New(c *api.Client, cfg Config) *Client {
	if cfg.WaitTime <= 0 {
		cfg.WaitTime = 5 * time.Minute
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = time.Second
	}
	if cfg.Retry == nil {
		cfg.Retry = fixedDelay(5 * time.Second)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Observe == nil {
		cfg.Observe = func(string, error) {}
	}
	return &Client{api: c, cfg: cfg}
}

type fixedDelay time.Duration

func (d fixedDelay) NextDelay(int) (time.Duration, bool) { return time.Duration(d), true }

// Service starts a query for the named service.
func (c *Client) Service(name string) Query {
	return Query{c: c, name: name, passing: true}
}

// Watch is a shortcut for Service(name).Watch(ctx).
func (c *Client) Watch(ctx context.Context, name string) (*Watch, error) {
	return c.Service(name).Watch(ctx)
}

// Services lists the service names known to the catalog with their tags.
func (c *Client) Services(ctx context.Context) (map[string][]string, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	out, _, err := c.api.Catalog().Services((&api.QueryOptions{}).WithContext(ctx))
	c.cfg.Observe("services", err)
	return out, err
}

func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.cfg.RequestTimeout > 0 {
		return context.WithTimeout(ctx, c.cfg.RequestTimeout)
	}
	return context.WithCancel(ctx)
}

// Consistency selects the read consistency mode of a query.
type Consistency int

const (
	// Default lets the leader answer, possibly with a small staleness window.
	Default Consistency = iota
	// Stale lets any server answer: fastest, scales reads, may be stale.
	Stale
	// Consistent forces a leader round-trip for strongly consistent reads.
	Consistent
)

// Query describes a discovery query. It is an immutable value: every method
// returns a modified copy, so a base query can be shared and specialised
// safely from several goroutines.
type Query struct {
	c           *Client
	name        string
	tags        []string
	meta        map[string]string
	filter      string
	passing     bool
	dc          string
	ns          string
	partition   string
	near        string
	consistency Consistency
	useCache    bool
}

// Tag requires every given tag.
func (q Query) Tag(tags ...string) Query {
	q.tags = append(slices.Clone(q.tags), tags...)
	return q
}

// Meta requires the metadata key to have value.
func (q Query) Meta(key, value string) Query {
	q.meta = maps.Clone(q.meta)
	if q.meta == nil {
		q.meta = map[string]string{}
	}
	q.meta[key] = value
	return q
}

// Filter adds a Consul filter expression, for example
// `Service.Meta.version != "1.0"`. Several filters are combined with "and".
func (q Query) Filter(expr string) Query {
	if q.filter == "" {
		q.filter = expr
	} else {
		q.filter = "(" + q.filter + ") and (" + expr + ")"
	}
	return q
}

// Passing returns only instances whose checks all pass. This is the
// default; the method exists to make intent explicit.
func (q Query) Passing() Query {
	q.passing = true
	return q
}

// AnyStatus returns instances whatever their health. Inspect
// ServiceInstance.Status before sending traffic to them.
func (q Query) AnyStatus() Query {
	q.passing = false
	return q
}

// Datacenter queries another datacenter.
func (q Query) Datacenter(dc string) Query {
	q.dc = dc
	return q
}

// Namespace queries a namespace (Consul Enterprise).
func (q Query) Namespace(ns string) Query {
	q.ns = ns
	return q
}

// Partition queries an admin partition (Consul Enterprise).
func (q Query) Partition(p string) Query {
	q.partition = p
	return q
}

// Near sorts results by estimated round-trip time from node. "_agent" means
// the local agent's node.
func (q Query) Near(node string) Query {
	q.near = node
	return q
}

// Consistency sets the read consistency mode.
func (q Query) Consistency(m Consistency) Query {
	q.consistency = m
	return q
}

// Cached serves the query from the local agent cache when possible. It
// reduces server load for frequent lookups at the cost of possibly stale
// results. Ignored by Watch, which uses blocking queries.
func (q Query) Cached() Query {
	q.useCache = true
	return q
}

// options builds the official query options.
func (q Query) options(ctx context.Context) *api.QueryOptions {
	o := &api.QueryOptions{
		Datacenter: q.dc,
		Namespace:  q.ns,
		Partition:  q.partition,
		Near:       q.near,
		Filter:     q.filterExpr(),
	}
	switch q.consistency {
	case Stale:
		o.AllowStale = true
	case Consistent:
		o.RequireConsistent = true
	}
	return o.WithContext(ctx)
}

// filterExpr combines metadata requirements and the user filter.
func (q Query) filterExpr() string {
	parts := make([]string, 0, len(q.meta)+1)
	for _, k := range slices.Sorted(maps.Keys(q.meta)) {
		parts = append(parts, fmt.Sprintf("Service.Meta[%s] == %s", strconv.Quote(k), strconv.Quote(q.meta[k])))
	}
	if q.filter != "" {
		parts = append(parts, "("+q.filter+")")
	}
	return strings.Join(parts, " and ")
}

// fetch runs the health query and converts the result.
func (q Query) fetch(opts *api.QueryOptions) ([]ServiceInstance, *api.QueryMeta, error) {
	entries, meta, err := q.c.api.Health().ServiceMultipleTags(q.name, q.tags, q.passing, opts)
	if err != nil {
		return nil, nil, err
	}
	out := make([]ServiceInstance, 0, len(entries))
	for _, e := range entries {
		out = append(out, fromEntry(e))
	}
	if q.near == "" {
		sortInstances(out) // stable order; with Near, keep Consul's RTT order
	}
	return out, meta, nil
}

// All returns every matching instance. An empty result is not an error.
func (q Query) All(ctx context.Context) ([]ServiceInstance, error) {
	if q.name == "" {
		return nil, errNoName
	}
	ctx, cancel := q.c.requestContext(ctx)
	defer cancel()
	opts := q.options(ctx)
	opts.UseCache = q.useCache
	out, _, err := q.fetch(opts)
	q.c.cfg.Observe("query", err)
	if err != nil {
		return nil, fmt.Errorf("consulx: discover %s: %w", q.name, err)
	}
	return out, nil
}

// First returns the first matching instance (the nearest one with Near).
// It returns ErrServiceNotFound when none matches.
func (q Query) First(ctx context.Context) (ServiceInstance, error) {
	all, err := q.All(ctx)
	if err != nil {
		return ServiceInstance{}, err
	}
	if len(all) == 0 {
		return ServiceInstance{}, fmt.Errorf("%w: %s", ErrServiceNotFound, q.name)
	}
	return all[0], nil
}
