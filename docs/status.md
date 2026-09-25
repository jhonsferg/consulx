# Project status

Last verified: 2026-09-25, Go 1.27.1, official client
`github.com/hashicorp/consul/api` v1.34.5, Consul 1.20.6, 1.21.5, 1.22.7
and 2.0.4.

## Definition of Done

Legend: ✅ done and tested · 🟡 partially / with a caveat · ⬜ not done.

| Item                          | Status | Evidence / caveat                                                                                                                                                                                                                                                                                                                                                                                                                           |
|-------------------------------|--------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Compiles                      | ✅     | every module builds                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `go vet ./...`                | ✅     | all modules                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `go test ./...`               | ✅     | all modules                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `go test -race ./...`         | ✅     | run in a `golang:1.27` Linux container                                                                                                                                                                                                                                                                                                                                                                                                      |
| Integration with real Consul  | ✅     | `integration/`, 4 versions                                                                                                                                                                                                                                                                                                                                                                                                                  |
| Registration                  | ✅     | `TestRegistrationLifecycleWithHTTPCheck`                                                                                                                                                                                                                                                                                                                                                                                                    |
| Deregistration                | ✅     | same test, plus unit tests                                                                                                                                                                                                                                                                                                                                                                                                                  |
| Health checks                 | ✅     | HTTP and TTL against real agents; TCP/gRPC definitions unit tested                                                                                                                                                                                                                                                                                                                                                                          |
| Auto health                   | ✅     | injected endpoints checked by Consul                                                                                                                                                                                                                                                                                                                                                                                                        |
| Lifecycle                     | ✅     | unit tests with goleak, integration                                                                                                                                                                                                                                                                                                                                                                                                         |
| Context cancellation          | ✅     | `Run` returns and deregisters on cancel                                                                                                                                                                                                                                                                                                                                                                                                     |
| Retry                         | ✅     | unit tests, `TestConsulUnavailableAtStartup`                                                                                                                                                                                                                                                                                                                                                                                                |
| Reconnect                     | ✅     | `TestReconnectAndReRegister`                                                                                                                                                                                                                                                                                                                                                                                                                |
| Re-registration               | ✅     | same test, agent restarted empty                                                                                                                                                                                                                                                                                                                                                                                                            |
| Discovery                     | ✅     | `TestDiscoveryWatchAndBalancer`                                                                                                                                                                                                                                                                                                                                                                                                             |
| Watches                       | ✅     | discovery and configuration watches, real agent                                                                                                                                                                                                                                                                                                                                                                                             |
| KV                            | 🟡     | reading through `Config()` is tested; other KV operations are `Raw().KV()` (low-level supported)                                                                                                                                                                                                                                                                                                                                            |
| Config binding                | ✅     | `internal/bind` tests, fuzzing, integration                                                                                                                                                                                                                                                                                                                                                                                                 |
| Dynamic config                | ✅     | `TestDistributedConfiguration` (live and rejected changes)                                                                                                                                                                                                                                                                                                                                                                                  |
| TLS                           | ✅     | `TestTLS` (mutual TLS, wrong CA, missing client certificate)                                                                                                                                                                                                                                                                                                                                                                                |
| ACL                           | ✅     | `TestACLEnforced` (deny by default, least-privilege token, no token in logs)                                                                                                                                                                                                                                                                                                                                                                |
| Metadata                      | ✅     | unit and integration tests                                                                                                                                                                                                                                                                                                                                                                                                                  |
| Datacenter                    | 🟡     | option and per-query `Datacenter` sent (unit tested); no multi-datacenter integration test                                                                                                                                                                                                                                                                                                                                                  |
| Namespaces                    | 🟡     | Enterprise feature: gated and unit tested; not integration tested (no Enterprise license)                                                                                                                                                                                                                                                                                                                                                   |
| Low-level APIs exposed        | ✅     | `Raw()`                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| No goroutine leaks            | ✅     | goleak in every package with goroutines                                                                                                                                                                                                                                                                                                                                                                                                     |
| No race conditions            | ✅     | race detector, 3 to 5 repetitions                                                                                                                                                                                                                                                                                                                                                                                                           |
| Documentation                 | ✅     | README, docs/, ADRs, godoc on exported identifiers                                                                                                                                                                                                                                                                                                                                                                                          |
| Runnable examples             | ✅     | `examples/` builds; `basic` smoke-tested against Consul 2.0                                                                                                                                                                                                                                                                                                                                                                                 |
| Compatibility matrix          | ✅     | [compatibility.md](compatibility.md)                                                                                                                                                                                                                                                                                                                                                                                                        |
| Continuous integration        | 🟡     | Defined in `.github/workflows/` and validated locally (actionlint with shellcheck, every lint and test step run in Linux containers, versioning logic tested); **not yet executed on GitHub** because the repository is not pushed. Covers: lint, race tests on Go 1.26 and 1.27, 80% coverage gate, integration matrix, fuzzing, CodeQL, govulncheck, gosec, gitleaks, dependency review, automatic release (see [release.md](release.md)) |
| Static analysis               | ✅     | golangci-lint (staticcheck, gosec, errcheck, errorlint, ...) reports 0 issues in every module                                                                                                                                                                                                                                                                                                                                               |
| Known vulnerabilities         | ✅     | govulncheck: none reachable. GO-2026-5932 (`golang.org/x/crypto/openpgp`, required by Fiber, never imported, no fix available) is informational                                                                                                                                                                                                                                                                                             |
| Public API documented         | ✅     | godoc                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| Errors documented             | ✅     | `errors.go`, README                                                                                                                                                                                                                                                                                                                                                                                                                         |
| Shutdown behaviour documented | ✅     | README Lifecycle, production guide                                                                                                                                                                                                                                                                                                                                                                                                          |

