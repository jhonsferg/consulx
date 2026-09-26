# ADR 0004: Health endpoint injection

- Status: accepted
- Date: 2026-09-25

## Context

Consul needs an endpoint to probe. Asking every service to write one leads
to inconsistent semantics, and replacing the application's router is not
acceptable.

## Decision

- When health endpoints are enabled, `New` wraps `Server.Handler` with a
  small handler that serves the configured **exact** paths and delegates
  every other request, unchanged, to the original handler
  (`http.DefaultServeMux` when nil).
- Wrapping happens in `New`, not `Start`: net/http reads `Server.Handler`
  on each request, so replacing it after `ListenAndServe` would be a data
  race. The documented rule is to call `New` before serving.
  `HealthHandler()` is available to mount the endpoints manually instead.
- Endpoints: `/health` (every component), `/health/live` (liveness scope
  only; must not depend on external systems) and `/health/ready`
  (readiness scope, plus DOWN while shutting down). Consul checks
  readiness by default. Paths are configurable and opt-in.
- Status codes follow Consul's HTTP check contract: 200 UP (passing),
  429 DEGRADED (warning), 503 DOWN (critical). The DEGRADED code is
  configurable because Kubernetes treats 429 as a failed probe.
- Checkers run concurrently, each bounded to 80% of the Consul check
  timeout, so a slow dependency yields DOWN instead of a Consul timeout. A
  panicking checker reports DOWN.
- Applications report state with `health.Registry`: `Register` for checks
  run on every probe, `Set` for state pushed asynchronously.

## Consequences

- Existing routes, including user routes under `/health/...`, keep working.
- A user route with exactly the same path as a health endpoint is shadowed;
  the paths are configurable to avoid that.
- The wrapper adds one map lookup per request (see BenchmarkHealthMuxPassThrough).
