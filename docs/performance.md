# Performance and memory

## Hot paths

| Path | Runs | Cost | Notes |
|------|------|------|-------|
| Health endpoint wrapper, pass-through | every inbound request | 11 ns, **0 allocations** | one map lookup in front of the application router |
| `Balancer.Next`, 3 instances | every outbound call | 231 ns, 112 B, 2 allocations | cost does not depend on the number of instances |
| `Balancer.Next`, 20 instances | every outbound call | 409 ns, 464 B, 4 allocations | only the selected instance is copied |
| `discovery.Watch.Pick` | per selection | 271 ns, 448 B, 4 allocations | |
| Readiness probe, 3 components | every health check (seconds) | 8 µs, 3.7 KB, 41 allocations | mostly `net/http` and `encoding/json` |
| Configuration binding | every configuration reload | 4 µs, 904 B, 24 allocations | |
| Health entry conversion | per instance, per discovery change | 234 ns, 464 B, 4 allocations | |

Measured with `go test -bench . -benchmem` (Go 1.27, averages of three runs).
Reproduce with:

```sh
go test -run '^$' -bench . -benchmem ./balancer ./health ./discovery ./internal/bind .
```

## Improvements in this release

| Path | Before | After |
|------|--------|-------|
| `Balancer.Next`, 20 instances | 6.5 µs, 15,808 B, 81 allocations | 0.41 µs, 464 B, 4 allocations |
| `Balancer.Next`, 3 instances | 695 ns, 1,360 B, 7 allocations | 231 ns, 112 B, 2 allocations |
| Configuration binding | 59 µs, 324,900 B, 229 allocations | 4.2 µs, 904 B, 24 allocations |
| Readiness probe | 9.1 µs, 4,681 B, 55 allocations | 8.1 µs, 3,731 B, 41 allocations |

- `Balancer.Next` used to deep-copy every instance on every call and then
  pick one. `discovery.Watch.Pick` hands the selector the immutable snapshot
  and copies only the selected instance.
- Binding normalised field names with a `strings.Replacer` built on every
  call, which accounted for 97% of its allocations. Names are now compared
  in place.
- Probes no longer create a second deadline and a helper goroutine per
  component.

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
