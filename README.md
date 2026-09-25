<div align="center">

# ConsulX

**The Go-native Consul integration layer for production microservices**

Registration · Health checks · Service discovery · Load balancing · Distributed configuration

<p>
  <a href="https://github.com/jhonsferg/consulx/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/jhonsferg/consulx/ci.yml?branch=main&style=for-the-badge&logo=githubactions&logoColor=white&label=CI"></a>
  <a href="https://github.com/jhonsferg/consulx/actions/workflows/security.yml"><img alt="Security" src="https://img.shields.io/github/actions/workflow/status/jhonsferg/consulx/security.yml?branch=main&style=for-the-badge&logo=githubactions&logoColor=white&label=Security"></a>
  <a href="https://pkg.go.dev/github.com/jhonsferg/consulx"><img alt="Go Reference" src="https://img.shields.io/badge/Go%20Reference-pkg.go.dev-007D9C?style=for-the-badge&logo=go&logoColor=white"></a>
  <a href="https://github.com/jhonsferg/consulx/releases"><img alt="Release" src="https://img.shields.io/github/v/release/jhonsferg/consulx?sort=semver&style=for-the-badge&logo=github&logoColor=white"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/jhonsferg/consulx?style=for-the-badge&logo=go&logoColor=white"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/jhonsferg/consulx?style=for-the-badge"></a>
</p>

<p>
  <img alt="Go" src="https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white">
  <img alt="Consul" src="https://img.shields.io/badge/Consul-1.20%20%E2%80%93%202.0-E03875?style=for-the-badge&logo=consul&logoColor=white">
  <img alt="OpenTelemetry" src="https://img.shields.io/badge/OpenTelemetry-000000?style=for-the-badge&logo=opentelemetry&logoColor=white">
  <img alt="Prometheus" src="https://img.shields.io/badge/Prometheus-E6522C?style=for-the-badge&logo=prometheus&logoColor=white">
  <img alt="Docker" src="https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white">
  <img alt="GitHub Actions" src="https://img.shields.io/badge/GitHub%20Actions-2088FF?style=for-the-badge&logo=githubactions&logoColor=white">
</p>

