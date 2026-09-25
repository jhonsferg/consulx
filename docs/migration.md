# Migration guide

## From hand-written `consul/api` code

| Before | With ConsulX |
| ------ | ------------ |
| `api.NewClient(api.DefaultConfig())` | `consulx.New(...)`; the official client is `consul.Raw()` |
| Building `AgentServiceRegistration`, guessing the IP | `WithServer(server)`, `WithServiceName(name)`; address resolved and logged |
| Writing a `/health` handler | `WithAutoHealth()` and `consul.Health().Register(...)` |
| `Agent().ServiceRegister` in `main` | `consul.Run(ctx)` |
| Deregister in a SIGTERM handler | cancel the context passed to `Run` |
| Periodic `UpdateTTL` goroutine | `HealthConfig{Check: consulx.CheckTTL}`; status follows `Health()` |
| Retry loops, re-registration after agent restarts | built in |
| `Health().Service(name, tag, true, nil)` | `consul.Discovery().Service(name).Tag(tag).All(ctx)` |
| Blocking-query loops with `WaitIndex` | `consul.Discovery().Watch(ctx, name)` |
| `KV().List` + manual parsing | `consul.Config().LoadInto(ctx, &cfg)` or `kvconfig.Watch[T]` |

Steps:

1. Keep your `*http.Server`; create the ConsulX client **before** it starts
   serving and pass it with `WithServer`.
2. Replace the registration and deregistration code with `consul.Run(ctx)`
   using the context you already cancel on SIGTERM.
3. Keep the same service ID if other systems depend on it (`WithServiceID`);
   otherwise the default `<name>-<hostname>-<port>` is used.
4. Move any registration field ConsulX does not model (Connect sidecar,
   proxy, kind, locality) into `WithRegistrationHook`.
5. Move remaining direct API calls to `consul.Raw()` so they share the
   connection settings, and add `QueryOptions.WithContext(ctx)`.

## From Spring Cloud Consul

| Spring Cloud Consul | ConsulX |
| ------------------- | ------- |
| `spring.application.name` | `Service.Name` |
| `spring.cloud.consul.host/port/scheme` | `Consul.Address` / `CONSUL_HTTP_ADDR` |
| `discovery.instance-id` (default `name:profiles:port`) | `Service.ID` (default `<name>-<hostname>-<port>`) |
| `discovery.prefer-ip-address`, `ip-address` | automatic resolution, or `Service.Address` |
| `discovery.tags`, `discovery.metadata` | `Service.Tags`, `Service.Meta` |
| `discovery.health-check-path` (`/actuator/health`) | `Health.Endpoints` (`/health/ready` checked) |
| `discovery.health-check-interval` (10s) | `Health.Interval` (10s) |
| `discovery.heartbeat.enabled`, `ttl` (30s) | `Health.Check = ttl`, `Health.TTL` (30s) |
| `health-check-critical-timeout` | `Health.DeregisterCriticalServiceAfter` (1m by default) |
| `discovery.register=false` | `Lifecycle.AutoRegister=false` |
| `fail-fast` | `Lifecycle.FailFast` (default false) |
| `retry.*` | `Retry` |
| `catalog-services-watch` | `Discovery().Watch` |
| `query-passing` | passing-only is the default; `AnyStatus()` opts out |
| Spring Cloud LoadBalancer | `consul.Balancer(balancer.RoundRobin())` |
| `config.prefix` / `default-context` / `profile-separator` | `KV.Prefix` / `KV.DefaultContext` / `KV.ProfileSeparator` |
| `config.format` (KEY_VALUE, YAML, PROPERTIES, FILES) | `KV.Format` (keyvalue, yaml, json) |
| `config.data-key` | `KV.DataKey` |
| `config.watch.enabled` + `@RefreshScope` | `kvconfig.Watch[T]` |

The KV layout (`config/application`, `config/<name>,<profile>`) is the same,
so Go and Java services can share configuration trees. PROPERTIES and
FILES formats are not supported.
