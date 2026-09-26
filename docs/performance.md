# Performance and memory

## Hot paths

| Path                                             | Runs                                    | Cost                            | Notes                                                                  |
| ------------------------------------------------ | --------------------------------------- | ------------------------------- | ---------------------------------------------------------------------- |
| Health endpoint wrapper, pass-through            | every inbound request                   | 11 ns, **0 allocations**        | one map lookup in front of the application router                      |
| `Balancer.Next`, 3 instances                     | every outbound call                     | 230 ns, 112 B, 2 allocations    | cost does not depend on the number of instances                        |
| `Balancer.Next`, 20 instances                    | every outbound call                     | 430 ns, 464 B, 4 allocations    | only the selected instance is copied                                   |
| `Balancer.Next`, 32 concurrent callers           | every outbound call                     | 231 ns, 112 B, 2 allocations    | the same as single-threaded: nothing on this path takes a lock         |
| `Balancer.NextEndpoint`, any number of instances | every outbound call                     | 96 ns, **0 allocations**        | address, port, scheme, `HostPort` and `URL`, formatted once per change |
| `discovery.Watch.Pick`                           | per selection                           | 271 ns, 448 B, 4 allocations    |                                                                        |
| `discovery.Snapshot.PickEndpoint`                | per selection                           | 8 ns, **0 allocations**         |                                                                        |
| Metric calls, any backend                        | every registration, heartbeat and query | **0 allocations**               | label sets, series and attribute sets are cached                       |
| Readiness probe, 3 components                    | every health check (seconds)            | 7.5 µs, 3.3 KB, 38 allocations  | mostly `net/http` and `encoding/json`                                  |
| Health query, 20 instances                       | per request that lists instances        | 1.9 ms, 194 KB, 821 allocations | HTTP round trip, JSON decoding, conversion and ordering                |
| Configuration binding                            | every configuration reload              | 4.2 µs, 904 B, 24 allocations   |                                                                        |
| Health entry conversion                          | per instance, per discovery change      | 103 ns, 96 B, 1 allocation      |                                                                        |

Measured with `go test -bench . -benchmem` (Go 1.27, three runs each).
Allocation counts are exact; times move ±10% with machine load, so only
back-to-back measurements on the same machine are comparable.
Reproduce with:

```sh
go test -run '^$' -bench . -benchmem ./balancer ./health ./discovery ./internal/bind .
```

## Improvements in this release

| Path                                   | Before                                   | After                           |
| -------------------------------------- | ---------------------------------------- | ------------------------------- |
| `Balancer.Next`, 20 instances          | 6.5 µs, 15,808 B, 81 allocations         | 0.43 µs, 464 B, 4 allocations   |
| `Balancer.Next`, 3 instances           | 695 ns, 1,360 B, 7 allocations           | 230 ns, 112 B, 2 allocations    |
| `Balancer.Next`, 32 concurrent callers | 268 ns/op with a shared lock on the path | 231 ns/op, lock-free            |
| Configuration binding                  | 59 µs, 324,900 B, 229 allocations        | 4.2 µs, 904 B, 24 allocations   |
| Readiness probe                        | 9.1 µs, 4,681 B, 55 allocations          | 7.5 µs, 3,345 B, 38 allocations |
| Health entry conversion                | 234 ns, 464 B, 4 allocations             | 103 ns, 96 B, 1 allocation      |
| Health query, 20 instances             | 884 allocations                          | 821 allocations                 |

- `Balancer.Next` used to deep-copy every instance on every call and then
  pick one. `discovery.Watch.Pick` hands the selector the immutable snapshot
  and copies only the selected instance.
- Binding normalised field names with a `strings.Replacer` built on every
  call, which accounted for 97% of its allocations. Names are now compared
  in place.
- Probes no longer create a second deadline and a helper goroutine per
  component.
- The balancer and the watch publish their state through atomic pointers
  instead of shared locks, so `Next` and `Pick` never contend with each
  other, a selection reads the clock once, and stale grace is kept
  without a lock. The 268 ns/op figure was measured back to back with
  the previous design's locks put back in place (ten samples per
  variant); the locks also make results less stable (240–295 ns/op
  against 210–253 ns/op).
