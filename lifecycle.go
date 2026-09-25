package consulx

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// State is the lifecycle state of a Client.
type State int32

const (
	// StateIdle: created, not started.
	StateIdle State = iota
	// StateStarting: Start is running.
	StateStarting
	// StateRunning: started and registered (or registration not required).
	StateRunning
	// StateDegraded: started, but Consul is unreachable or the service is
	// not registered; ConsulX is retrying in the background.
	StateDegraded
	// StateStopping: Stop is running.
	StateStopping
	// StateStopped: stopped; Done is closed. Terminal.
	StateStopped
)

var stateNames = [...]string{"idle", "starting", "running", "degraded", "stopping", "stopped"}

func (s State) String() string {
	if s >= 0 && int(s) < len(stateNames) {
		return stateNames[s]
	}
	return "unknown"
}

// errorsBuffer bounds Errors(). Errors beyond it are dropped (and counted)
// rather than blocking the runtime.
const errorsBuffer = 32

// lifecycle holds the runtime state of a Client.
type lifecycle struct {
	// opMu serialises Start and Stop, so Stop waits for an in-flight Start.
	opMu sync.Mutex

	stateMu sync.RWMutex
	state   State

	ctx    context.Context    // runtime context, set by Start
	cancel context.CancelFunc // cancels ctx
	wg     sync.WaitGroup     // runtime goroutines
	done   chan struct{}

	errMu     sync.Mutex
	errs      chan error
	errClosed bool
}

func newLifecycle() lifecycle {
	return lifecycle{done: make(chan struct{}), errs: make(chan error, errorsBuffer)}
}

// State returns the current lifecycle state.
func (c *Client) State() State {
	c.lc.stateMu.RLock()
	defer c.lc.stateMu.RUnlock()
	return c.lc.state
}

func (c *Client) setState(s State) {
	c.lc.stateMu.Lock()
	prev := c.lc.state
	c.lc.state = s
	c.lc.stateMu.Unlock()
	if prev != s {
		c.metrics.SetGauge(MetricRuntimeState, float64(s))
		c.log.Debug("runtime state changed", slog.String("from", prev.String()), slog.String("to", s.String()))
	}
}

// Done returns a channel closed when the Client has stopped.
func (c *Client) Done() <-chan struct{} { return c.lc.done }

// Errors returns asynchronous runtime errors, such as a failed
// re-registration. Every error is also logged, so reading the channel is
// optional. Errors are dropped when the buffer is full. The channel is
// closed after Stop, so ranging over it terminates.
func (c *Client) Errors() <-chan error { return c.lc.errs }

// report publishes an asynchronous error without ever blocking.
func (c *Client) report(err error) {
	c.lc.errMu.Lock()
	defer c.lc.errMu.Unlock()
	if c.lc.errClosed {
		return
	}
	select {
	case c.lc.errs <- err:
	default:
		c.metrics.IncCounter(MetricErrorsDroppedTotal)
	}
}

// Start registers the service (according to LifecycleConfig) and launches
// the background runtime. ctx bounds only the start-up; the runtime keeps
// running until Stop is called. Use Run to tie the runtime to a context.
//
// With FailFast, Start returns an error if registration does not succeed
// within StartTimeout. Otherwise it returns as soon as the runtime is
// launched and registration continues in the background.
//
// A Client can be started once: a second call returns ErrAlreadyStarted, a
// call after Stop returns ErrAlreadyStopped.
func (c *Client) Start(ctx context.Context) error {
	c.lc.opMu.Lock()
	defer c.lc.opMu.Unlock()

	switch c.State() {
	case StateIdle:
	case StateStopping, StateStopped:
		return ErrAlreadyStopped
	default:
		return ErrAlreadyStarted
	}
	c.setState(StateStarting)

	// The runtime outlives ctx: it keeps ctx values (trace IDs, loggers) but
	// not its cancellation, and ends when Stop cancels it.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.lc.ctx, c.lc.cancel = runCtx, cancel

	if err := c.start(ctx); err != nil {
		cancel()
		c.lc.wg.Wait()
		c.finish()
		return err
	}
	if c.State() == StateStarting {
		c.setState(StateRunning)
	}
	c.log.Info("consulx started", slog.String("state", c.State().String()))
	return nil
}

