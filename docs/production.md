# Production guide

## Checklist

- [ ] TLS towards Consul with certificate verification on.
- [ ] A dedicated ACL token per service, least privilege, from a file.
- [ ] The registered address is explicit or its source is verified in logs
      (`address_source` in the "service registered" log line).
- [ ] Health checks probe readiness, with a timeout below the interval.
- [ ] `DeregisterCriticalServiceAfter` set (default 1 minute).
- [ ] The application passes a context cancelled on SIGTERM and gives
      `Run` time to deregister before the process exits.
- [ ] Logs and metrics exported.

## TLS

```go
consulx.WithConsulAddress("https://127.0.0.1:8501")
consulx.WithTLS(consulx.TLSConfig{
	CAFile:   "/etc/consul/tls/ca.pem",
	CertFile: "/etc/consul/tls/client.pem", // when the agent sets verify_incoming
	KeyFile:  "/etc/consul/tls/client-key.pem",
	ServerName: "localhost",               // name in the agent certificate
})
```

The standard variables work as well: `CONSUL_HTTP_ADDR`, `CONSUL_HTTP_SSL`,
`CONSUL_CACERT`, `CONSUL_CLIENT_CERT`, `CONSUL_CLIENT_KEY`,
`CONSUL_TLS_SERVER_NAME`. Never ship `InsecureSkipVerify`; ConsulX logs a
warning when it is on.

## ACL

Minimal policy for a service named `orders-api` that also discovers others
and reads configuration:

```hcl
service "orders-api" { policy = "write" }
service_prefix ""    { policy = "read" }
node_prefix ""       { policy = "read" }
key_prefix "config/application" { policy = "read" }
key_prefix "config/orders-api"  { policy = "read" }
```

Provide it with `WithTokenFile` (or `CONSUL_HTTP_TOKEN_FILE`) so the token is
not in the process environment. The file is read when the Client is created.
Without `agent:read`, ConsulX cannot read the agent version; optional fields
are then validated by the agent itself, which is safe but gives less precise
errors.

## Timeouts

| Setting                              | Default  | Notes                                      |
|--------------------------------------|----------|--------------------------------------------|
| `Consul.DialTimeout`                 | 5s       | TCP connect                                |
| `Consul.RequestTimeout`              | 10s      | every non-blocking request                 |
| `Consul.WaitTime`                    | 5m       | blocking queries (Consul's maximum is 10m) |
| `Health.Interval` / `Health.Timeout` | 10s / 5s | the timeout must not exceed the interval   |
| `Health.TTL`                         | 30s      | heartbeat every TTL/3                      |
| `Lifecycle.StartTimeout`             | 30s      | fail-fast start-up only                    |
| `Lifecycle.ShutdownTimeout`          | 10s      | deregistration in `Run`                    |

Keep the orchestrator's grace period (Kubernetes
`terminationGracePeriodSeconds`) above `ShutdownTimeout` plus your server's
own drain time.

## Retry and failure modes

- Consul down at start-up: with `FailFast(false)` (default) the service
  starts, reports `degraded`, and registers when Consul returns. With
  `FailFast(true)` `Start` fails after `StartTimeout`; permission and
  validation errors fail immediately.
- Consul down while running: the runtime goes `degraded`, retries with
  exponential backoff (500ms up to 30s, jittered) and returns to `running`.
- Agent restarted without state: detected by a blocking watch on the
  agent's view of the service; the service is re-registered.
- Process killed (SIGKILL, OOM, node loss): the check turns critical and
  Consul removes the instance after `DeregisterCriticalServiceAfter`
  (verified by the integration suite: about 90 seconds with the 1 minute
  default, because Consul reaps periodically).

Read `consul.Errors()` or watch `consulx_runtime_state` to alert on
prolonged `degraded` states.

## Client-side load balancing

When the local Consul agent restarts, it reports its services critical
until their checks run again, so for a few seconds discovery returns no
healthy instance although every instance is alive (measured: 4 to 6
seconds, about 35 failed calls at 5 calls per second). `consul.Balancer` bridges that gap by default with a 10 second grace period
(`DefaultStaleGrace`): within it, an empty list is replaced by the last
non-empty one. Measured over five agent restarts, callers without the grace
period failed 24% of their calls (173 of 716); with it, none failed.
`balancer.WithStaleGrace(0)` restores the strict behaviour; balancers built
directly with `balancer.New` have no grace period unless requested. While Consul is completely unreachable, balancers keep serving the
last known instances regardless of this option.

## Health checks

- Point Consul at readiness (the default). Keep liveness free of external
  dependencies so a database outage does not restart every pod.
- `DEGRADED` becomes Consul `warning`: the instance is excluded from
  passing-only discovery but stays registered. If Kubernetes probes use the
  same endpoints, set `DegradedStatusCode: 200`.
- During `Stop`, readiness turns DOWN before deregistration so load
  balancers stop sending traffic.
- Use `FailuresBeforeCritical` to damp flapping checks.

## Datacenters and namespaces

`WithDatacenter` applies to every request. Discovery can target another
datacenter per query with `.Datacenter("dc2")`. Namespaces and admin
partitions are Consul Enterprise features: set them only on Enterprise;
ConsulX refuses them against Community Edition agents instead of failing
later.

## Docker and Kubernetes

**Docker / Compose**: the default resolver uses the local IP that routes to
the Consul agent, which is the container IP on the shared network. Set
`CONSULX_SERVICE_ADDRESS` when consumers reach the service through another
address (published ports, host networking).

**Kubernetes** with a node-local agent (DaemonSet):

```yaml
env:
  - name: HOST_IP
    valueFrom: { fieldRef: { fieldPath: status.hostIP } }
  - name: POD_IP
    valueFrom: { fieldRef: { fieldPath: status.podIP } }
  - name: CONSUL_HTTP_ADDR
    value: http://$(HOST_IP):8500
  - name: CONSUL_HTTP_TOKEN_FILE
    value: /var/run/secrets/consul/token
```

```go
consulx.Config{Service: consulx.ServiceConfig{AddressEnv: "POD_IP"}}
```

The pod name is the host name, so default service IDs are unique per pod.
When Consul's own Kubernetes integration (catalog sync or the mesh
injector) already registers pods, do not also register them with ConsulX:
use `WithAutoRegister(false)` and keep ConsulX for discovery and
configuration.

## Resource usage

Per Client: one registrar goroutine holding one long-poll HTTP connection;
one heartbeat goroutine for TTL checks; one goroutine and connection per
discovery watch, balancer service and configuration folder. Idle blocking
queries cost no CPU. The HTTP transport is private to the Client and its
idle connections are closed on `Stop`.

## Observability

Structured logs (`log/slog`) name every lifecycle event. Metrics are listed
in the README; the most useful alerts are
`consulx_runtime_state == 3` (degraded) for several minutes, and increases
of `consulx_register_errors_total` and `consulx_config_reload_errors_total`.

## Security

- Tokens are `consulx.Secret` values: redacted in fmt, slog, JSON and YAML.
- Configuration values read from KV are never logged.
- Health responses list component names and details; use `HideDetails`
  when the endpoints are reachable by untrusted clients.
- TLS verification is on by default; there is no silent downgrade to HTTP
  when TLS is configured.