- Health entry conversion adopts the maps and slices the Consul client
  already decodes: a freshly decoded response belongs to nobody else, so
  cloning it did the same work twice. One instance went from 464 B and
  4 allocations to 96 B and 1, in less than half the time.
- Instance lists are sorted by comparing identifiers directly instead of
  building a temporary key string (CPU only: the old comparator never
  allocated).

### Allocation-free request path

Measured against v0.2.0 with the local benchmark suite
([benchmarks.md](benchmarks.md), medians of six runs back to back):

| Path                                                       | v0.2.0                           | This release                    |
| ---------------------------------------------------------- | -------------------------------- | ------------------------------- |
| Outbound call, address only (`Balancer.NextEndpoint`, new) | `Next` + `URL`: 6 allocations    | 96 ns, **0 allocations**        |
| `Balancer.Next`, weighted, 100 instances                   | 2,902 ns                         | 717 ns                          |
| Metric call with a label, no backend                       | 32 B, 1 allocation               | **0 allocations**               |
| Metric call with a label, Prometheus                       | 70 ns, 16 B, 1 allocation        | 35 ns, **0 allocations**        |
| Metric call with a label, OpenTelemetry                    | 266 ns, 232 B, 5 allocations     | 154 ns, **0 allocations**       |
| Watch event diff, 20 instances, one changed                | 34 µs, 39,784 B, 340 allocations | 11 µs, 18,040 B, 89 allocations |
| Request options, metadata and filter query                 | 865 ns, 689 B, 21 allocations    | 76 ns, 224 B, 1 allocation      |
| `Client.State`, 32 concurrent readers                      | 28.5 ns                          | 0.03 ns                         |
| Service definition (every registration)                    | 10.1 µs, 15 allocations          | 1.0 µs, 13 allocations          |
| `/health/ready` endpoint                                   | 2,675 B, 33 allocations          | 2,288 B, 28 allocations         |

- `Balancer.NextEndpoint` and `discovery.Snapshot.PickEndpoint` return an
  `Endpoint` whose `HostPort` and `URL` are formatted once per change of the
  service, so the request path allocates nothing. `Next` keeps returning an
  independent copy of the whole instance.
- The built-in strategies select by index, and the weighted strategy no
  longer copies every instance while it walks the list.
- Metric label sets are built once; the Prometheus and OpenTelemetry
  adapters cache their series and attribute sets.
- The lifecycle state is an atomic value; the filter expression of a query
  is built when the query is defined; watch events compare instances with a
  typed comparison instead of reflection; the host name is read once per
  process; health responses share their header values.

## Memory leaks

Two unbounded growth paths were found and fixed:

1. **Hung health checkers.** A checker that ignores its context and never
   returns used to leave one stuck goroutine per probe; with probes every few
   seconds that grows without bound. Concurrent and successive probes now
   share the execution in flight, so a component never has more than one
   running check, and a result produced after the deadline counts as a
   timeout.
2. **Balancer watches.** Every distinct service name asked for kept a watch
   (a goroutine and a connection) until `Close`. Services unused for
   `balancer.DefaultIdleTimeout` (15 minutes) now release their watch; the
   next call starts a new one.

`TestNoLeakUnderChurn` runs on every CI build: it drives a complete client
through 500 cycles (50 re-registrations, 500 instance changes, 500
configuration reloads, 10,000 balancer calls) after a warm-up and requires
the number of ConsulX goroutines to stay identical and the live heap to grow
by less than 512 KiB. Typical result: 7 → 7 goroutines, +4 to +23 KiB of
heap. Idle HTTP keep-alive connections are excluded from the goroutine
count: the pool fills up to its per-host limit and stays there.

The ten-minute chaos run in a real cluster (see [status.md](status.md))
showed the same behaviour: flat goroutine counts and a heap oscillating
between 1 and 3.3 MB.

## Footprint of an idle client

The client of the leak test runs 7 goroutines: the registrar, the TTL
heartbeat, one discovery watch and the janitor of its balancer, and a
configuration watcher with one watcher per KV folder (two here). Each
blocking query also holds one HTTP connection. Blocking queries wait on the server, so an idle
client uses no CPU.
