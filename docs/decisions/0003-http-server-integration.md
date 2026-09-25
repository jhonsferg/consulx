# ADR 0003: Integration through *http.Server

* Status: accepted
* Date: 2026-09-25

## Context

Services use many routers (net/http, Gin, Echo, Chi, Gorilla). All of them
produce an `http.Handler` served by an `*http.Server`. A Consul integration
needs the port to register, a place to serve health endpoints, and the
scheme (http/https).

## Decision

* The integration point is the application's `*http.Server`
  (`WithServer`). ConsulX never creates, starts, stops or replaces it.
* The port comes from, in order: `Service.Port`, the default entry of
  `Service.Ports`, the listener passed with `WithListener`, `Server.Addr`
  (empty means `:http`, as in net/http). Port 0 requires `WithListener` or
  an explicit port, because the real port is only known after listening.
* The scheme is https when `Server.TLSConfig` is set, unless configured.
* The address is never taken from a wildcard `Server.Addr` (`:8080`,
  `0.0.0.0`, `::`). It comes from an explicit value or from a resolver
  chain: `Service.AddressEnv`, a concrete server host, the local IP routing
  to the Consul agent, then the first private interface address. Loopback is
  refused unless `AllowLoopback` is set. The chosen source is logged.
* No framework is imported by the core. Frameworks whose engine is an
  `http.Handler` need no adapter.

## Consequences

* Any router works unchanged, including ones added after ConsulX.
* Route listing is impossible through `http.Handler`; ConsulX does not try
  to discover routes by reflection.
* Automatic address detection can still be wrong in unusual topologies;
  `WithServiceAddress` or a custom `AddressResolver` fixes it explicitly.
