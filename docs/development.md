# Development

## Repository layout

| Module                         | Path                  | Purpose                                           |
| ------------------------------ | --------------------- | ------------------------------------------------- |
| `github.com/jhonsferg/consulx` | `/`                   | the library                                       |
| `.../integration`              | `integration/`        | tests against real Consul agents (Testcontainers) |
| `.../examples`                 | `examples/`           | runnable examples                                 |
| `.../contrib/prometheus`       | `contrib/prometheus/` | Prometheus metrics adapter                        |
| `.../contrib/otel`             | `contrib/otel/`       | OpenTelemetry metrics and tracing                 |
| `.../contrib/fiber`            | `contrib/fiber/`      | Fiber health endpoints                            |

Every module except the root uses a `replace` directive pointing at the
working copy, so changes are tested together.

## Running Consul locally

```sh
# Development agent, in memory, UI on http://localhost:8500
docker run -d --name consul -p 8500:8500 hashicorp/consul:1.22 agent -dev "-client=0.0.0.0"

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

The quotes around `"-client=0.0.0.0"` are required in PowerShell, which
otherwise splits the argument at the dots, and are harmless in every other
shell.

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

## Formatting and linting

`tools/lint.sh` runs every formatter and linter in Docker with pinned
versions: Prettier and markdownlint-cli2 for Markdown, `golangci-lint fmt`
(gofmt, goimports) and `golangci-lint run` for every module with and without
the `bench` tag, shellcheck for the scripts and actionlint for the
workflows. `-w` formats in place, `-M` also renders the Mermaid diagrams.
Prettier and markdownlint read `.prettierrc.yaml`, `.prettierignore` and
`.markdownlint-cli2.yaml`.

## Fuzzing

```sh
go test -run '^$' -fuzz FuzzParseConfig -fuzztime 30s .
go test -run '^$' -fuzz FuzzBind -fuzztime 30s ./internal/bind
go test -run '^$' -fuzz FuzzSanitize -fuzztime 30s ./internal/serviceid
go test -run '^$' -fuzz FuzzParseListenAddress -fuzztime 30s ./internal/netaddr
go test -run '^$' -fuzz FuzzParse -fuzztime 30s ./internal/compat
```

## Benchmarks

```sh
go test -run '^$' -bench . -benchmem ./...
```

Covered: configuration binding (`internal/bind`), instance conversion and
full health queries (`discovery`), balancer `Next` alone, under the stale
grace and with 32 concurrent callers (`balancer`), readiness probes
(`health`) and the health handler wrapper (root package). Recorded
baselines and what they mean live in [performance.md](performance.md).

Times move ±10% with machine load, so compare only runs taken back to
back on the same machine; allocation counts are exact. On Windows,
`-cpuprofile` has been observed to freeze the test binary (the profile
thread suspends threads while the runtime preempts them); profile on
Linux instead when a run does not finish.

The complete suite, covering every feature and the footprint of a running
client, sits behind the `bench` build tag so CI never compiles or runs it.
Run it with `tools/bench.sh` (`-L` runs it in a Linux container); see
[benchmarks.md](benchmarks.md).

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

## CI and releases

Pull requests, required checks, automatic versioning and publication are
described in [release.md](release.md). Run the same static analysis locally:

```sh
golangci-lint run ./...   # uses .golangci.yml
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## Commit conventions

Messages are in English, in the imperative mood, following Conventional
Commits with the types `feat`, `fix`, `refactor`, `docs`, `style` and
`chore`. Pull request titles use the same format, because squash merges turn
them into the commit subject that drives versioning.
