# ConsulX

[![CI](https://github.com/jhonsferg/consulx/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/jhonsferg/consulx/actions/workflows/ci.yml)
[![Security](https://github.com/jhonsferg/consulx/actions/workflows/security.yml/badge.svg?branch=main)](https://github.com/jhonsferg/consulx/actions/workflows/security.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/jhonsferg/consulx.svg)](https://pkg.go.dev/github.com/jhonsferg/consulx)
[![Release](https://img.shields.io/github/v/release/jhonsferg/consulx?sort=semver)](https://github.com/jhonsferg/consulx/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/jhonsferg/consulx)](go.mod)
[![License: MIT](https://img.shields.io/github/license/jhonsferg/consulx)](LICENSE)

![Go](https://img.shields.io/badge/Go-00ADD8?logo=go&logoColor=white)
![Consul](https://img.shields.io/badge/Consul-1.20%20%7C%201.21%20%7C%201.22%20%7C%202.0-E03875?logo=consul&logoColor=white)
![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-000000?logo=opentelemetry&logoColor=white)
![Prometheus](https://img.shields.io/badge/Prometheus-E6522C?logo=prometheus&logoColor=white)
![Docker](https://img.shields.io/badge/Testcontainers%20%2B%20Docker-2496ED?logo=docker&logoColor=white)
![GitHub Actions](https://img.shields.io/badge/GitHub%20Actions-2088FF?logo=githubactions&logoColor=white)

The Go-native Consul integration layer for production microservices.

ConsulX sits on top of the official client (`github.com/hashicorp/consul/api`)
and takes care of what every Go service otherwise writes by hand: resolving
its own address, registering with health checks, heartbeats, re-registering
after agent restarts, deregistering on shutdown, discovering other services,
client-side load balancing and layered configuration from Consul KV.

> Status: `v0.x`. The API may still change between minor versions; see
> [CHANGELOG.md](CHANGELOG.md) and [docs/status.md](docs/status.md).

## Contents

- [Why ConsulX?](#why-consulx)
- [Features](#features)
- [Installation](#installation)
- [Quick start](#quick-start)
- [HTTP integration](#http-integration)
- [Service registration](#service-registration)
- [Health checks](#health-checks)
- [Discovery](#discovery)
- [Configuration](#configuration)
- [KV and the rest of the Consul API](#kv-and-the-rest-of-the-consul-api)
- [ACL](#acl)
- [TLS](#tls)
- [Retry](#retry)
- [Lifecycle](#lifecycle)
- [Observability](#observability)
- [Docker and Kubernetes](#docker-and-kubernetes)
- [Production](#production)
- [Compatibility](#compatibility)
- [Examples](#examples)
- [Development](#development)

## Why ConsulX?

Integrating Consul by hand means building `AgentServiceRegistration` values,
guessing a routable address from `:8080`, writing `/health`, choosing check
intervals, deregistering on SIGTERM, noticing that the agent restarted and
lost your service, retrying with backoff, following blocking-query index
rules, and decoding KV into structs. ConsulX does all of it with safe
defaults, while `Raw()` keeps every Consul capability one call away.

It is modelled on the capabilities of Spring Cloud Consul (discovery and
distributed configuration, including its KV layout), expressed in plain Go:
functional options, `context.Context`, `*http.Server`, `slog`, errors.

## Features

| Area | What you get |
| ---- | ------------ |
| Registration | Agent API registration, stable instance IDs, automatic metadata, tags, weights, tagged and multi-port addresses, maintenance mode, registration hook for Connect/proxy settings |
| Health | Injected `/health`, `/health/live`, `/health/ready` in front of your router; HTTP, TCP, gRPC or TTL checks; component registry with UP / DEGRADED / DOWN |
| Reliability | Background retry with capped exponential backoff and jitter, fail-fast option, loss detection by hash-based blocking query, automatic re-registration, `DeregisterCriticalServiceAfter` crash protection |
| Lifecycle | `Run(ctx)` / `Start` / `Stop` / `Done` / `Errors` / `State`; no signal handling, no goroutine leaks |
| Discovery | Health-aware immutable query builder, watches with coalesced events, round robin / random / weighted load balancing |
| Configuration | Spring-compatible KV layout with profiles, key/value, YAML or JSON, struct binding with defaults and required keys, validated live updates |
| Security | ACL token and token file, TLS and mutual TLS, secrets redacted everywhere |
| Compatibility | Consul 1.20 to 2.0 tested; version-gated features rejected before the agent does |
| Observability | `log/slog`; metrics interface with Prometheus and OpenTelemetry adapters; request tracing |

## Installation

```sh
go get github.com/jhonsferg/consulx
```

Requires Go 1.26.7 or later (the minimum of the official Consul client).

## Quick start

Start a local agent:

```sh
docker run -d --name consul -p 8500:8500 hashicorp/consul:1.22 agent -dev "-client=0.0.0.0"
```

Integrate your server:

```go
func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", listOrders)
	server := &http.Server{Addr: ":8080", Handler: mux}

	consul, err := consulx.New(
		consulx.WithConsulAddress("http://localhost:8500"),
		consulx.WithServer(server),        // call New before serving
		consulx.WithServiceName("orders-api"),
		consulx.WithAutoHealth(),          // /health, /health/live, /health/ready
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}()

	// Registers, keeps the registration alive, deregisters on ctx cancellation.
	if err := consul.Run(ctx); err != nil {
		log.Print(err)
	}
	_ = server.Shutdown(context.Background())
}
```

`orders-api` now appears in Consul as `orders-api-<hostname>-8080`, checked
every 10s on `/health/ready`.

> Consul in Docker Desktop reaches processes on your machine through
> `host.docker.internal`: run with `CONSULX_SERVICE_ADDRESS=host.docker.internal`.

## HTTP integration

ConsulX receives your existing `*http.Server`; it never creates, starts or
stops it. Any router that is an `http.Handler` works unchanged: `net/http`,
Gin, Echo, Chi, Gorilla. With health endpoints enabled, `New` wraps
`server.Handler` so the exact health paths are served first and every other
request goes to your router, exactly as before. See
[docs/frameworks.md](docs/frameworks.md), including Fiber.

**Port**: `Service.Port`, else the listener passed with `WithListener`, else
`server.Addr`. A server on port 0 needs `WithListener`.

**Address**: never taken from a wildcard such as `:8080`. First match wins:

1. `WithServiceAddress` / `CONSULX_SERVICE_ADDRESS`
2. a custom `WithAddressResolver`, or the default chain:
   `Service.AddressEnv` (e.g. `POD_IP`), a concrete host in `server.Addr`,
   the local IP that routes to the Consul agent, the first private interface
   address.

Loopback addresses are refused unless `AllowLoopback` is set. Resolvers are
composable: `StaticAddress`, `EnvAddress`, `RouteAddress`,
`InterfaceAddress`, `HostnameAddress`, `FirstAddress`.

## Service registration

```go
consulx.New(
	consulx.WithServiceName("orders-api"),
	consulx.WithServiceID("orders-api-01"),            // default: <name>-<hostname>-<port>
	consulx.WithTags("v2", "blue"),
	consulx.WithMetadata(map[string]string{"team": "payments"}),
	consulx.Config{Service: consulx.ServiceConfig{
		Version:     "1.4.0",
		Environment: "prod",
		Weights:     &consulx.Weights{Passing: 10, Warning: 1},
	}},
	consulx.WithRegistrationHook(func(r *api.AgentServiceRegistration) {
		r.Connect = &api.AgentServiceConnect{SidecarService: &api.AgentServiceRegistration{}}
	}),
)
```

- The default ID `<name>-<hostname>-<port>` is unique per instance and stable
  across restarts, so a restarted process replaces its own entry.
  `IDStrategy: consulx.IDRandom` gives `<name>-<uuid>`.
- Automatic metadata: `secure`, `language`, `go_version`, `consulx_version`,
  `hostname`, plus `version`, `environment`, `zone` when set. **Your keys
  always win.** `DisableAutoMeta` turns it off.
- Registration uses the Agent API with `replace-existing-checks`.
- `EnableMaintenance` / `DisableMaintenance`; `Register` / `Deregister` for
  applications that manage registration themselves.

## Health checks

```go
consulx.WithAutoHealth() // or WithHealthEndpoints(consulx.HealthEndpoints{Ready: "/ready"})
consulx.WithHealth(consulx.HealthConfig{
	Interval: 10 * time.Second,
	Timeout:  5 * time.Second,
	DeregisterCriticalServiceAfter: time.Minute,
})

consul.Health().Register("database", health.CheckerFunc(func(ctx context.Context) health.Result {
	if err := db.PingContext(ctx); err != nil {
		return health.Result{Status: health.StatusDown, Error: err.Error()}
	}
	return health.Result{Status: health.StatusUp}
}))
consul.Health().Set("broker", health.Result{Status: health.StatusDegraded})
```

| Endpoint | Includes | Status codes |
| -------- | -------- | ------------ |
| `/health` | every component | 200 UP, 429 DEGRADED, 503 DOWN |
| `/health/live` | liveness components only | idem |
| `/health/ready` | readiness components (default scope); DOWN while shutting down | idem |

Consul maps 2xx to passing, 429 to warning and anything else to critical.
Kubernetes treats 429 as a failure: set `DegradedStatusCode: 200` if probes
share the endpoints.

Check types: `CheckHTTP` (default with endpoints), `CheckTTL` (default
without; ConsulX heartbeats every TTL/3 and immediately on `Set`),
`CheckTCP`, `CheckGRPC`, `CheckNone`. HTTP checks support method, headers,
body, TLS server name, skip-verify and flap damping.

## Discovery

```go
instances, err := consul.Discovery().
	Service("payments").
	Tag("v2").
	Datacenter("dc2").
	All(ctx)                         // passing instances only, by default

inst, err := consul.Discovery().Service("payments").Near("_agent").First(ctx)
if errors.Is(err, consulx.ErrServiceNotFound) { ... }

w, err := consul.Discovery().Watch(ctx, "payments")
for ev := range w.Events() {        // coalesced, with Added/Removed/Changed
	...
}

lb := consul.Balancer(balancer.RoundRobin()) // or Random(), Weighted()
inst, err := lb.Next(ctx, "payments")        // memory read, backed by a watch
```

Queries are immutable values, so a base query can be shared. `AnyStatus()`
includes unhealthy instances; `Meta`, `Filter`, `Consistency`, `Cached`,
`Namespace` and `Partition` are available.

## Configuration

ConsulX reads the Spring Cloud Consul layout, lowest precedence first:

```text
config/application/            shared by every service
config/application,<profile>/
config/<service>/
config/<service>,<profile>/
```

```go
type AppConfig struct {
	Database struct {
		Host     string        `consul:"host,required"`
		Port     int           `consul:"port" default:"5432"`
		MaxConns int           `consul:"max-conns"`
		Timeout  time.Duration `consul:"timeout" default:"5s"`
	} `consul:"database"`
	Features map[string]bool `consul:"features"`
}

func (c AppConfig) Validate() error { ... } // optional

var cfg AppConfig
err := consul.Config().LoadInto(ctx, &cfg)

w, err := kvconfig.Watch[AppConfig](ctx, consul.Config())
current := w.Current()              // always a complete, validated value
```

The profile defaults to `Service.Environment`. Formats: key/value (default),
YAML or JSON documents under `data` (`WithKVConfig`). Invalid changes are
rejected, reported once on `Errors()` and never applied; a reload that
cannot read Consul is reported there too, with a different message.

**ConsulX's own configuration** can come from code, a file and the
environment. Precedence, lowest first: defaults → `LoadConfig(file)` →
environment (`CONSUL_HTTP_ADDR`, `CONSUL_HTTP_TOKEN`, ... and `CONSULX_*`) →
`consulx.Config` values → functional options.

```yaml
consul:
  address: https://consul.service.consul:8501
  tokenFile: /var/run/secrets/consul-token
  tls: { caFile: /etc/consul/ca.pem }
service:
  name: orders-api
  tags: [v2]
health:
  enabled: true
  interval: 10s
lifecycle:
  failFast: false
```

```go
cfg, err := consulx.LoadConfig("consulx.yaml")
consul, err := consulx.New(cfg, consulx.WithServer(server))
```

## KV and the rest of the Consul API

`consul.Raw()` returns the official client, sharing ConsulX's address,
token, TLS and datacenter. Use it for KV reads and writes, CAS, sessions,
locks, transactions, ACL management, prepared queries, events, config
entries, Connect, operator APIs and anything added in future Consul
releases. Always pass a context:

```go
pair, _, err := consul.Raw().KV().Get("feature/flag", (&api.QueryOptions{}).WithContext(ctx))
```

## ACL

```go
consulx.WithToken(os.Getenv("CONSUL_HTTP_TOKEN"))
consulx.WithTokenFile("/var/run/secrets/consul/token") // wins over WithToken
```

The token needs `service:write` for the registered service, plus
`service_prefix "" { policy = "read" }` and `node_prefix "" { policy = "read" }`
(the policy used by the ACL integration test). Configuration also needs
`key_prefix "config/" { policy = "read" }`. Without `agent:read`
ConsulX cannot read the agent version and lets the agent validate optional
fields itself. Tokens are never logged.

## TLS

```go
consulx.WithConsulAddress("https://consul.service.consul:8501")
consulx.WithTLS(consulx.TLSConfig{
	CAFile:     "/etc/consul/ca.pem",
	CertFile:   "/etc/consul/client.pem", // mutual TLS
	KeyFile:    "/etc/consul/client-key.pem",
	ServerName: "server.dc1.consul",
})
```

Certificate verification is always on unless `InsecureSkipVerify` is set,
which logs a warning.

## Retry

```go
consulx.WithRetry(consulx.RetryConfig{
	InitialDelay: 500 * time.Millisecond, // defaults shown
	MaxDelay:     30 * time.Second,
	Multiplier:   2,
	MaxAttempts:  0, // unlimited
})
consulx.WithFailFast(true) // Start fails if registration fails within StartTimeout
```

With `FailFast` off (default), `Start` returns immediately, the service keeps
serving, and ConsulX registers once Consul is reachable.

## Lifecycle

| Call | Behaviour |
| ---- | --------- |
| `Run(ctx)` | `Start`, wait for `ctx`, then `Stop` bounded by `ShutdownTimeout`. Returns nil on normal shutdown |
| `Start(ctx)` | Register (per `FailFast`) and launch the runtime; `ctx` bounds start-up only |
| `Stop(ctx)` | Mark readiness DOWN, stop background tasks, deregister, close connections. Idempotent |
| `Done()` | Closed after `Stop` |
| `Errors()` | Asynchronous errors (outages, failed re-registration); never blocks; closed after `Stop` |
| `State()` | `idle`, `starting`, `running`, `degraded`, `stopping`, `stopped` |

ConsulX never handles OS signals; pass a context from `signal.NotifyContext`.
If the process dies without `Stop`, `DeregisterCriticalServiceAfter`
(default 1 minute) removes the instance once its check is critical.

## Observability

```go
consulx.WithLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
consulx.WithMetrics(prometheus.New(prom.DefaultRegisterer))    // contrib/prometheus
consulx.WithMetrics(otel.NewMetrics(otel.Meter()))             // contrib/otel
consulx.WithHTTPClient(otel.HTTPClient(nil))                   // trace Consul requests
```

Metrics: `consulx_register_total`, `consulx_register_errors_total`,
`consulx_deregister_total`, `consulx_discovery_requests_total`,
`consulx_discovery_errors_total`, `consulx_consul_requests_total`,
`consulx_consul_request_errors_total`, `consulx_consul_request_duration_seconds`,
`consulx_reconnect_total`, `consulx_health_status`, `consulx_runtime_state`,
`consulx_config_reload_total`, `consulx_config_reload_errors_total`,
`consulx_errors_dropped_total`.

## Docker and Kubernetes

See [docs/production.md](docs/production.md#docker-and-kubernetes). In
short: in Kubernetes, expose the pod IP and point ConsulX at it:

```yaml
env:
  - name: POD_IP
    valueFrom: { fieldRef: { fieldPath: status.podIP } }
  - name: CONSUL_HTTP_ADDR
    value: http://$(HOST_IP):8500   # node-local agent
```

```go
consulx.Config{Service: consulx.ServiceConfig{AddressEnv: "POD_IP"}}
```

## Production

Read [docs/production.md](docs/production.md) before deploying: TLS, ACL
policies, timeouts, check tuning, critical-service timeout, datacenters and
namespaces, resource usage and security.

## Compatibility

Tested against real agents: Consul **1.20.6, 1.21.5, 1.22.7 and 2.0.4**.
Consul 2.0 is a version-numbering change, not a new HTTP API. Optional
fields (multi-port services, IPv6 addresses) are gated by the detected agent
version. Details and evidence: [docs/compatibility.md](docs/compatibility.md).

## Examples

Runnable programs in [examples/](examples): `basic`, `gin`, `echo`, `chi`,
`discovery`, `config`, `health`. `discovery` calls the service that `basic`
registers (tag `v1`), and `config` reads the KV layout shown above, so run
`basic` or seed the keys first.

## Development

[docs/development.md](docs/development.md) explains how to run Consul
locally, the unit, race, fuzz and integration test suites, and the version
matrix; CI and automatic releases are described in
[docs/release.md](docs/release.md). Architecture: [docs/architecture.md](docs/architecture.md);
performance and memory: [docs/performance.md](docs/performance.md);
decisions: [docs/decisions/](docs/decisions/).
