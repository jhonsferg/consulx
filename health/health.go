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
		name string
		c    component
	}
	r.mu.RLock()
	var list []named
	for name, c := range r.components {
		if c.scope&scope != 0 {
			list = append(list, named{name, *c})
		}
	}
	r.mu.RUnlock()

	rep := Report{Status: StatusUp, Components: make(map[string]Result, len(list))}
	if len(list) == 0 {
		return rep
	}

	results := make([]Result, len(list))
	var wg sync.WaitGroup
	for i, n := range list {
		if n.c.checker == nil {
			results[i] = n.c.pushed
			continue
		}
		wg.Go(func() { results[i] = r.check(ctx, n.c.checker) })
	}
	wg.Wait()

	for i, n := range list {
		res := results[i]
		if res.Status == "" {
			res.Status = StatusDown
		}
		rep.Components[n.name] = res
		rep.Status = rep.Status.Worse(res.Status)
	}
	return rep
}

func (r *Registry) check(ctx context.Context, c Checker) (res Result) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	done := make(chan Result, 1) // buffered: a late checker never blocks
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- Result{Status: StatusDown, Error: "health check panicked"}
			}
		}()
		done <- c.Check(ctx)
	}()
	select {
	case res = <-done:
		return res
	case <-ctx.Done():
		return Result{Status: StatusDown, Error: "health check timed out"}
	}
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
