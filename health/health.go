// Package health lets an application report its health and serves it over
// HTTP in a form Consul, Kubernetes and load balancers understand.
//
// Components report their state either by registering a Checker, which is
// called on every probe, or by pushing a state with Registry.Set. Each
// component belongs to one or more scopes:
//
//   - Readiness: whether the instance should receive traffic. Consul checks
//     this by default. This is the default scope.
//   - Liveness: whether the process is alive. Keep it free of external
//     dependencies, or orchestrators restart healthy processes when a
//     dependency fails.
//
// The aggregate health endpoint reports every component.
package health

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"
)

// Status is the health of a component or of the whole instance.
type Status string

const (
	// StatusUp means fully operational.
	StatusUp Status = "UP"
	// StatusDegraded means operational with reduced capacity or quality.
	// Consul reports it as "warning".
	StatusDegraded Status = "DEGRADED"
	// StatusDown means not operational. Consul reports it as "critical".
	StatusDown Status = "DOWN"
)

// severity orders statuses from best to worst.
func (s Status) severity() int {
	switch s {
	case StatusUp:
		return 0
	case StatusDegraded:
		return 1
	default: // DOWN and any unknown value are treated as DOWN
		return 2
	}
}

// Worse returns the worse of s and other.
func (s Status) Worse(other Status) Status {
	if other.severity() > s.severity() {
		return other
	}
	return s
}

// Result is the outcome of one component check.
type Result struct {
	Status Status `json:"status"`
	// Details are optional, JSON-serialisable values. Never put secrets here:
	// they are served over HTTP unless details are hidden.
	Details map[string]any `json:"details,omitempty"`
	// Error is a short human readable failure reason.
	Error string `json:"error,omitempty"`
}

// Checker reports the health of one component. Check must honour ctx and
// be safe for concurrent use.
type Checker interface {
	Check(ctx context.Context) Result
}

// CheckerFunc adapts a function to Checker.
type CheckerFunc func(ctx context.Context) Result

// Check implements Checker.
func (f CheckerFunc) Check(ctx context.Context) Result { return f(ctx) }

// Scope selects which probes include a component.
type Scope uint8

const (
	// Readiness includes the component in readiness probes.
	Readiness Scope = 1 << iota
	// Liveness includes the component in liveness probes.
	Liveness
)

// Report is the aggregate result of a probe.
type Report struct {
	Status     Status            `json:"status"`
	Components map[string]Result `json:"components,omitempty"`
}

// DefaultCheckTimeout bounds each Checker when the Registry has no timeout.
const DefaultCheckTimeout = 3 * time.Second

type component struct {
	scope   Scope
	checker Checker // nil for pushed components
	pushed  Result
	// inflight is the Check call in progress, shared by concurrent probes.
	// A checker that ignores its context can hang forever; sharing the call
	// keeps probes from piling up one stuck goroutine each.
	mu       sync.Mutex
	inflight *checkCall
}

// checkCall is one execution of a Checker.
type checkCall struct {
	ctx  context.Context // bounds the execution; probes wait at most this long
	done chan struct{}
	res  Result
}

// Registry holds the application's health components. It is safe for
// concurrent use. The zero value is not usable; call NewRegistry.
type Registry struct {
	timeout time.Duration

	mu         sync.RWMutex
	components map[string]*component
	draining   bool
	listeners  []func(Status)
}

// NewRegistry returns an empty Registry. An empty Registry is UP. timeout
// bounds each Checker call; zero means DefaultCheckTimeout.
func NewRegistry(timeout time.Duration) *Registry {
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	return &Registry{timeout: timeout, components: map[string]*component{}}
}

// Register adds or replaces a component checked on every probe. With no
// scopes the component is a Readiness component.
func (r *Registry) Register(name string, c Checker, scopes ...Scope) {
	r.mu.Lock()
	r.components[name] = &component{scope: combine(scopes), checker: c}
	r.mu.Unlock()
}

// Set pushes the state of a component, creating it when needed. It suits
// components that learn their state asynchronously (a consumer losing its
// broker connection, a cache warming up). With no scopes a new component is
// a Readiness component; an existing component keeps its scopes.
func (r *Registry) Set(name string, res Result, scopes ...Scope) {
	r.mu.Lock()
	c, ok := r.components[name]
	if !ok || c.checker != nil {
		c = &component{scope: combine(scopes)}
		r.components[name] = c
	} else if len(scopes) > 0 {
		c.scope = combine(scopes)
	}
	res.Details = maps.Clone(res.Details)
	c.pushed = res
	listeners := slices.Clone(r.listeners)
	r.mu.Unlock()
	for _, l := range listeners {
		l(res.Status)
	}
}

// Remove deletes a component.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	delete(r.components, name)
	r.mu.Unlock()
}

