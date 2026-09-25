# ConsulX Architecture

Status: **proposal** (Architecture & Capability Discovery). Evidence for every
Consul-related claim is in [compatibility.md](compatibility.md).

## 1. Scope

ConsulX is an application integration layer on top of the official client
`github.com/hashicorp/consul/api`. It owns what every Go microservice
re-implements by hand: resolving its own address, registering, health
endpoints, heartbeats, re-registration, deregistration, discovery, client-side
load balancing and layered configuration from KV. Everything else in Consul is
reachable through `Client.Raw()`.

Functional benchmark: Spring Cloud Consul (discovery + distributed config).
Programming model: plain Go (structs, functional options, `context.Context`,
channels, errors). No container, no annotations, no global state.

```text
Microservice
   │  *http.Server, context.Context
   ▼
consulx.Client ─────────────── Raw() ──► *api.Client (escape hatch)
   │
   ├── runtime supervisor (one per Client)
   │     ├── registrar     register, detect loss, re-register, deregister
   │     ├── heartbeat     TTL checks only
   │     └── watchers      discovery / config / load balancer caches
   ├── internal/compat     agent version + edition → feature gate
   ├── internal/backoff    exponential, full jitter, capped
   └── official client ──► Consul agent HTTP API (/v1)
```

## 2. Module and package layout

The core module must not pull in web frameworks, Prometheus, OpenTelemetry or
Testcontainers. Anything with a heavy dependency lives in its own Go module.

```text
github.com/jhonsferg/consulx            core module
├── consulx (root)      Client, Config, Option, lifecycle, registration,
│                        health checks, errors, AddressResolver, Metrics
├── health/             Status (UP/DEGRADED/DOWN), Provider, HTTP handlers
├── discovery/          query builder, ServiceInstance, Watch, events
├── balancer/           Balancer interface, RoundRobin, Random, Weighted
├── kvconfig/           layered KV config, binding, generic Watcher[T]
├── kv/                 context-first KV helpers (namespace/dc defaults)
├── internal/compat     version detection, feature gate
├── internal/backoff    retry policy implementation
├── internal/bind       reflection binder (tree → struct), fuzzed
├── internal/netaddr    host:port parsing, interface/route IP discovery
├── internal/serviceid  ID generation and sanitising
└── internal/fakeconsul in-process fake agent (httptest) for unit tests

github.com/jhonsferg/consulx/integration      separate module: testcontainers
github.com/jhonsferg/consulx/contrib/prometheus separate module (Phase 8)
github.com/jhonsferg/consulx/contrib/otel       separate module (Phase 8)
github.com/jhonsferg/consulx/contrib/fiber      separate module (Phase 9)
examples/                                        separate module
```

Why not one package per Consul API (`acl/`, `catalog/`, `session/`...)?
Those would be thin copies of the official client with no added behaviour.
They are reachable with `Raw()` and listed as `Low-level supported`. A package
is added only when ConsulX contributes real behaviour (policy, lifecycle,
decoding, caching).

Why no Gin/Echo/Chi adapters by default? All three produce an `http.Handler`,
so the generic integration already covers health injection and registration
for them. They get examples and tests, not packages. An adapter is created
only where `net/http` is not enough: Fiber (fasthttp) is the only confirmed
case. Route metadata via adapters is deferred until a concrete use appears.

## 3. Public API (initial)

