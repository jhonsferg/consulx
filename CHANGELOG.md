# Changelog

All notable changes are documented here. The project follows
[Semantic Versioning](https://semver.org); until `v1.0.0`, minor releases may
contain breaking changes, always listed under "Breaking".

## [Unreleased]

### Project

- Community health files: contributing guide, Code of Conduct (Contributor
  Covenant 2.1), security policy with private vulnerability reporting,
  support guide, pull request template, issue forms (bug report, feature
  request, question, documentation) and code owners.

## [0.2.0] - 2026-09-25

### Fixed

- Health checks: a checker that ignores its context no longer leaves one
  stuck goroutine per probe. Concurrent and successive probes share the
  execution in flight, and a result produced after the deadline counts as a
  timeout.
- Balancer: the watch of a service unused for `DefaultIdleTimeout`
  (15 minutes, `WithIdleTimeout`) is released, so dynamically named services
  no longer keep goroutines and connections forever.
- `kvconfig.Watch` reports a rejected configuration state once instead of
  once per watched folder: every folder wakes on a write, and a folder
  without keys used to rebuild and re-report an unchanged configuration.
  Signals queued while a reload runs are coalesced into that reload.
  Deduplication compares the configuration read from Consul, so two
  different invalid values are both reported even when they fail with the
  same message.
- A reload that cannot read Consul is logged as
  `configuration reload failed`, not as `configuration rejected`: only a
  configuration that was read and refused is rejected.
- `examples/basic` registers the `v1` tag that `examples/discovery` looks
  up, so the two examples work together out of the box;
  `examples/config` writes the value meant to be rejected to the profile
  layer (the layer without a profile is shadowed), labels failed reloads
  separately from rejected changes, and stops selecting on a closed
  `Errors()` channel.
- Documentation: the Docker command quotes `-client=0.0.0.0`, which
  PowerShell otherwise splits at the dots.

### Changed

- `Balancer.Next` copies only the selected instance: with 20 instances it
  went from 15.8 KB and 81 allocations to 464 B and 4 allocations per call.
  New `discovery.Watch.Pick` exposes the same zero-copy selection.
- Configuration binding allocates 99.7% less (324.9 KB to 904 B per bind)
  and runs 14 times faster.
- Readiness probes allocate 20% less.
- `TestNoLeakUnderChurn` guards against goroutine and heap growth on every
  build. See docs/performance.md.

### Project

- Release notes are generated from Conventional Commits, grouped by type,
  with install instructions and links; contrib modules get their own
  releases.
- Releases are published only when Go files change (`*.go`, `go.mod`,
  `go.sum`); documentation and CI changes ship with the next code release.
- README rewritten as a complete guide: centred header with large badges
  (build, security, API reference, release, Go version, licence,
  technologies), numbered sections with a matching table of contents, usage
  guide, twelve use cases (net/http, Gin, Echo, Chi, Fiber, gRPC, workers,
  gateways, configuration-only jobs, multi-port, management port, secure
  production, observability, testing), deployment environments (local,
  Docker Compose, Kubernetes, VMs, Enterprise) and full configuration,
  observability and error references. Every code sample is compiled
  against the real API.
- Every diagram is Mermaid (README, architecture, release process); no ASCII
  diagrams remain in the repository.

## [0.1.0] - 2026-09-25

First public release.

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
