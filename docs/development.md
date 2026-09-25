# Development

## Repository layout

| Module | Path | Purpose |
| ------ | ---- | ------- |
| `github.com/jhonsferg/consulx` | `/` | the library |
| `.../integration` | `integration/` | tests against real Consul agents (Testcontainers) |
| `.../examples` | `examples/` | runnable examples |
| `.../contrib/prometheus` | `contrib/prometheus/` | Prometheus metrics adapter |
| `.../contrib/otel` | `contrib/otel/` | OpenTelemetry metrics and tracing |
| `.../contrib/fiber` | `contrib/fiber/` | Fiber health endpoints |

Every module except the root uses a `replace` directive pointing at the
working copy, so changes are tested together.

## Running Consul locally

```sh
# Development agent, in memory, UI on http://localhost:8500
docker run -d --name consul -p 8500:8500 hashicorp/consul:1.22 agent -dev -client=0.0.0.0

# Seed configuration
docker exec consul consul kv put config/application/database/host shared-db
docker exec consul consul kv put config/orders-api,prod/database/max-conns 50

# Inspect
curl -s localhost:8500/v1/health/service/orders-api?passing
```

The container must reach your service for HTTP checks. Docker Desktop
provides `host.docker.internal`; on Linux start the agent with
`--add-host host.docker.internal:host-gateway`. Then run services with
`CONSULX_SERVICE_ADDRESS=host.docker.internal`.

## Unit tests

```sh
go vet ./...
go test ./...
```

Unit tests need no network: `internal/fakeconsul` is an in-process fake
agent that implements the endpoints ConsulX uses, including hash- and
index-based blocking and the strict decoding of version-gated fields.
Packages that start goroutines verify leaks with `go.uber.org/goleak`.

## Race detector

```sh
go test -race ./...
```

The race detector needs cgo. On machines without a C toolchain (for example
Windows without MinGW), run it in a container:

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.27 go test -race ./...
```

## Fuzzing

```sh
go test -run '^$' -fuzz FuzzParseConfig -fuzztime 30s .
go test -run '^$' -fuzz FuzzBind -fuzztime 30s ./internal/bind
go test -run '^$' -fuzz FuzzSanitize -fuzztime 30s ./internal/serviceid
go test -run '^$' -fuzz FuzzSplitHostPort -fuzztime 30s ./internal/netaddr
go test -run '^$' -fuzz FuzzParse -fuzztime 30s ./internal/compat
```

## Benchmarks

```sh
go test -run '^$' -bench . -benchmem ./... 
```

Covered: configuration binding (`internal/bind`), instance conversion
(`discovery`), balancer `Next` (`balancer`), the health handler wrapper
(root package).

## Integration tests

They start real agents with Testcontainers and need Docker.

```sh
cd integration
CONSUL_VERSION=1.22 go test ./...          # full suite (about 2.5 minutes)
CONSUL_VERSION=2.0 go test -short ./...    # skips the crash-reaping test
```

`CONSUL_VERSION` selects the `hashicorp/consul` image tag (default `1.22`).
The matrix run before each release:

```sh
for v in 1.20 1.21 1.22 2.0; do CONSUL_VERSION=$v go test ./... || break; done
```

Scenarios covered: registration visible and passing, HTTP and TTL checks
reflecting application health, deregistration on shutdown, crash reaping via
`DeregisterCriticalServiceAfter`, Consul unavailable at start-up with and
without fail-fast, agent restart with re-registration, maintenance mode,
feature gate agreement with the real agent, ACL enforcement with a
least-privilege token (and no token leak in logs), mutual TLS, discovery,
watches and load balancing, layered KV configuration with live and rejected
updates, and the net/http, Gin, Echo and Chi integrations.

## Commit conventions

See the repository's contribution rules. Messages are in English, in the
imperative mood, using `feat`, `fix`, `refactor`, `docs`, `style` or `chore`.