```go
// Construction never touches the network. It validates, normalises and
// wraps the server handler. It returns an error instead of panicking.
func New(opts ...Option) (*Client, error)

type Option interface{ apply(*settings) error }

// Config is itself an Option, so struct-based and option-based
// configuration compose: New(cfg, WithServer(srv)).
type Config struct {
    Consul    ConsulConfig    // address, scheme, datacenter, token, TLS, timeouts
    Service   ServiceConfig   // name, id, address, port, tags, meta, weights...
    Health    HealthConfig    // endpoints, check kind, interval, timeout, TTL...
    Retry     RetryConfig
    Lifecycle LifecycleConfig // AutoRegister, FailFast, StartTimeout, ShutdownTimeout
}

func LoadConfig(path string) (Config, error)   // YAML or JSON, then env overlay
func ConfigFromEnv() (Config, error)

type Client struct{ /* unexported */ }

func (c *Client) Start(ctx context.Context) error // register (per FailFast), launch runtime
func (c *Client) Stop(ctx context.Context) error  // stop workers, deregister, wait
func (c *Client) Run(ctx context.Context) error   // Start, block on ctx, Stop
func (c *Client) Done() <-chan struct{}           // closed after Stop completes
func (c *Client) Errors() <-chan error            // async runtime errors, non-blocking
func (c *Client) State() State                    // Idle, Starting, Running, Degraded, Stopping, Stopped

func (c *Client) Registration() Registration       // effective service ID, address, port
func (c *Client) Health() *health.Registry         // app reports UP/DEGRADED/DOWN
func (c *Client) Discovery() *discovery.Client
func (c *Client) Balancer(s balancer.Strategy) *balancer.Balancer
func (c *Client) Config() *kvconfig.Loader
func (c *Client) KV() *kv.Client
func (c *Client) Raw() *api.Client
```

Differences from the conceptual API in the brief, with reasons:

| Brief                                 | Proposal                                   | Reason |
| ------------------------------------- | ------------------------------------------ | ------ |
| `consul := consulx.New(...)`          | `consul, err := consulx.New(...)`          | Invalid configuration must be reported, not panic. |
| `consul.Config().Watch(ctx, &cfg)`    | `kvconfig.Watch[T](ctx, loader, ...)`      | Writing into a caller-owned struct from a watcher goroutine is a data race. The watcher publishes immutable `T` values instead; `Current()` returns the last accepted one. Methods cannot be generic in Go, hence a function. |
| `consul.LoadBalancer(RoundRobin())`   | `consul.Balancer(balancer.RoundRobin())`   | Same shape; the `balancer` package keeps the root small. |

Everything else keeps the brief's shape, including
`consul.Discovery().Service("payments").Passing().Datacenter("dc1").All(ctx)`
and `First(ctx)`.

## 4. HTTP integration

* Input is `*http.Server`. ConsulX never creates, starts or stops it.
* In `New`, if health endpoints are enabled, `server.Handler` is replaced by a
  small wrapper that serves the configured exact paths and delegates every
  other request to the original handler (or `http.DefaultServeMux` when nil).
  This happens in `New` because `net/http` reads `Server.Handler` on every
  request: changing it after `ListenAndServe` starts is a data race.
  Documented requirement: call `New` before the server starts serving.
* Health status mapping uses Consul's HTTP check semantics:
  `UP → 200` (passing), `DEGRADED → 429` (warning), `DOWN → 503` (critical).
  Response body is small JSON with component details; details can be hidden.
* Endpoints are opt-in and configurable: `/health` (aggregate),
  `/health/live` (process alive, never depends on dependencies),
  `/health/ready` (dependencies ready; this is what Consul checks by default).
* Port is taken from `server.Addr`. Host is only trusted when it is a concrete
  routable IP. When the port is `0`, `WithListener(net.Listener)` supplies the
  real one.

### 4.1 Address resolution

`AddressResolver` is an interface; the default is a chain, first success wins:

1. Explicit `Service.Address` / `WithServiceAddress`.
2. Environment variable named by config (default `CONSULX_SERVICE_ADDRESS`;
   Kubernetes users map `status.podIP` into it via the Downward API).
3. Host from `server.Addr` when it is a concrete, non-wildcard IP.
4. Route to Consul: the local IP the kernel would use to reach the Consul
   address (UDP "connect", no packets sent). Works in Docker, Compose, pods
   and VMs because it follows the actual route.
5. First private, up, non-loopback interface address (skipping known virtual
   bridges). IPv4 preferred unless configured otherwise.

