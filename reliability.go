package consulx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/health"
	"github.com/jhonsferg/consulx/internal/backoff"
	"github.com/jhonsferg/consulx/internal/blocking"
)

// minWatchInterval spaces blocking queries that return immediately, so an
// agent answering without blocking cannot turn the watch into a hot loop.
const minWatchInterval = time.Second

// errServiceLost reports that the agent no longer knows the service.
var errServiceLost = errors.New("consulx: service missing from agent")

// registerWithRetry registers until success, the retry policy giving up, or
// ctx ending. During a fail-fast start-up, permanent errors (invalid
// definition, missing ACL permission) abort immediately; in the background
// they are retried too, because an operator may fix them without a restart.
func (c *Client) registerWithRetry(ctx context.Context, startup bool) error {
	return backoff.Retry(ctx, c.retry, c.cfg.Retry.MaxElapsed,
		func(attempt int, delay time.Duration, err error) {
			if !startup {
				c.setDegraded()
			}
			c.log.Warn("registration failed, retry scheduled",
				slog.Int("attempt", attempt), slog.Duration("delay", delay), slog.Any("error", err))
		},
		func(ctx context.Context) error {
			err := c.register(ctx)
			if err != nil && startup && isPermanent(err) {
				return backoff.Permanent(err)
			}
			return err
		})
}

// runRegistrar is the runtime task that keeps the service registered.
//
// Purpose: register (with backoff) and then watch the registration with a
// hash-based blocking query on /v1/agent/service/:id. A 404 means the agent
// lost the service (agent restart without state, reaped after being
// critical) and triggers re-registration; transport errors mark the runtime
// degraded and are retried with backoff.
//
// Exit condition: the runtime context is cancelled, or the retry policy
// gives up (MaxAttempts/MaxElapsed), in which case the error is reported
// and the instance stays unregistered.
func (c *Client) runRegistrar(ctx context.Context) {
	failures := 0 // consecutive failed watches; drives the backoff
	for ctx.Err() == nil {
		if !c.reg.get().Registered {
			if err := c.registerWithRetry(ctx, false); err != nil {
				if ctx.Err() == nil {
					c.log.Error("registration abandoned", slog.Any("error", err))
					c.report(err)
				}
				return
			}
			if c.State() == StateDegraded {
				c.metrics.IncCounter(MetricReconnectTotal)
				c.log.Info("service re-registered", slog.String("service_id", c.reg.get().ServiceID))
			}
			c.setRunning()
			failures = 0
		}

		err := c.watchRegistration(ctx, func() { failures = 0 })
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errServiceLost):
			c.reg.markLost()
			c.setDegraded()
			c.log.Warn("service missing from agent, re-registering", slog.String("service_id", c.reg.get().ServiceID))
		default:
			failures++
			c.waitUnavailable(ctx, err, failures)
		}
	}
}

// watchRegistration blocks on the agent's view of the service until it
// disappears (errServiceLost), a request fails, or ctx ends. healthy is
// called after every successful response. Each request is bounded by
// blocking.RequestTimeout, so a silent network partition surfaces as an
// error instead of stalling the watch.
func (c *Client) watchRegistration(ctx context.Context, healthy func()) error {
	id := c.reg.get().ServiceID
	timeout := blocking.RequestTimeout(c.cfg.Consul.WaitTime, c.cfg.Consul.RequestTimeout)
	var hash string
	for ctx.Err() == nil {
		start := time.Now()
		rctx, cancel := context.WithTimeout(ctx, timeout)
		q := (&api.QueryOptions{WaitHash: hash, WaitTime: c.cfg.Consul.WaitTime}).WithContext(rctx)
		_, meta, err := c.api.Agent().Service(id, q)
		cancel()
		if err != nil {
			if isStatus(err, http.StatusNotFound) {
				return errServiceLost
			}
			return err
		}
		healthy()
		if c.State() == StateDegraded {
			c.metrics.IncCounter(MetricReconnectTotal)
			c.log.Info("reconnection successful")
			c.setRunning()
		}
		hash = meta.LastContentHash
		if elapsed := time.Since(start); elapsed < minWatchInterval {
			if backoff.Sleep(ctx, minWatchInterval-elapsed) != nil {
				return ctx.Err()
			}
		}
	}
	return ctx.Err()
}

// waitUnavailable handles a failed watch: mark degraded, report the first
// failure of an outage, and wait with backoff. The next watch request is the
// probe: it goes to the local agent, so recovery does not depend on the
// cluster having a leader.
func (c *Client) waitUnavailable(ctx context.Context, cause error, attempt int) {
	c.setDegraded()
	if attempt == 1 {
		c.log.Warn("consul unavailable", slog.Any("error", cause))
		c.report(c.opError(nil, cause))
	}
	delay, ok := c.retry.NextDelay(attempt)
	if !ok {
		delay = c.cfg.Retry.MaxDelay
	}
	c.log.Debug("retry scheduled", slog.Int("attempt", attempt), slog.Duration("delay", delay), slog.Any("error", cause))
	_ = backoff.Sleep(ctx, delay)
}

// runHeartbeat is the runtime task for TTL checks.
//
// Purpose: report the readiness status to the TTL check every TTL/3, and
// immediately when the application pushes a health change. Reporting a
// third of the TTL tolerates two lost updates before the check expires.
//
// Exit condition: the runtime context is cancelled.
func (c *Client) runHeartbeat(ctx context.Context) {
	interval := c.cfg.Health.TTL / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-c.healthChanged:
		}
		c.heartbeat(ctx)
	}
}

// heartbeat sends one TTL update. Failures are logged; loss of the service
// is detected and repaired by the registrar.
func (c *Client) heartbeat(ctx context.Context) {
	cur := c.reg.get()
	if !cur.Registered || cur.CheckID == "" {
		return
	}
	rep := c.health.Ready(ctx)
	c.metrics.SetGauge(MetricHealthStatus, healthGauge(rep.Status))
	output := "ConsulX heartbeat: " + string(rep.Status)

	rctx, cancel := c.requestContext(ctx)
	defer cancel()
	q := (&api.QueryOptions{}).WithContext(rctx)
	err := c.api.Agent().UpdateTTLOpts(cur.CheckID, output, ttlStatus(rep.Status), q)
	if err != nil && ctx.Err() == nil {
		c.log.Debug("heartbeat failed", slog.String("check_id", cur.CheckID), slog.Any("error", err))
	}
}

// notifyHealthChanged wakes the heartbeat without blocking.
func (c *Client) notifyHealthChanged(health.Status) {
	select {
	case c.healthChanged <- struct{}{}:
	default:
	}
}

func healthGauge(s health.Status) float64 {
	switch s {
	case health.StatusUp:
		return 1
	case health.StatusDegraded:
		return 0.5
	default:
		return 0
	}
}

// setDegraded and setRunning only move between the two started states, so
// they never override Starting, Stopping or Stopped.
func (c *Client) setDegraded() {
	if s := c.State(); s == StateRunning || s == StateStarting {
		c.setState(StateDegraded)
	}
}

func (c *Client) setRunning() {
	if s := c.State(); s == StateDegraded || s == StateStarting {
		c.setState(StateRunning)
	}
}