// SetDraining marks the instance as shutting down. While draining,
// readiness reports DOWN regardless of components, so traffic stops before
// the process exits. Liveness is unaffected.
func (r *Registry) SetDraining(draining bool) {
	r.mu.Lock()
	r.draining = draining
	r.mu.Unlock()
}

// OnPush registers fn to be called after every Set, with the pushed status.
// It lets TTL heartbeats react immediately. fn must not block.
func (r *Registry) OnPush(fn func(Status)) {
	r.mu.Lock()
	r.listeners = append(r.listeners, fn)
	r.mu.Unlock()
}

// Ready runs the readiness probe.
func (r *Registry) Ready(ctx context.Context) Report {
	rep := r.run(ctx, Readiness)
	r.mu.RLock()
	draining := r.draining
	r.mu.RUnlock()
	if draining {
		rep.Status = StatusDown
		rep.Components["shutdown"] = Result{Status: StatusDown, Error: "instance is shutting down"}
	}
	return rep
}

// Live runs the liveness probe.
func (r *Registry) Live(ctx context.Context) Report { return r.run(ctx, Liveness) }

// Health runs every component, whatever its scope.
func (r *Registry) Health(ctx context.Context) Report {
	rep := r.run(ctx, Readiness|Liveness)
	r.mu.RLock()
	draining := r.draining
	r.mu.RUnlock()
	if draining {
		rep.Status = StatusDown
		rep.Components["shutdown"] = Result{Status: StatusDown, Error: "instance is shutting down"}
	}
	return rep
}

// run evaluates every component matching scope. Checkers run concurrently,
// each bounded by the registry timeout; a checker that does not return in
// time is reported DOWN and its goroutine is left to finish on its own
// (it received a cancelled context).
func (r *Registry) run(ctx context.Context, scope Scope) Report {
	type named struct {
		name   string
		c      *component
		pushed Result // copied under the lock: Set may replace it
		call   *checkCall
	}
	r.mu.RLock()
	list := make([]named, 0, len(r.components))
	for name, c := range r.components {
		if c.scope&scope != 0 {
			list = append(list, named{name: name, c: c, pushed: c.pushed})
		}
	}
	r.mu.RUnlock()

	rep := Report{Status: StatusUp, Components: make(map[string]Result, len(list))}
	if len(list) == 0 {
		return rep
	}

	// Start every check first so they run concurrently, then collect: the
	// probe takes as long as the slowest check, without helper goroutines.
	for i := range list {
		if list[i].c.checker != nil {
			list[i].call = r.begin(ctx, list[i].c)
		}
	}
	for _, n := range list {
		res := n.pushed
		if n.call != nil {
			res = n.call.wait(ctx)
		}
		if res.Status == "" {
			res.Status = StatusDown
		}
		rep.Components[n.name] = res
		rep.Status = rep.Status.Worse(res.Status)
	}
	return rep
}

// begin returns the execution of c in flight, starting one if there is none,
// so concurrent probes share it. The execution has its own deadline and
// outlives the probe that started it if the checker is slow.
func (r *Registry) begin(ctx context.Context, c *component) *checkCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight != nil {
		return c.inflight
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	call := &checkCall{ctx: cctx, done: make(chan struct{})}
	c.inflight = call
	go func() {
		defer cancel()
		res := runChecker(cctx, c.checker)
		c.mu.Lock()
		call.res = res
		c.inflight = nil
		c.mu.Unlock()
		close(call.done)
	}()
	return call
}

// wait returns the result of the execution, or DOWN when its deadline
// passes (a hung checker) or the probe itself is cancelled.
func (call *checkCall) wait(ctx context.Context) Result {
	select {
	case <-call.done:
		return call.res
	case <-call.ctx.Done():
	case <-ctx.Done():
	}
	// The execution cancels its context right after publishing its result,
	// so both channels can be ready: a published result wins.
	select {
	case <-call.done:
		return call.res
	default:
		return Result{Status: StatusDown, Error: "health check timed out"}
	}
}

// runChecker calls the checker. A panic is DOWN, and so is any result
// produced after the deadline: a late answer is not a timely one.
func runChecker(ctx context.Context, c Checker) (res Result) {
	defer func() {
		if p := recover(); p != nil {
			res = Result{Status: StatusDown, Error: "health check panicked"}
		}
	}()
	res = c.Check(ctx)
	if ctx.Err() != nil {
		return Result{Status: StatusDown, Error: "health check timed out"}
	}
	return res
}

func combine(scopes []Scope) Scope {
	if len(scopes) == 0 {
		return Readiness
	}
	var s Scope
	for _, sc := range scopes {
		s |= sc
	}
	if s == 0 {
		return Readiness
	}
	return s
}