Wildcards (`0.0.0.0`, `::`) are always rejected. Loopback is rejected unless
`AllowLoopback` is set (useful when app and agent share one host and all
consumers are local).

## 5. Registration

* Agent API only (`/v1/agent/service/register` with
  `replace-existing-checks=true`), never Catalog registration, because the
  agent performs anti-entropy.
* Default service ID: `<name>-<hostname>-<port>`, lower-cased and sanitised.
  Rationale: unique per instance (a host cannot bind the same port twice;
  containers and pods get unique hostnames), and stable across restarts, so a
  restarted process replaces its own entry instead of leaving an orphan.
  `IDStrategy` can switch to `<name>-<uuid>`; `WithServiceID` wins over both.
* Automatic metadata (opt-out, never overwrites a key the user set explicitly,
  documented rule: user keys win): `consulx_version`, `hostname`,
  `go_version`, `language=go`, plus `version`, `environment`, `zone` when
  configured. Scheme is published as `secure=true|false` (Spring-compatible).
* Before sending, the definition is checked against the feature gate
  (e.g. `Ports` requires ≥ 1.22, Namespace/Partition require Enterprise).
* Default check: HTTP against the readiness endpoint, interval 10s (Spring
  Cloud Consul default), timeout 5s (must be below interval; Consul's own
  default of 10s would equal it), `DeregisterCriticalServiceAfter` 1m (the
  Consul minimum; the reaper runs periodically so removal happens within
  roughly 1m-1m30s). False positives are self-healing because the runtime
  re-registers a missing service.

## 6. Runtime and lifecycle

```text
Idle ─Start─► Starting ──registered──► Running ◄──┐
                 │                        │       │ re-registered
                 │ FailFast=false         ▼       │
                 └───────────────────► Degraded ──┘  (backoff retries)
Running/Degraded ─ctx done or Stop─► Stopping ─► Stopped (Done closed)
```

* One supervisor goroutine per `Client`, children started with
  `errgroup.WithContext`. Each goroutine is documented with its purpose and
  exit condition, and every one exits when the runtime context is cancelled.
* **Loss detection without aggressive polling:** the registrar performs a
  blocking query on `GET /v1/agent/service/:id` (hash-based blocking). A `404`
  means the agent lost the service (agent restart, reaper) → re-register.
  Transport errors → `Degraded`, exponential backoff, re-register on recovery.
* **TTL mode:** heartbeat every TTL/3 reporting the `health.Registry` status
  (pass/warn/fail). A "check not found" response triggers re-registration.
* **FailFast=true:** `Start` retries within `StartTimeout` (default 30s) and
  returns `ErrConsulUnavailable` / `ErrRegistrationFailed` if it cannot
  register. **FailFast=false (default):** `Start` returns once the runtime is
  launched; registration continues in the background. Default chosen for
  availability: a Consul outage should not stop an already healthy service
  from starting. Config loading is not affected; its errors are always
  returned to the caller.
* **Shutdown:** stop watchers and heartbeat, then deregister with a context
  derived from `context.WithoutCancel(parent)` bounded by `ShutdownTimeout`
  (default 10s). This is the one documented use of a detached context:
  deregistration must run *after* the caller's context was cancelled.
  `DeregisterCriticalServiceAfter` covers SIGKILL, OOM and host loss.
* Signals are never captured. The application passes a context from
  `signal.NotifyContext`.
* `Errors()` is buffered; sends never block. When full, the error is dropped
  and counted (`consulx_errors_dropped_total`). Everything is also logged.
* A `Client` is single-use: `Start` twice → `ErrAlreadyStarted`, after `Stop`
  → `ErrAlreadyStopped`. `Stop` is idempotent-safe to call concurrently.

## 7. Retry

