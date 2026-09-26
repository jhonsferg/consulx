# ADR 0009: Client-side load balancing

- Status: accepted
- Date: 2026-09-25

## Context

Callers want "give me an instance of payments" without querying Consul on
every request, and with a choice of strategy.

## Decision

- `balancer.Balancer` implements the `LoadBalancer` interface
  (`Next(ctx, service)`). It keeps one discovery watch per service, created
  lazily on first use, so `Next` reads memory instead of calling Consul. The
  first call for a service waits for the initial state, bounded by `ctx`.
- Strategies are pluggable: a `Strategy` creates one `Picker` per service,
  so stateful strategies keep independent state per service. Built in:
  `RoundRobin` (stable ID order), `Random`, `Weighted` (Consul weights: the
  passing weight, or the warning weight for instances in warning; weight 0
  excludes an instance unless all are 0).
- `WithQuery` customises the discovery query (tags, datacenter, ...). The
  default is passing instances only.
- Balancers created from a `Client` stop when the Client stops; standalone
  ones stop with their context or `Close`.

## Consequences

- A service with no healthy instance yields `ErrServiceNotFound` quickly.
- Retries, circuit breaking and outlier detection are out of scope; they
  belong to the HTTP client or a service mesh.