[Documentation](docs/) · [Examples](examples/) · [Releases](https://github.com/jhonsferg/consulx/releases) · [API reference](https://pkg.go.dev/github.com/jhonsferg/consulx) · [Changelog](CHANGELOG.md)

</div>

---

## Table of contents

1. [Overview](#1-overview)
2. [Features](#2-features)
3. [Requirements and compatibility](#3-requirements-and-compatibility)
4. [Installation](#4-installation)
5. [Quick start](#5-quick-start)
6. [Core concepts](#6-core-concepts)
   - 6.1 [Client lifecycle](#61-client-lifecycle)
   - 6.2 [Configuration layers](#62-configuration-layers)
   - 6.3 [Address and port resolution](#63-address-and-port-resolution)
   - 6.4 [Health model](#64-health-model)
7. [Usage guide](#7-usage-guide)
   - 7.1 [Registering a service](#71-registering-a-service)
   - 7.2 [Health checks](#72-health-checks)
   - 7.3 [Service discovery](#73-service-discovery)
   - 7.4 [Client-side load balancing](#74-client-side-load-balancing)
   - 7.5 [Distributed configuration](#75-distributed-configuration)
   - 7.6 [Maintenance mode](#76-maintenance-mode)
   - 7.7 [Explicit registration](#77-explicit-registration)
   - 7.8 [Runtime errors and states](#78-runtime-errors-and-states)
   - 7.9 [Low-level Consul API](#79-low-level-consul-api)
8. [Use cases](#8-use-cases)
   - 8.1 [REST API with net/http](#81-rest-api-with-nethttp)
   - 8.2 [Gin, Echo and Chi](#82-gin-echo-and-chi)
   - 8.3 [Fiber](#83-fiber)
   - 8.4 [gRPC service](#84-grpc-service)
   - 8.5 [Background worker](#85-background-worker)
   - 8.6 [API gateway or BFF](#86-api-gateway-or-bff)
   - 8.7 [Configuration-only service](#87-configuration-only-service)
   - 8.8 [Multi-port service](#88-multi-port-service)
   - 8.9 [Health on a management port](#89-health-on-a-management-port)
   - 8.10 [Secure production setup](#810-secure-production-setup)
   - 8.11 [Metrics and tracing](#811-metrics-and-tracing)
   - 8.12 [Testing services that use ConsulX](#812-testing-services-that-use-consulx)
9. [Deployment environments](#9-deployment-environments)
   - 9.1 [Local development](#91-local-development)
   - 9.2 [Docker and Docker Compose](#92-docker-and-docker-compose)
   - 9.3 [Kubernetes](#93-kubernetes)
   - 9.4 [Virtual machines and bare metal](#94-virtual-machines-and-bare-metal)
   - 9.5 [Consul Enterprise](#95-consul-enterprise)
10. [Configuration reference](#10-configuration-reference)
    - 10.1 [Functional options](#101-functional-options)
    - 10.2 [Config struct](#102-config-struct)
    - 10.3 [Configuration file](#103-configuration-file)
    - 10.4 [Environment variables](#104-environment-variables)
    - 10.5 [Defaults](#105-defaults)
11. [Observability reference](#11-observability-reference)
12. [Errors reference](#12-errors-reference)
13. [Performance](#13-performance)
14. [Documentation](#14-documentation)
15. [Development](#15-development)
16. [License](#16-license)

---

## 1. Overview

Integrating Consul by hand means building `AgentServiceRegistration` values,
guessing a routable address from `:8080`, writing `/health` endpoints,
choosing check intervals, deregistering on SIGTERM, noticing that the agent
restarted and lost your service, retrying with backoff, following the
blocking-query index rules and decoding KV into structs.

ConsulX does all of that with safe defaults, on top of the official client
(`github.com/hashicorp/consul/api`), and keeps every Consul capability one
call away through `Raw()`. It is modelled on the capabilities of Spring Cloud
Consul, including its KV layout, expressed in plain Go: functional options,
`context.Context`, `*http.Server`, `log/slog` and errors.

```text
            your service
   ┌───────────────────────────────┐
   │ *http.Server   your router    │
   │        │                      │
   │   ConsulX health endpoints    │◄──── HTTP check from the Consul agent
   │        │                      │
   │   consulx.Client  ────────────┼────► registration, heartbeats, watches
   │   ├─ Discovery / Balancer ◄───┼───── healthy instances of other services
   │   └─ Config (KV) ◄────────────┼───── layered configuration, live reload
   └───────────────────────────────┘
                   │  official consul/api client
                   ▼
            Consul agent (/v1)
```

## 2. Features

| Area | What you get |
|------|--------------|
| Registration | Agent API registration, stable instance IDs, automatic metadata, tags, weights, tagged and multi-port addresses, maintenance mode, registration hook for Connect and proxy settings |
| Health | Injected `/health`, `/health/live`, `/health/ready` in front of any router; HTTP, TCP, gRPC or TTL checks; component registry with UP / DEGRADED / DOWN |
| Reliability | Retry with capped exponential backoff and jitter, fail-fast option, loss detection, automatic re-registration, crash protection with `DeregisterCriticalServiceAfter`, bounded timeouts |
| Lifecycle | `Run(ctx)`, `Start`, `Stop`, `Done`, `Errors`, `State`; no signal handling; no goroutine leaks |
| Discovery | Health-aware immutable query builder, watches with coalesced events |
| Load balancing | Round robin, random and weighted strategies backed by watches; survives agent restarts |
| Configuration | Spring-compatible KV layout with profiles, key/value, YAML or JSON, struct binding with defaults and required keys, validated live updates |
| Security | ACL token and token file, TLS and mutual TLS, secrets redacted in logs and serialisation |
| Observability | `log/slog`, metrics interface, Prometheus and OpenTelemetry adapters, request tracing |
| Compatibility | Consul 1.20 to 2.0 tested; version-gated features rejected before the agent does |

## 3. Requirements and compatibility

| Requirement | Supported |
|-------------|-----------|
| Go | 1.26.7 or later (the minimum of the official Consul client); tested on 1.26 and 1.27 |
| Consul | 1.20, 1.21, 1.22 and 2.0, Community Edition; tested against real agents |
| Consul Enterprise | namespaces and admin partitions supported, not integration tested |
| Operating systems | any platform supported by Go; CI runs on Linux |

Consul 2.0 is a version-numbering change, not a new HTTP API. Optional
fields (multi-port services, IPv6 addresses) are gated by the detected agent
version. Details and evidence: [docs/compatibility.md](docs/compatibility.md).

## 4. Installation

```sh
go get github.com/jhonsferg/consulx
```

Optional modules, each with its own version:

```sh
go get github.com/jhonsferg/consulx/contrib/prometheus   # Prometheus metrics
go get github.com/jhonsferg/consulx/contrib/otel         # OpenTelemetry metrics and tracing
go get github.com/jhonsferg/consulx/contrib/fiber        # Fiber v3 health endpoints
```

## 5. Quick start

Start a local Consul agent:

```sh
docker run -d --name consul -p 8500:8500 hashicorp/consul:1.22 agent -dev "-client=0.0.0.0"
```

Add ConsulX to an existing HTTP service:

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jhonsferg/consulx"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	server := &http.Server{Addr: ":8080", Handler: mux}

	consul, err := consulx.New(
		consulx.WithConsulAddress("http://localhost:8500"),
		consulx.WithServer(server),        // call New before the server starts serving
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

	// Registers the service, keeps it registered and deregisters it when ctx ends.
	if err := consul.Run(ctx); err != nil {
		log.Print(err)
	}
	_ = server.Shutdown(context.Background())
}
```

The service appears in Consul as `orders-api-<hostname>-8080`, checked every
10 seconds on `/health/ready`.

> When Consul runs in Docker Desktop and the service runs on your machine,
> start the service with `CONSULX_SERVICE_ADDRESS=host.docker.internal` so
> the agent can reach it.

## 6. Core concepts

### 6.1 Client lifecycle

A `consulx.Client` is created with `New`, which validates the configuration
and performs **no network I/O**. The runtime starts with `Start` or `Run`
and stops with `Stop` or when the `Run` context ends.

```text
idle ──Start──► starting ──registered──► running ◄─────────┐
                    │                       │               │ recovered
                    │ FailFast=false        ▼               │
                    └──────────────────► degraded ──────────┘  (retrying)
running / degraded ──ctx done or Stop──► stopping ──► stopped (Done closed)
```

| Call | Behaviour |
|------|-----------|
| `Run(ctx)` | `Start`, wait for `ctx`, then `Stop` bounded by `ShutdownTimeout`; returns `nil` on normal shutdown |
| `Start(ctx)` | registers (according to `FailFast`) and launches the runtime; `ctx` bounds the start-up only |
| `Stop(ctx)` | marks readiness DOWN, stops background tasks, deregisters, closes connections; idempotent |
| `Done()` | closed after `Stop` |
| `Errors()` | asynchronous errors (outages, failed re-registration); never blocks; closed after `Stop` |
| `State()` | `idle`, `starting`, `running`, `degraded`, `stopping`, `stopped` |

ConsulX never handles OS signals: pass a context from `signal.NotifyContext`.
A `Client` is single-use; create a new one to start again.

### 6.2 Configuration layers

Configuration comes from five layers, lowest precedence first. A later layer
overrides only the fields it sets.

| # | Layer | How |
|---|-------|-----|
| 1 | Defaults | built in, see [10.5 Defaults](#105-defaults) |
| 2 | File | `consulx.LoadConfig("consulx.yaml")` (YAML or JSON) |
| 3 | Environment | `CONSUL_*` (official names) and `CONSULX_*`, see [10.4](#104-environment-variables) |
| 4 | `consulx.Config` values | passed to `New` like any option |
| 5 | Functional options | `consulx.WithServiceName(...)`, applied in argument order |

Zero values in a `Config` do not override earlier layers. Boolean fields whose
default is `true` are pointers (`consulx.Bool(false)`), and options such as
`WithFailFast(false)` always assign.

### 6.3 Address and port resolution

**Port**, first match wins: `WithServicePort`, the default entry of
`Service.Ports`, the listener passed with `WithListener`, the port of
`server.Addr`. A server on port `0` needs `WithListener`.

**Address**, first match wins:

1. `WithServiceAddress` or `CONSULX_SERVICE_ADDRESS`;
2. a custom `WithAddressResolver`, or else the default chain:
   `Service.AddressEnv` (for example `POD_IP`), a concrete host in
   `server.Addr`, the local IP that routes to the Consul agent, the first
   private interface address.

Wildcards (`0.0.0.0`, `::`) are never registered; loopback is refused unless
`Service.AllowLoopback` is set. The chosen source is logged
(`address_source`). Resolvers compose: `StaticAddress`, `EnvAddress`,
`RouteAddress`, `InterfaceAddress`, `HostnameAddress`, `FirstAddress`.

**Service ID**: `WithServiceID`, or `<name>-<hostname>-<port>` (unique per
instance and stable across restarts), or `<name>-<uuid>` with
`IDStrategy: consulx.IDRandom`.

### 6.4 Health model

The application reports component health to `consul.Health()`, a
`health.Registry`. Each component is `UP`, `DEGRADED` or `DOWN` and belongs to
the readiness scope (default), the liveness scope, or both.

| Endpoint | Includes | UP | DEGRADED | DOWN |
|----------|----------|----|----------|------|
| `/health` | every component | 200 | 429 | 503 |
| `/health/live` | liveness components | 200 | 429 | 503 |
| `/health/ready` | readiness components; DOWN while shutting down | 200 | 429 | 503 |

Consul maps 2xx to *passing*, 429 to *warning* and anything else to
*critical*. By default Consul checks `/health/ready`. When no endpoint is
injected, ConsulX registers a TTL check and reports the readiness status
itself every TTL/3.

## 7. Usage guide

### 7.1 Registering a service

```go
consul, err := consulx.New(
	consulx.WithServer(server),
	consulx.WithServiceName("orders-api"),
	consulx.WithServiceID("orders-api-01"),                    // optional
	consulx.WithTags("v2", "blue"),
	consulx.WithMetadata(map[string]string{"team": "payments"}),
	consulx.Config{Service: consulx.ServiceConfig{
		Version:     "1.4.0",
		Environment: "prod",
		Zone:        "eu-west-1a",
		Weights:     &consulx.Weights{Passing: 10, Warning: 1},
	}},
)
```

- Registration uses the Agent API with `replace-existing-checks`, so a
  restarted instance replaces its own registration.
- Automatic metadata: `secure`, `language`, `go_version`, `consulx_version`,
  `hostname`, plus `version`, `environment` and `zone` when set. **Your keys
  always win**; `DisableAutoMeta` turns automatic metadata off.
- Fields ConsulX does not model (Connect sidecars, proxy settings, kind,
  locality, extra checks) go through a hook that runs on every registration:

```go
consulx.WithRegistrationHook(func(r *api.AgentServiceRegistration) {
	r.Connect = &api.AgentServiceConnect{SidecarService: &api.AgentServiceRegistration{}}
})
```

### 7.2 Health checks

```go
consulx.WithAutoHealth()                                         // default endpoints
consulx.WithHealthEndpoints(consulx.HealthEndpoints{Ready: "/ready", Live: "/live"})
consulx.WithHealth(consulx.HealthConfig{
	Interval:               10 * time.Second,
	Timeout:                5 * time.Second,
	FailuresBeforeCritical: 3,      // damp flapping
	DegradedStatusCode:     200,    // if Kubernetes probes share the endpoints
})
```

Report component health:

```go
// Checked on every probe (in parallel, bounded by the check timeout).
consul.Health().Register("database", health.CheckerFunc(func(ctx context.Context) health.Result {
	if err := db.PingContext(ctx); err != nil {
		return health.Result{Status: health.StatusDown, Error: err.Error()}
	}
	return health.Result{Status: health.StatusUp}
}))

// Pushed when the state changes (TTL checks are updated immediately).
consul.Health().Set("broker", health.Result{
	Status:  health.StatusDegraded,
	Details: map[string]any{"lag": 1200},
})

// Liveness-only component: never makes readiness fail.
consul.Health().Register("event-loop", loopChecker, health.Liveness)
```

Check types (`HealthConfig.Check`): `CheckHTTP` (default with endpoints),
`CheckTTL` (default without), `CheckTCP`, `CheckGRPC`, `CheckNone`. Concurrent
probes share one execution per component, and a checker that hangs is
reported DOWN without accumulating goroutines.

### 7.3 Service discovery

```go
d := consul.Discovery()

// Passing instances only, by default.
instances, err := d.Service("payments").Tag("v2").All(ctx)

// Nearest healthy instance.
inst, err := d.Service("payments").Near("_agent").First(ctx)
if errors.Is(err, consulx.ErrServiceNotFound) {
	// no healthy instance
}
url := inst.URL() // "http://10.0.0.7:8080"

// More filters.
q := d.Service("payments").
	Meta("version", "2").
	Filter(`Service.Port != 8081`).
	Datacenter("dc2").
	Consistency(discovery.Stale).
	Cached()

// Follow changes with blocking queries; events are coalesced.
w, err := d.Watch(ctx, "payments")
defer w.Close()
for ev := range w.Events() {
	log.Printf("%d instances (+%d -%d ~%d)", len(ev.Instances), len(ev.Added), len(ev.Removed), len(ev.Changed))
}
```

Queries are immutable values: every method returns a copy, so a base query
can be shared. `AnyStatus()` includes unhealthy instances; inspect
`inst.Status` before using them.

### 7.4 Client-side load balancing

```go
lb := consul.Balancer(balancer.RoundRobin())    // or balancer.Random(), balancer.Weighted()

inst, err := lb.Next(ctx, "payments")           // memory read, backed by a watch
resp, err := http.Get(inst.URL() + "/charges")
```

Options: `balancer.WithQuery` (custom query per service), `WithStaleGrace`
(keep the last instances while an agent restart empties the list; 10 s by
default through `consul.Balancer`) and `WithIdleTimeout` (release watches of
unused services; 15 min by default). Implement `balancer.Strategy` for custom
strategies. Balancers stop when the client stops.

### 7.5 Distributed configuration

ConsulX reads the Spring Cloud Consul layout, lowest precedence first:

```text
config/application/                 shared by every service
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

// Optional: rejected values are never applied.
func (c AppConfig) Validate() error {
	if c.Database.MaxConns > 1000 {
		return errors.New("max-conns too high")
	}
	return nil
}

// Load once.
var cfg AppConfig
err := consul.Config().LoadInto(ctx, &cfg)

// Or keep it up to date.
w, err := kvconfig.Watch[AppConfig](ctx, consul.Config(),
	kvconfig.OnChange(func(old, new AppConfig) { pool.Resize(new.Database.MaxConns) }))
current := w.Current()          // always a complete, validated value
for err := range w.Errors() {   // rejected changes and failed reloads
	log.Print(err)
}
```

| Setting | Default | Notes |
|---------|---------|-------|
| Profile | `Service.Environment` | `KVConfig.Profiles` for several |
| Format | key/value | `KVConfig.Format: "yaml"` or `"json"` reads one document under `data` |
| Supported types | | strings, bools, ints, uints, floats, `time.Duration`, `encoding.TextUnmarshaler`, slices, maps, nested and embedded structs, pointers |
| Tags | | `consul:"name"`, `consul:"name,required"`, `consul:"-"`, `default:"value"`; untagged fields match `max-conns`, `max_conns` or `MaxConns` |
| Typos | ignored | `KVConfig.ErrorUnused: true` reports unknown keys |

### 7.6 Maintenance mode

```go
_ = consul.EnableMaintenance(ctx, "deploying v2")  // excluded from discovery, stays registered
_ = consul.DisableMaintenance(ctx)
```

### 7.7 Explicit registration

For applications that manage registration themselves:

```go
consul, _ := consulx.New(consulx.WithServiceName("orders-api"), consulx.WithServicePort(8080),
	consulx.WithAutoRegister(false))
err := consul.Register(ctx)    // one registration, no background runtime
err = consul.Deregister(ctx)
```

Nothing re-registers the service if the agent loses it; prefer `Run` or
`Start` for the managed lifecycle.

### 7.8 Runtime errors and states

```go
go func() {
	for err := range consul.Errors() {
		log.Printf("consul: %v", err) // e.g. consul unavailable, re-registration failed
	}
}()

reg := consul.Registration() // ServiceID, Address, Port, CheckID, Registered
if consul.State() == consulx.StateDegraded {
	// Consul unreachable or service not registered; ConsulX keeps retrying
}
```

### 7.9 Low-level Consul API

`consul.Raw()` returns the official client with ConsulX's address, token,
TLS and datacenter. Use it for everything ConsulX does not wrap: KV writes,
CAS, sessions and locks, transactions, ACL management, prepared queries,
events, config entries, Connect, operator APIs. Always pass a context:

```go
pair, _, err := consul.Raw().KV().Get("feature/flag", (&api.QueryOptions{}).WithContext(ctx))
```

## 8. Use cases

### 8.1 REST API with net/http

The [quick start](#5-quick-start) is the complete pattern: `WithServer`,
`WithServiceName`, `WithAutoHealth` and `Run(ctx)`. Any `http.Handler` works,
including `http.DefaultServeMux` when `server.Handler` is nil. Runnable
example: [examples/basic](examples/basic).

### 8.2 Gin, Echo and Chi

Their engines are `http.Handler`s, so no adapter is needed. Serve them with
your own `*http.Server` instead of the framework's `Run`/`Start` helpers:

```go
router := gin.New()                  // echo.New() or chi.NewRouter() work the same way
router.GET("/orders", listOrders)
server := &http.Server{Addr: ":8080", Handler: router}

consul, err := consulx.New(consulx.WithServer(server), consulx.WithServiceName("orders-api"),
	consulx.WithAutoHealth())
go server.ListenAndServe()
err = consul.Run(ctx)
```

Examples: [gin](examples/gin), [echo](examples/echo), [chi](examples/chi).
All three are covered by the integration tests.

### 8.3 Fiber

Fiber runs on fasthttp and has no `*http.Server`, so give ConsulX the port
and mount the endpoints with `contrib/fiber`:

```go
import consulxfiber "github.com/jhonsferg/consulx/contrib/fiber"

consul, err := consulx.New(
	consulx.WithServiceName("orders-api"),
	consulx.WithServicePort(3000),
	consulx.WithAutoHealth(),
)
app := fiber.New()
consulxfiber.Mount(app, consul)
go app.Listen(":3000")
err = consul.Run(ctx)
```

### 8.4 gRPC service

Consul's gRPC check calls the standard health protocol
(`grpc.health.v1.Health/Check`), so register it on your server and select the
gRPC check:

```go
import (
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

srv := grpc.NewServer()
healthpb.RegisterHealthServer(srv, grpchealth.NewServer())
lis, _ := net.Listen("tcp", ":9090")
go srv.Serve(lis)

consul, err := consulx.New(
	consulx.WithServiceName("orders-grpc"),
	consulx.WithListener(lis),                                   // registers port 9090
	consulx.WithHealth(consulx.HealthConfig{
		Check:       consulx.CheckGRPC,
		GRPCService: "orders.v1.Orders",                          // optional
	}),
)
err = consul.Run(ctx)
```

### 8.5 Background worker

Services without HTTP endpoints use the TTL check: ConsulX reports the
readiness of `consul.Health()` every TTL/3, and immediately when a component
changes. A registration needs a port; use the port of your metrics or admin
endpoint.

```go
consul, err := consulx.New(
	consulx.WithServiceName("billing-worker"),
	consulx.WithServicePort(9100),
	consulx.WithHealth(consulx.HealthConfig{TTL: 15 * time.Second}),
)

// In the consumer loop:
consul.Health().Set("queue", health.Result{Status: health.StatusUp})
// On broker loss:
consul.Health().Set("queue", health.Result{Status: health.StatusDown, Error: "broker unreachable"})
```

### 8.6 API gateway or BFF

Services that only call others disable registration and use discovery and
balancing. Start the client so background watches are managed:

```go
consul, err := consulx.New(consulx.WithAutoRegister(false))
if err := consul.Start(ctx); err != nil {
	log.Fatal(err)
}
defer consul.Stop(context.Background())

lb := consul.Balancer(balancer.Weighted(),
	balancer.WithQuery(func(d *discovery.Client, svc string) discovery.Query {
		return d.Service(svc).Tag("public")
	}))

proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
	inst, err := lb.Next(r.In.Context(), "orders-api")
	if err != nil {
		return // answered by the proxy's ErrorHandler
	}
	target, _ := url.Parse(inst.URL())
	r.SetURL(target)
}}
```

Example: [examples/discovery](examples/discovery).

### 8.7 Configuration-only service

A job or CLI that only needs configuration:

```go
consul, err := consulx.New(
	consulx.WithServiceName("report-job"),          // selects config/report-job/
	consulx.WithAutoRegister(false),
	consulx.Config{Service: consulx.ServiceConfig{Environment: "prod"}},
)
var cfg JobConfig
if err := consul.Config().LoadInto(ctx, &cfg); err != nil {
	log.Fatal(err)
}
```

Use `kvconfig.Watch` in long-running processes (see
[7.5](#75-distributed-configuration)). Example: [examples/config](examples/config).

### 8.8 Multi-port service

One instance exposing several named ports (Consul 1.22 or later; ConsulX
refuses the field on older agents instead of failing the registration):

```go
consulx.Config{Service: consulx.ServiceConfig{Ports: []consulx.ServicePort{
	{Name: "http", Port: 8080, Default: true},
	{Name: "grpc", Port: 9090},
}}}

// Consumers:
port, ok := inst.PortNamed("grpc")
```

### 8.9 Health on a management port

The Consul check targets the registered address and port. To expose health
only on an internal port, mount `HealthHandler()` there and point the check
at it:

```go
consul, err := consulx.New(
	consulx.WithServer(public),                          // registers the public port
	consulx.WithServiceName("orders-api"),
	consulx.WithHealthEndpoints(consulx.HealthEndpoints{Ready: "/ready"}),
	consulx.WithRegistrationHook(func(r *api.AgentServiceRegistration) {
		r.Check.HTTP = "http://" + net.JoinHostPort(r.Address, "9090") + "/ready"
	}),
)
mgmt := &http.Server{Addr: ":9090", Handler: consul.HealthHandler()}
```

### 8.10 Secure production setup

```go
consul, err := consulx.New(
	consulx.WithConsulAddress("https://127.0.0.1:8501"),
	consulx.WithTLS(consulx.TLSConfig{
		CAFile:     "/etc/consul/tls/ca.pem",
		CertFile:   "/etc/consul/tls/client.pem",       // mutual TLS
		KeyFile:    "/etc/consul/tls/client-key.pem",
		ServerName: "localhost",
	}),
	consulx.WithTokenFile("/var/run/secrets/consul/token"),
	consulx.WithServer(server),
	consulx.WithServiceName("orders-api"),
	consulx.WithAutoHealth(),
	consulx.WithFailFast(true),                         // refuse to start unregistered
)
```

Least-privilege ACL policy for this service:

```hcl
service "orders-api" { policy = "write" }
service_prefix ""    { policy = "read" }
node_prefix ""       { policy = "read" }
key_prefix "config/application" { policy = "read" }
key_prefix "config/orders-api"  { policy = "read" }
```

Certificate verification is always on unless `InsecureSkipVerify` is set,
which logs a warning. Tokens are never logged. More in
[docs/production.md](docs/production.md).

### 8.11 Metrics and tracing

```go
import (
	cxotel "github.com/jhonsferg/consulx/contrib/otel"
	cxprom "github.com/jhonsferg/consulx/contrib/prometheus"
	"github.com/prometheus/client_golang/prometheus"
)

consulx.WithLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
consulx.WithMetrics(cxprom.New(prometheus.DefaultRegisterer))   // Prometheus
consulx.WithMetrics(cxotel.NewMetrics(cxotel.Meter()))          // or OpenTelemetry
consulx.WithHTTPClient(cxotel.HTTPClient(nil))                  // trace every request to Consul
```

### 8.12 Testing services that use ConsulX

- `consulx.New` performs no network I/O, so handlers and health logic can be
  unit tested without Consul: build the client, register components on
  `consul.Health()` and call `consul.HealthHandler()` with `httptest`.
- Disable registration in unit tests with `WithAutoRegister(false)` and silence
  logs with `WithLogger(nil)`.
- For end-to-end tests, start a dev agent with Testcontainers, as the
  [integration suite](integration) does, and set `WithConsulAddress`.

```go
func TestReadiness(t *testing.T) {
	consul, _ := consulx.New(consulx.WithServiceName("svc"), consulx.WithAutoHealth(),
		consulx.WithAutoRegister(false), consulx.WithLogger(nil))
	consul.Health().Set("db", health.Result{Status: health.StatusDown})

	rec := httptest.NewRecorder()
	consul.HealthHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
}
```

## 9. Deployment environments

### 9.1 Local development

```sh
docker run -d --name consul -p 8500:8500 hashicorp/consul:1.22 agent -dev "-client=0.0.0.0"
CONSULX_SERVICE_ADDRESS=host.docker.internal go run ./cmd/orders-api
```

The UI is at <http://localhost:8500>. The quotes around `-client` are needed
in PowerShell. On Linux, run the agent with
`--add-host host.docker.internal:host-gateway`.

### 9.2 Docker and Docker Compose

Inside a shared network, the default resolver registers the container IP
(the route to the agent). Configure through the environment:

```yaml
services:
  consul:
    image: hashicorp/consul:1.22
    command: agent -dev -client=0.0.0.0
  orders-api:
    image: example/orders-api
    environment:
      CONSUL_HTTP_ADDR: http://consul:8500
      CONSULX_SERVICE_NAME: orders-api
      CONSULX_ENVIRONMENT: prod
      CONSULX_SERVICE_TAGS: v2,blue
      CONSULX_HEALTH_ENABLED: "true"
```

Set `CONSULX_SERVICE_ADDRESS` when consumers reach the service through
another address (published ports, host networking).

### 9.3 Kubernetes

With a node-local Consul agent (DaemonSet), expose the pod and host IPs:

```yaml
spec:
  terminationGracePeriodSeconds: 30        # above ShutdownTimeout + drain time
  containers:
    - name: orders-api
      env:
        - name: HOST_IP
          valueFrom: { fieldRef: { fieldPath: status.hostIP } }
        - name: POD_IP
          valueFrom: { fieldRef: { fieldPath: status.podIP } }
        - name: CONSUL_HTTP_ADDR
          value: http://$(HOST_IP):8500
        - name: CONSUL_HTTP_TOKEN_FILE
          value: /var/run/secrets/consul/token
      readinessProbe:
        httpGet: { path: /health/ready, port: 8080 }
      livenessProbe:
        httpGet: { path: /health/live, port: 8080 }
```

```go
consulx.Config{
	Service: consulx.ServiceConfig{AddressEnv: "POD_IP"},
	Health:  consulx.HealthConfig{DegradedStatusCode: http.StatusOK}, // probes share the endpoints
}
```

Pod names are host names, so default service IDs are unique per pod. If
Consul's own Kubernetes integration (catalog sync or mesh injection) already
registers pods, use `WithAutoRegister(false)` and keep ConsulX for discovery
and configuration.

### 9.4 Virtual machines and bare metal

Run a Consul client agent on each host and let ConsulX use the default
address (`127.0.0.1:8500`). The route-based resolver registers the address of
the interface that reaches the agent; on multi-homed hosts pick one
explicitly:

```go
consulx.WithAddressResolver(consulx.InterfaceAddress(consulx.InterfaceFilter{Name: "eth1"}))
```

A YAML file is convenient on hosts managed by configuration tools:

```go
cfg, err := consulx.LoadConfig("/etc/orders-api/consulx.yaml")
consul, err := consulx.New(cfg, consulx.WithServer(server))
```

### 9.5 Consul Enterprise

```go
consulx.WithNamespace("payments")
consulx.WithPartition("eu")
consul.Discovery().Service("ledger").Namespace("finance").All(ctx)
```

ConsulX detects the edition from the agent version and refuses namespaces
and partitions against Community Edition before sending them.

## 10. Configuration reference

### 10.1 Functional options

| Option | Purpose |
|--------|---------|
| `WithConsulAddress(addr)` | agent address: `host:port`, `http(s)://host:port`, `unix:///path` |
| `WithDatacenter(dc)` | datacenter for every request |
| `WithNamespace(ns)`, `WithPartition(p)` | Consul Enterprise tenancy |
| `WithToken(t)`, `WithTokenFile(path)` | ACL token; the file wins |
| `WithTLS(TLSConfig)` | TLS towards Consul (enables https) |
| `WithRequestTimeout(d)` | bound of every non-blocking request |
| `WithHTTPClient(c)` | custom `http.Client` (tracing, proxies) |
| `WithAPIConfig(fn)` | adjust the official client configuration |
| `WithServer(srv)` | integrate an `*http.Server` (port, scheme, health injection) |
| `WithListener(ln)` | take the port from a listener (port 0, gRPC) |
| `WithServiceName`, `WithServiceID`, `WithServiceAddress`, `WithServicePort` | service identity |
| `WithAddressResolver(r)` | replace the address resolution chain |
| `WithTags(...)`, `WithMetadata(map)` | service tags and metadata |
| `WithRegistrationHook(fn)` | edit the registration before it is sent |
| `WithAutoHealth()`, `WithHealthEndpoints(e)`, `WithHealth(h)` | health endpoints and check |
| `WithDeregisterCriticalServiceAfter(d)` | crash protection timeout (negative disables) |
| `WithRetry(RetryConfig)`, `WithRetryPolicy(p)` | retry behaviour |
| `WithAutoRegister(bool)`, `WithFailFast(bool)`, `WithShutdownTimeout(d)` | lifecycle |
| `WithKVConfig(KVConfig)` | distributed configuration layout |
| `WithLogger(l)`, `WithMetrics(m)` | observability |

### 10.2 Config struct

```go
consulx.Config{
	Consul: consulx.ConsulConfig{
		Address, Scheme, Datacenter, Namespace, Partition, Token, TokenFile, HTTPAuth,
		TLS: consulx.TLSConfig{Enabled, CAFile, CAPath, CAPEM, CertFile, KeyFile, CertPEM,
			KeyPEM, ServerName, InsecureSkipVerify},
		DialTimeout, RequestTimeout, WaitTime,
	},
	Service: consulx.ServiceConfig{
		Name, ID, IDStrategy, Address, AddressEnv, AllowLoopback, PreferIPv6, Port, Ports,
		Scheme, Tags, Meta, DisableAutoMeta, Version, Environment, Zone, TaggedAddresses,
		Weights, EnableTagOverride, Namespace, Partition,
	},
	Health: consulx.HealthConfig{
		Enabled, Endpoints, HideDetails, DegradedStatusCode, Check, CheckPath, Interval,
		Timeout, TTL, DeregisterCriticalServiceAfter, Method, Header, Body, TLSServerName,
		TLSSkipVerify, UseTLS, GRPCService, SuccessBeforePassing, FailuresBeforeWarning,
		FailuresBeforeCritical,
	},
	Retry:     consulx.RetryConfig{InitialDelay, MaxDelay, Multiplier, DisableJitter, MaxAttempts, MaxElapsed},
	Lifecycle: consulx.LifecycleConfig{AutoRegister, DeregisterOnShutdown, FailFast, StartTimeout, ShutdownTimeout},
	KV:        consulx.KVConfig{Name, Profiles, Prefix, DefaultContext, ProfileSeparator, Format, DataKey, ErrorUnused},
}
```

Every field is documented in the [API reference](https://pkg.go.dev/github.com/jhonsferg/consulx#Config).
`consul.EffectiveConfig()` returns the result of all layers.

### 10.3 Configuration file

`consulx.LoadConfig(path)` reads YAML or JSON (unknown keys are rejected to
catch typos) and applies the environment on top:

```yaml
consul:
  address: https://127.0.0.1:8501
  tokenFile: /var/run/secrets/consul/token
  requestTimeout: 10s
  tls:
    caFile: /etc/consul/tls/ca.pem
service:
  name: orders-api
  tags: [v2, blue]
  environment: prod
  meta:
    team: payments
health:
  enabled: true
  interval: 10s
  timeout: 5s
  deregisterCriticalServiceAfter: 1m
retry:
  initialDelay: 500ms
  maxDelay: 30s
lifecycle:
  failFast: false
  shutdownTimeout: 10s
kv:
  format: yaml
```

### 10.4 Environment variables

| Variable | Field |
|----------|-------|
| `CONSUL_HTTP_ADDR` | `Consul.Address` |
| `CONSUL_HTTP_TOKEN`, `CONSUL_HTTP_TOKEN_FILE` | `Consul.Token`, `Consul.TokenFile` |
| `CONSUL_HTTP_AUTH` | `Consul.HTTPAuth` (`user:password`) |
| `CONSUL_HTTP_SSL`, `CONSUL_HTTP_SSL_VERIFY` | https, certificate verification |
| `CONSUL_CACERT`, `CONSUL_CAPATH`, `CONSUL_CLIENT_CERT`, `CONSUL_CLIENT_KEY`, `CONSUL_TLS_SERVER_NAME` | `Consul.TLS` |
| `CONSUL_NAMESPACE`, `CONSUL_PARTITION` | Enterprise tenancy |
| `CONSULX_DATACENTER` | `Consul.Datacenter` |
| `CONSULX_SERVICE_NAME`, `CONSULX_SERVICE_ID` | service identity |
| `CONSULX_SERVICE_ADDRESS`, `CONSULX_SERVICE_PORT` | registered address and port |
| `CONSULX_SERVICE_TAGS` | comma-separated tags |
| `CONSULX_SERVICE_META` | `key=value,key=value` |
| `CONSULX_SERVICE_VERSION`, `CONSULX_ENVIRONMENT`, `CONSULX_ZONE` | metadata; the environment is also the KV profile |
| `CONSULX_PROFILES` | comma-separated KV profiles |
| `CONSULX_HEALTH_ENABLED`, `CONSULX_HEALTH_INTERVAL` | health endpoints and interval |
| `CONSULX_DEREGISTER_CRITICAL_AFTER` | crash protection timeout |
| `CONSULX_FAIL_FAST` | fail-fast start-up |

Malformed values are reported by `New`, never ignored.

### 10.5 Defaults

| Setting | Default | Reason |
|---------|---------|--------|
| Consul address | `127.0.0.1:8500` | node-local agent |
| Dial / request timeout | 5 s / 10 s | bounded requests |
| Blocking query wait | 5 min | Consul's default |
| Health endpoints | `/health`, `/health/live`, `/health/ready` when enabled | |
| Check | HTTP on readiness with endpoints, TTL without | |
| Check interval / timeout | 10 s / 5 s | timeout below the interval |
| TTL | 30 s, heartbeat every 10 s | tolerates two lost updates |
| Deregister critical after | 1 min | Consul's minimum |
| Retry | 500 ms to 30 s, ×2, jitter, unlimited | fast recovery, no reconnect storms |
| Auto-register / deregister on shutdown | on / on | |
| Fail fast | off | a Consul outage does not stop a healthy service |
| Start / shutdown timeout | 30 s / 10 s | |
| Balancer stale grace / idle timeout | 10 s / 15 min | agent restarts, dynamic service names |
| KV layout | `config/`, context `application`, separator `,` | Spring Cloud Consul compatible |

## 11. Observability reference

**Logs** (`log/slog`, default `slog.Default()`, `WithLogger(nil)` silences):
`consul client initialized`, `service registered`, `health check configured`,
`service deregistration started`, `service deregistered`,
`consul unavailable`, `retry scheduled`, `reconnection successful`,
`service re-registered`, `configuration changed`, `configuration rejected`,
`configuration reload failed`, `runtime stopping`, `runtime stopped`.
Tokens, TLS keys and KV values are never logged.

**Metrics** (`WithMetrics`, no-op by default):

| Metric | Type |
|--------|------|
| `consulx_register_total`, `consulx_register_errors_total` | counter |
| `consulx_deregister_total`, `consulx_deregister_errors_total` | counter |
| `consulx_discovery_requests_total`, `consulx_discovery_errors_total` | counter, label `operation` |
| `consulx_consul_requests_total`, `consulx_consul_request_errors_total` | counter, label `operation` |
| `consulx_consul_request_duration_seconds` | histogram |
| `consulx_reconnect_total` | counter |
| `consulx_health_status` | gauge: 1 up, 0.5 degraded, 0 down |
| `consulx_runtime_state` | gauge: 0 idle … 5 stopped |
| `consulx_config_reload_total`, `consulx_config_reload_errors_total` | counter |
| `consulx_errors_dropped_total` | counter |

## 12. Errors reference

All errors wrap their cause and work with `errors.Is` / `errors.As`.

| Error | Meaning |
|-------|---------|
| `ErrInvalidConfiguration` (`*ConfigError`) | invalid configuration; `New` reports every problem at once |
| `ErrConsulUnavailable` | the agent could not be reached |
| `ErrRegistrationFailed`, `ErrDeregistrationFailed` | the agent refused or failed the operation |
| `ErrUnsupportedFeature` (`*UnsupportedFeatureError`) | the connected agent version or edition lacks a feature |
| `ErrServiceNotFound` | no healthy instance matched a discovery query or balancer call |
| `ErrNotRegistered` | maintenance or deregistration before registration |
| `ErrAlreadyStarted`, `ErrAlreadyStopped`, `ErrNotStarted` | lifecycle misuse |
| `kvconfig.ErrInvalid`, `kvconfig.ErrRequired`, `*kvconfig.BindError` | configuration rejected, missing key, wrong type |
| `balancer.ErrClosed` | the balancer or client was stopped |

## 13. Performance

| Path | Runs | Cost |
|------|------|------|
| Health endpoint wrapper | every inbound request | 11 ns, 0 allocations |
| `Balancer.Next` | every outbound call | ~0.2–0.4 µs, 2–4 allocations, independent of the number of instances |
| Configuration binding | every reload | ~4 µs, 904 B |

No goroutine or heap growth under churn (`TestNoLeakUnderChurn` in CI).
Details and methodology: [docs/performance.md](docs/performance.md).

## 14. Documentation

| Document | Content |
|----------|---------|
| [docs/production.md](docs/production.md) | production checklist, TLS, ACL, timeouts, failure modes, Kubernetes |
| [docs/compatibility.md](docs/compatibility.md) | Consul versions, capability matrix and evidence |
| [docs/architecture.md](docs/architecture.md) | design and internals |
| [docs/performance.md](docs/performance.md) | benchmarks, memory and leak testing |
| [docs/frameworks.md](docs/frameworks.md) | net/http, Gin, Echo, Chi, Fiber |
| [docs/migration.md](docs/migration.md) | from hand-written `consul/api` code and from Spring Cloud Consul |
| [docs/status.md](docs/status.md) | definition of done, verification in a real cluster, limitations |
| [docs/release.md](docs/release.md) | CI, security scanning and automatic releases |
| [docs/decisions/](docs/decisions/) | architecture decision records |
| [examples/](examples/) | runnable programs: basic, gin, echo, chi, discovery, config, health |

## 15. Development

```sh
go test ./...                                         # unit tests, no Consul needed
go test -race ./...                                   # race detector
cd integration && CONSUL_VERSION=1.22 go test ./...   # real Consul via Testcontainers
```

Pull requests use Conventional Commits; releases are automatic once CI and
security scans pass on `main`, and only changes to Go files publish a new
version. See [docs/development.md](docs/development.md) and
[docs/release.md](docs/release.md).

## 16. License

[MIT](LICENSE) © jhonsferg