// start performs the start-up steps, bounded by ctx. Background tasks are
// launched with goRuntime. It is extended by later phases (registration,
// heartbeat, loss detection).
func (c *Client) start(ctx context.Context) error {
	if !*c.cfg.Lifecycle.AutoRegister {
		return nil
	}
	// Configuration problems (no port, no usable address) fail Start
	// whatever FailFast says: retrying cannot fix them.
	if _, err := c.resolveEndpoint(ctx); err != nil {
		return err
	}

	if c.cfg.Lifecycle.FailFast {
		sctx, cancel := context.WithTimeout(ctx, c.cfg.Lifecycle.StartTimeout)
		err := c.registerWithRetry(sctx, true)
		cancel()
		if err != nil {
			return err
		}
	} else if err := c.register(ctx); err != nil {
		if errors.Is(err, ErrInvalidConfiguration) || errors.Is(err, ErrUnsupportedFeature) {
			return err
		}
		c.log.Warn("initial registration failed, retrying in the background", slog.Any("error", err))
		c.setDegraded()
	}

	c.goRuntime("registrar", c.runRegistrar)
	if c.cfg.Health.Check == CheckTTL {
		c.health.OnPush(c.notifyHealthChanged)
		c.goRuntime("heartbeat", c.runHeartbeat)
	}
	return nil
}

// goRuntime runs fn in a goroutine tracked by the lifecycle. fn receives
// the runtime context and must return promptly once it is done; Stop waits
// for it. It must only be called while the runtime is started.
func (c *Client) goRuntime(name string, fn func(context.Context)) {
	ctx := c.lc.ctx
	c.lc.wg.Go(func() {
		c.log.Debug("runtime task started", slog.String("task", name))
		defer c.log.Debug("runtime task stopped", slog.String("task", name))
		fn(ctx)
	})
}

// Stop stops the runtime: it marks the instance as draining, stops every
// background task, deregisters the service (bounded by ctx) and releases
// connections. It is idempotent and safe to call concurrently; calls after
// the first return nil once the Client has stopped. Calling Stop on a
// Client that was never started returns ErrNotStarted.
func (c *Client) Stop(ctx context.Context) error {
	c.lc.opMu.Lock()
	defer c.lc.opMu.Unlock()

	switch c.State() {
	case StateIdle:
		return ErrNotStarted
	case StateStopped:
		return nil
	}
	c.log.Info("runtime stopping")
	c.setState(StateStopping)
	c.health.SetDraining(true)

	c.lc.cancel()
	// Every runtime goroutine selects on the runtime context and bounds its
	// requests with it, so this wait is short.
	c.lc.wg.Wait()

	err := c.shutdown(ctx)
	c.finish()
	c.log.Info("runtime stopped")
	return err
}

// shutdown runs the ordered shutdown steps after the runtime has stopped:
// deregistration, bounded by ctx. If it fails (Consul unreachable), the
// DeregisterCriticalServiceAfter timeout removes the instance later.
func (c *Client) shutdown(ctx context.Context) error {
	if !*c.cfg.Lifecycle.DeregisterOnShutdown || c.reg.get().ServiceID == "" {
		return nil
	}
	return c.deregister(ctx)
}

// finish moves to the terminal state and releases resources.
func (c *Client) finish() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	c.setState(StateStopped)
	c.lc.errMu.Lock()
	c.lc.errClosed = true
	close(c.lc.errs)
	c.lc.errMu.Unlock()
	close(c.lc.done)
}

// Run starts the Client, waits until ctx is done (or Stop is called from
// elsewhere), then stops it with a fresh context bounded by
// ShutdownTimeout, so deregistration still happens after ctx was
// cancelled. Cancelling ctx is the normal way to end Run and is not an
// error: Run returns nil after a clean shutdown.
func (c *Client) Run(ctx context.Context) error {
	if err := c.Start(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-c.lc.done:
		return nil
	}
	// Documented exception to "no detached contexts": shutdown must run
	// after the caller's context has been cancelled.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.cfg.Lifecycle.ShutdownTimeout)
	defer cancel()
	return c.Stop(stopCtx)
}
