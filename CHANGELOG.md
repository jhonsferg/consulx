# Changelog

All notable changes are documented here. The project follows
[Semantic Versioning](https://semver.org); until `v1.0.0`, minor releases may
contain breaking changes, always listed under "Breaking".

## [Unreleased]

First release candidate (`v0.1.0`).

### Added

- `balancer.WithStaleGrace` and `DefaultStaleGrace`: `Client.Balancer` keeps
  the last known instances for 10 seconds when the passing list empties, as
  happens for a few seconds after a Consul agent restart.
- Explicit timeouts on every blocking query, and recovery that does not
  depend on the cluster having a leader.
- Client construction with functional options and `Config` values, layered
  over the environment (`CONSUL_*`, `CONSULX_*`) and YAML/JSON files
  (`LoadConfig`), with validation reporting every problem at once.
- Integration through `*http.Server`: port detection, address resolution
  chain (`AddressResolver`), injected `/health`, `/health/live`,
  `/health/ready` endpoints in front of any router.
- `health` package: component registry with UP / DEGRADED / DOWN,
  readiness and liveness scopes, draining on shutdown.
- Service registration through the Agent API: stable instance IDs,
  automatic metadata, tags, weights, tagged and multi-port addresses, HTTP,
  TCP, gRPC and TTL checks, `DeregisterCriticalServiceAfter`, maintenance
  mode, registration hook.
- Runtime with `Run`, `Start`, `Stop`, `Done`, `Errors` and `State`: TTL
  heartbeats, loss detection with hash-based blocking queries, automatic
  re-registration, retry with exponential backoff and jitter, fail-fast.
- Compatibility gate: agent version detection and rejection of optional
  fields unsupported by the connected agent.
- `discovery` package: health-aware immutable query builder and watches with
  coalesced events.
- `balancer` package: round robin, random and weighted strategies backed by
  watches.
- `kvconfig` package: Spring-compatible layered configuration in key/value,
  YAML or JSON, struct binding with defaults and required keys, validated
  live updates with `Watch[T]`.
- ACL token and token file, TLS and mutual TLS, redacted secrets.
- `log/slog` logging, `Metrics` interface, `contrib/prometheus`,
  `contrib/otel` (metrics and tracing), `contrib/fiber`.
- Integration test suite against Consul 1.20, 1.21, 1.22 and 2.0, examples,
  architecture documentation and ADRs.