`RetryConfig{InitialDelay: 500ms, MaxDelay: 30s, Multiplier: 2, Jitter: Full,
MaxAttempts: 0 (unlimited), AttemptTimeout: 10s}`. Full jitter is used to
avoid synchronised reconnect storms when many instances lose the same agent.
Every wait selects on the context. `RetryPolicy` is an interface so users can
plug their own.

## 8. Discovery and load balancing

* Built on `Health().ServiceMultipleTags` (health-aware), never on Catalog for
  instance selection.
* **Default filter is `passing` only.** Returning critical instances by default
  is the unsafe choice for callers; `.AnyStatus()` opts out. `.Passing()` is
  kept as an explicit no-op for readability.
* `ServiceInstance` keeps: ID, Name, Address (service address, falling back to
  node address), Port, Ports, Scheme (from `secure` meta), Tags, Meta,
  TaggedAddresses, Weights, Datacenter, Namespace, Partition, Node (ID, name,
  address, meta), aggregated Status and the raw checks.
* Watches use blocking queries with `WaitIndex`/`WaitTime` (default 5m),
  following Consul's index rules: reset to 0 when the index goes backwards,
  never wait on index 0, enforce a minimum interval between calls, back off on
  errors. Events carry the full snapshot plus added/removed/changed sets.
  Delivery coalesces: a slow consumer receives the latest snapshot, never a
  backlog, and the watcher never blocks on a full channel.
* `Balancer` is backed by one shared watch per service (lazily started, tied
  to the Client lifecycle), so `Next` is a memory read, not a Consul request.
  Strategies: RoundRobin, Random, Weighted (Consul `Weights.Passing` /
  `Weights.Warning`). `Strategy` is an interface.

## 9. Distributed configuration (KV)

* Layout is Spring-compatible by default, lowest to highest precedence:
  `config/application/`, `config/application,<profile>/`,
  `config/<service>/`, `config/<service>,<profile>/`. Prefix, default context
  and separator are configurable.
* Formats: `KeyValue` (default, key path segments map to fields), `YAML` and
  `JSON` (single `data` key per context).
* Binding (`internal/bind`): string, bool, ints, uints, floats,
  `time.Duration`, `encoding.TextUnmarshaler`, slices, maps, nested structs,
  pointers. Tags: `consul:"name"`, `consul:"name,required"`,
  `default:"..."`. Unknown keys are ignored; type errors report the full key
  path. No `unsafe`, no unexported field writes.
* Validation: if the type implements `Validate() error` it is called; an
  extra validator function can be supplied.
* Dynamic config: `kvconfig.Watch[T]` watches every layer with blocking
  queries, rebuilds a fresh `T`, validates, and only then publishes it.
  Invalid changes are rejected, logged ("configuration rejected"), counted,
  and the previous value remains current.

## 10. Configuration precedence

Lowest to highest; later layers override only fields they set:

1. Built-in defaults (documented in `defaults.go`, each with a reason).
2. File via `LoadConfig` (YAML/JSON).
3. Environment: the official `CONSUL_*` variables (handled by the official
   client) plus `CONSULX_*` for service-level fields.
4. `Config` struct passed to `New`.
5. Functional options, applied in argument order.

A `Config` passed to `New` is a full layer, but zero values do not override
earlier layers (Go cannot distinguish "unset" from "zero"). Where zero is a
meaningful value, the field is a pointer or the option must be used; this is
documented per field.

## 11. Observability

* Logging: `*slog.Logger` via `WithLogger`; default `slog.Default()`. Event
  names follow the brief. Tokens, TLS keys and KV values are never logged;
  the ACL token type has a redacting `String`/`LogValue`.
* Metrics: small `Metrics` interface in the core, no-op by default. The
  `contrib/prometheus` and `contrib/otel` modules implement it.
* Tracing: `WithHTTPClient`/`WithTransport` lets users install `otelhttp`;
  the `contrib/otel` module provides a helper.

## 12. Errors

