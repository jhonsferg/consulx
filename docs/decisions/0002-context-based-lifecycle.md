# ADR 0002: Context-based lifecycle

* Status: accepted
* Date: 2026-09-25

## Context

A service integration runs background work (heartbeats, loss detection,
watches) that must stop cleanly, and must deregister the instance on
shutdown. Libraries that trap SIGTERM/SIGINT conflict with the application's
own signal handling and with orchestrators.

## Decision

* ConsulX never installs signal handlers. The application owns signals,
  typically through `signal.NotifyContext`, and passes the context to
  `Run(ctx)` or `Start(ctx)`.
* `Start` launches the runtime; `Stop` stops it, deregisters and waits for
  every goroutine; `Run` is `Start`, wait for `ctx.Done()`, then `Stop`.
  `Done()` is closed after `Stop` completes; `Errors()` reports asynchronous
  failures without blocking.
* Each goroutine has a documented purpose and exits when the runtime context
  is cancelled. Tests use goleak to prove it.
* Deregistration happens after the caller's context was cancelled, so it
  uses `context.WithoutCancel(ctx)` bounded by `ShutdownTimeout`. This is
  the only detached context in ConsulX.
* `DeregisterCriticalServiceAfter` is always set by default, because a
  graceful deregistration cannot happen after SIGKILL, OOM or host loss.
* A `Client` is single-use. Restarting would require resetting internal
  state that callers may observe (Done, Errors); creating a new Client is
  cheap and explicit.

## Consequences

* Integration is one line in `main` and composes with any signal strategy.
* Callers that forget to cancel the context keep the runtime alive; this is
  the standard Go contract and is documented.