## Production verification in a real cluster

Performed on 2026-09-25 with Consul 1.22.7: three servers
(`bootstrap-expect=3`), one client agent, two instances of a service and a
caller balancing five requests per second to them, all as separate
containers. No address was configured: every instance resolved its container
IP through the route to the agent.

| Scenario | Result |
| -------- | ------ |
| Leader failure and re-election | no effect: services stayed `running`, 0 failed calls |
| Client agent restart (state kept) | `degraded`, reconnected in about 4 s; checks restart critical for 4 to 6 s (Consul behaviour), bridged by the balancer grace period |
| Client agent recreated empty, with a new IP | services missing detected and re-registered within 5 s, 0 failed calls |
| Network partition of one instance (25 s) | removed from passing, 1 failed call (the one in flight), recovered on reconnection |
| SIGTERM (rolling deploy) | deregistered in 486 ms, clean exit, 0 failed calls |
| SIGKILL (crash) | critical within 5 s, reaped after 93 s by `DeregisterCriticalServiceAfter` |
| Consul completely down | balancers kept serving the last known instances |
| 10 minute chaos soak (10 agent restarts, 4 leader restarts, 3 rolling deploys) | always back to `running`; goroutines flat (16 to 18 caller, 11 to 12 service); heap flat (1 to 3.3 MB) |
| Balancer across 5 agent restarts | 173 failed calls (24%) without grace period, 0 with the 10 s default |

The harness is not part of the repository; the scenarios can be reproduced
with the steps in [development.md](development.md) and a Docker network.

## Known limitations

- Health endpoints are injected in `New`; the server must not be serving
  yet. `HealthHandler()` is the alternative.
- A `Client` is single-use (cannot be restarted after `Stop`).
- The scheme registered for a server is `https` only if its `TLSConfig` is
  set when `New` runs.
- The ACL token file is read once, when the Client is created; token
  rotation requires a new Client.
- Check types UDP, H2PING, Docker, OS service, alias and script are only
  available through `WithRegistrationHook`.
- Configuration formats PROPERTIES and FILES (Spring) are not supported.
- Connect sidecars and proxies are configured through the registration hook
  and are not covered by integration tests.
- Multi-datacenter behaviour and Enterprise namespaces/partitions are not
  integration tested.
- `Errors()` keeps 32 undelivered errors; later ones are dropped and counted.
- Each configuration watch keeps one blocking query per context folder
  (2 + 2 × profiles connections).
- The `consulx_version` metadata reads the module version from build info;
  it is `unknown` or a pseudo-version until a release is tagged.

## Technical risks

- **Address auto-detection** can pick an address consumers cannot reach in
  unusual networks. Mitigation: the source is logged; explicit address or
  `AddressResolver` wins.
- **Official client drift**: new registration fields may be rejected by
  older agents. Mitigation: feature gate plus
  `TestRegistrationFieldsAreClassified`, which fails on unclassified fields.
- **Consul behaviour changes** in future releases. Mitigation: the
  integration matrix is parameterised by `CONSUL_VERSION`.

## Technical debt

- Benchmarks exist without recorded baselines.
- `github.com/hashicorp/go-metrics` and `serf` are indirect dependencies of
  the official Consul client; ConsulX pins their latest versions (v0.7.0,
  v0.11.0), which also removes the legacy `armon/go-metrics` module.
- Fuzz targets run for 30 seconds per target in CI; no long-running
  (OSS-Fuzz style) campaign exists yet.
- Git history: commit `cad9db3` (discovery query builder) does not compile
  on its own, because it references the watch type added in the following
  commit, and four intermediate commits contain an unformatted
  `lifecycle.go`. Fixing it would require rewriting unpublished history.