Sentinels: `ErrInvalidConfiguration`, `ErrAlreadyStarted`,
`ErrAlreadyStopped`, `ErrNotRegistered`, `ErrConsulUnavailable`,
`ErrRegistrationFailed`, `ErrDeregistrationFailed`, `ErrUnsupportedFeature`,
`ErrServiceNotFound`. Typed errors: `*ConfigError{Field, Reason}`,
`*UnsupportedFeatureError{Feature, Required, Actual}`, `*BindError{Key,
Type, Err}`. All wrap causes with `%w`, compatible with `errors.Is/As`.

## 13. Testing strategy

| Layer        | Tooling                                         | Covers |
| ------------ | ----------------------------------------------- | ------ |
| Unit         | `internal/fakeconsul` (httptest fake agent)     | options, config, IDs, resolver, handler wrapping, registration payloads, retry, discovery decoding, binding, lifecycle state machine |
| Race         | `go test -race ./...` on every change           | all |
| Leaks        | `go.uber.org/goleak` in `TestMain` of each package with goroutines | start, stop, cancel, retry, watch cancellation |
| Integration  | separate module, Testcontainers-Go, real Consul | registration visible + passing, deregistration, DeregisterCriticalServiceAfter reaping, FailFast both modes, Consul down/up reconnect with re-registration, watches, KV config, TLS, ACL |
| Version matrix | `CONSUL_VERSION=1.21 go test ./...` in `integration/`, CI matrix 1.20 / 1.21 / 1.22 / 2.0 | compat gate |
| Fuzz         | `go test -fuzz`                                  | bind, address parsing, service ID sanitising, config parsing, health path matching |
| Benchmarks   | `testing.B`                                      | binding, balancer `Next`, handler wrapper, metadata build |
| Examples     | runnable `examples/` module, smoke-tested against Docker Consul | net/http, Gin, Echo, Chi, discovery, config, watch, TLS, ACL |

## 14. Implementation plan

Each phase ends with `go fmt`, `go vet`, `go test ./...`, `go test -race ./...`
and, from Phase 3 on, the integration suite.

1. **Foundation**: errors, Config/Option normalisation, defaults, env and file
   loading, official client construction (address, TLS, token, token file,
   timeouts), `internal/backoff`, `internal/compat`, logger, metrics
   interface, ADRs 1, 2, 5, 6, 11.
2. **HTTP integration**: `health` package, handler wrapper, address resolver,
   port resolution, lifecycle skeleton (states, Start/Stop/Run/Done/Errors).
3. **Registration**: definition builder, checks (HTTP/TCP/TTL/gRPC), metadata,
   service ID, register/deregister, maintenance; first integration tests.
4. **Reliability**: loss detection, heartbeat, reconnect, re-registration,
   FailFast, shutdown ordering, leak tests.
5. **Discovery**: builder, instance model, watches, balancer.
6. **Configuration**: `kv`, `internal/bind`, `kvconfig` layers, formats, watch.
7. **Low-level access**: `Raw()` documentation and matrix per API group.
8. **Observability**: slog events, metrics, `contrib/prometheus`, `contrib/otel`.
9. **Frameworks**: Gin/Echo/Chi examples and tests; `contrib/fiber`.
10. **Hardening**: fuzz, benchmarks, compatibility matrix from CI evidence,
    README, production guide, migration guide, CHANGELOG, `v0.1.0`.

## 15. Known risks

* Health endpoint injection requires `New` before `ListenAndServe`; misuse is
  detectable only partially (documented, and `WithHealthHandler` returns the
  handler for manual mounting as an alternative).
* Address auto-detection cannot be right in every network topology; the
  resolver chain is explicit and logged, and explicit configuration wins.
* The official client adds fields ahead of agent support (`AI`). The feature
  gate must be kept up to date with each client upgrade; a test asserts that
  every non-zero registration field is either gated or known-safe.
* Enterprise-only paths (namespaces, partitions) cannot be integration tested
  without an Enterprise license; they will be labelled accordingly.
