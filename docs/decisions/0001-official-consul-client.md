# ADR 0001: Build on the official Consul Go client

- Status: accepted
- Date: 2026-09-25

## Context

ConsulX needs every Consul HTTP API: agent, catalog, health, KV, ACL,
sessions, config entries, operator and more. HashiCorp maintains
`github.com/hashicorp/consul/api`, which covers all of them and follows each
Consul release. Re-implementing the API would duplicate that work, lag
behind new releases and risk diverging from real agent behaviour.

## Decision

ConsulX uses `github.com/hashicorp/consul/api` for every request and exposes
it through `Client.Raw()`. ConsulX adds behaviour (lifecycle, retries,
feature gating, decoding, caching) on top; it does not wrap APIs where it
would add nothing.

ConsulX always calls the context-aware variants of client methods
(`*Opts`, `QueryOptions.WithContext`, `WriteOptions.WithContext`). Where no
such variant exists (`Agent.Self`), it uses `Raw().Query` with a context.

ConsulX builds the `http.Transport` itself so that each `Client` owns its
connection pool, dial timeout and TLS settings. The `http.Client` has no
global timeout because blocking queries last up to `WaitTime`; each request
is bounded by its context instead.

## Consequences

- New Consul capabilities are available on day one through `Raw()`.
- The official client sometimes declares fields before agents accept them
  (see ADR 0006), so ConsulX must gate optional fields.
- The core module inherits the client's dependencies (hclog, go-metrics,
  serf types). They are already present in every Consul-integrated service.
