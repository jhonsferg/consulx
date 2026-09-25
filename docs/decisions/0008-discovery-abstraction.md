# ADR 0008: Discovery abstraction

* Status: accepted
* Date: 2026-09-25

## Context

Services need healthy instances of other services, both as one-off lookups
and as a continuously updated view. Consul offers the Catalog API (no health
awareness) and the Health API, plus blocking queries for change
notification.

## Decision

* Discovery always uses the Health API (`/v1/health/service/:name`), never
  the Catalog, to select instances.
* **Default filter is passing only.** Returning critical instances by
  default is the unsafe choice for callers; `AnyStatus()` opts out and
  `Passing()` keeps the intent explicit.
* The query builder is an immutable value: each method returns a copy, so
  base queries can be shared across goroutines and specialised safely.
* Supported options: tags, metadata (compiled to a filter expression), raw
  filter expressions, datacenter, namespace and partition (Enterprise),
  `Near`, consistency modes (default, stale, consistent) and agent caching.
* `ServiceInstance` preserves what Consul returns: service and node
  addresses (with Consul's node-address fallback), ports and named ports,
  tagged addresses, weights, metadata, datacenter, namespace, partition,
  node details, checks and the aggregated status. Values are deep copies.
* Watches follow Consul's blocking-query rules: reset the index when it goes
  backwards, never wait on index 0, rate-limit with a token bucket (burst
  2), back off on errors and resync from index 0 after an outage. Events are
  emitted only when the exposed data changes.
* Event delivery coalesces: a slow consumer receives the latest state with a
  diff relative to the last event it actually read, never a backlog, and the
  watcher never blocks.

## Consequences

* A consumer can never be served an instance whose checks are failing
  unless it asks for it.
* Anything the builder does not cover is available through `Raw().Health()`
  and `Raw().Catalog()`.
