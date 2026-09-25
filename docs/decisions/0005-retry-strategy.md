# ADR 0005: Retry strategy

* Status: accepted
* Date: 2026-09-25

## Context

Consul agents restart, lose their state in dev mode, or become unreachable
during network partitions. Many instances usually lose the same agent or
server at the same time, so naive fixed-interval retries synchronise into
reconnect storms.

## Decision

* Capped exponential backoff: `InitialDelay` 500ms, `Multiplier` 2,
  `MaxDelay` 30s. 500ms reconnects quickly after short blips; 30s bounds the
  time to recover after a long outage while keeping load low.
* Equal jitter: delay `d` becomes uniform in `[d/2, d]`. It decorrelates
  instances like full jitter, but never produces near-zero waits.
* Unlimited attempts by default for background work (re-registration,
  watches): giving up would leave a healthy instance unregistered forever.
  `MaxAttempts` and `MaxElapsed` are available to bound it.
* Start-up retries with `FailFast` are bounded by `StartTimeout` (30s).
* Every attempt is bounded by `RequestTimeout` and every wait selects on the
  context, so cancellation is immediate.
* Errors that cannot succeed on retry (invalid configuration, unsupported
  feature, 4xx other than 429) are marked permanent and returned at once.
* `RetryPolicy` is an interface so applications can plug their own policy.

## Consequences

* Recovery time after an outage is at most `MaxDelay` plus request time.
* The policy is implemented once in `internal/backoff` and shared by every
  retry loop.
