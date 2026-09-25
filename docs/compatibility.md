# Compatibility

This document records what ConsulX supports, against which Consul versions,
and on what evidence. It follows one rule: **nothing is marked as supported
without implementation, tests and verification against a real Consul agent.**

Status legend:

| Label                 | Meaning                                                                 |
| --------------------- | ----------------------------------------------------------------------- |
| `Supported`           | High-level ConsulX API, unit + integration tested.                      |
| `Low-level supported` | Reachable through `Client.Raw()` (official `consul/api` client) only.   |
| `Version dependent`   | Requires a minimum Consul version; ConsulX gates it at runtime.         |
| `Enterprise feature`  | Only meaningful on Consul Enterprise; never sent to CE by default.      |
| `Planned`             | Designed, not implemented yet. Do not rely on it.                       |
| `Not implemented`     | Out of scope for now.                                                   |

> Current project status: **Architecture & Capability Discovery**. Every
> high-level capability below is `Planned` until its phase lands. The
> "Evidence" column records what was verified during discovery.

## 1. What "Consul 1.x" and "Consul 2.x" actually are

Verified on 2026-09-25 against the official release notes, Docker Hub tags and
live agents (`hashicorp/consul:1.21.5`, `1.22.7`, `2.0.4`):

* **Consul 2.0 is not a new HTTP API.** It is the first release under IBM's
  Version-Modification-Fix (`V.M.F`) numbering. `V` starts a new support
  lifecycle (2.0.x supported through April 2032); `M` adds features; `F` is
  fixes only. The HTTP API is still served under `/v1/...`.
* The 2.0 feature set is additive: multi-port routing for the service mesh
  (Enterprise), a global RPC `rate-limit` config entry, a new CA provider,
  API Gateway scaling, opt-in telemetry, certificate expiration metrics.
* **Upgrade constraint (Enterprise only):** agents must be on 1.21.7+ with an
  IBM-issued license before moving to 2.0.x, or they fail to start. CE is not
  affected.
* The old "**v2 catalog**" / resource API was an *experiment* introduced in
  1.17 and deprecated in 1.19. It is **not** Consul 2.x. `GET
  /api/catalog/v2beta1/Service` returns `404` on both 1.22.7 and 2.0.4.
  ConsulX does not target it.
* Consul 1.22 introduced **IPv6** addresses in agent/service config and
  **multi-port services** (`Ports` in the service definition).

Consequence for the design: there is one HTTP API with **additive, version
gated fields**, not two APIs. The compatibility layer therefore is a
*feature gate* keyed on the detected agent version and edition, not two
separate client implementations.

### 1.1 Why a feature gate is required (evidence)

The Consul agent decodes request bodies strictly. Sending a field the agent
does not know rejects the whole request:

| Request                                        | 1.21.5                                      | 1.22.7 | 2.0.4                                     |
| ---------------------------------------------- | ------------------------------------------- | ------ | ----------------------------------------- |
| `PUT /v1/agent/service/register` with `Ports`  | `400 json: unknown field "Ports"`           | `200`  | `200`                                     |
| `PUT /v1/agent/service/register` with `AI`     | not tested                                  | `400 json: unknown field "AI"` | `400 json: unknown field "AI"` |

The official Go client `github.com/hashicorp/consul/api` **v1.34.5** already
declares both `Ports` and `AI` on `AgentServiceRegistration`. A client that
simply forwards user input would therefore fail registration on older agents.
ConsulX detects the agent version (`GET /v1/agent/self` → `Config.Version`,
`+ent` suffix for Enterprise) on every (re)connection and returns
`ErrUnsupportedFeature` *before* sending a request that the agent would reject.

## 2. Consul versions

| Consul | Line status                  | ConsulX status | Notes                                           |
| ------ | ---------------------------- | -------------- | ----------------------------------------------- |
| 2.0.x  | Current, supported to 2032   | Planned: test  | Verified reachable, same `/v1` API.             |
| 1.22.x | Supported                    | Planned: test  | Multi-port, IPv6.                               |
| 1.21.x | Supported (LTS)              | Planned: test  | No `Ports`.                                     |
| 1.20.x | Older                        | Planned: test  | Candidate minimum; confirmed only if CI passes. |
| < 1.20 | -                            | Unsupported    | Not tested; may work for basic features.        |

The minimum supported version will be the oldest version for which the full
integration suite passes. It is not declared until that happens.

## 3. Go client

| Item                          | Value                                     |
| ----------------------------- | ----------------------------------------- |
| Official client               | `github.com/hashicorp/consul/api` v1.34.5 |
| Client `go` directive         | `go 1.26.7`                               |
| Env vars honoured by client   | `CONSUL_HTTP_ADDR`, `CONSUL_HTTP_TOKEN`, `CONSUL_HTTP_TOKEN_FILE`, `CONSUL_HTTP_AUTH`, `CONSUL_HTTP_SSL`, `CONSUL_HTTP_SSL_VERIFY`, `CONSUL_CACERT`, `CONSUL_CAPATH`, `CONSUL_CLIENT_CERT`, `CONSUL_CLIENT_KEY`, `CONSUL_TLS_SERVER_NAME`, `CONSUL_NAMESPACE`, `CONSUL_PARTITION` |

Context support caveat: several client methods have no `context.Context`
parameter (`Agent.Self`, `Agent.Services`, `Agent.UpdateTTL`,
`Agent.ServiceRegister`, ...). ConsulX always calls the `*Opts` variants
(`ServiceRegisterOpts{}.WithContext`, `QueryOptions.WithContext`,
`WriteOptions.WithContext`) so every network call is cancellable.

## 4. Capability matrix

Columns "1.x" and "2.x" state API availability in Consul, per official docs
and live checks. "Tests" is filled only when an integration test exists.

### 4.1 Registration and health

| Feature                          | High level | Low level | 1.x      | 2.x | OSS | Ent | Tests | Notes |
| -------------------------------- | ---------- | --------- | -------- | --- | --- | --- | ----- | ----- |
| Agent service register           | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | `PUT /v1/agent/service/register`; verified 1.21.5/1.22.7/2.0.4 |
| Agent service deregister         | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | `PUT /v1/agent/service/deregister/:id` |
| `replace-existing-checks`        | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | Used for idempotent re-registration |
| Tags, Meta, TaggedAddresses      | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | |
| Weights                          | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | |
| Multi-port (`Ports`)             | Planned    | ✓         | ≥ 1.22   | ✓   | ✓   | ✓   | -     | Version dependent. Registration works on CE (verified); mesh routing by port is Enterprise |
| IPv6 service address             | Planned    | ✓         | ≥ 1.22   | ✓   | ✓   | ✓   | -     | Version dependent |
| `AI` service block               | No         | ✓         | ✗        | ✗ (2.0.4) | ? | ? | - | Present in client v1.34.5, rejected by 2.0.4. Not exposed |
| Service maintenance              | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | |
| HTTP check                       | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | 2xx passing, 429 warning, other critical |
| TCP / TCP+TLS check              | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | |
| TTL check + heartbeat            | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | |
| gRPC check                       | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | Standard gRPC health protocol |
| UDP, H2PING, OSService, Docker, Alias checks | No | ✓ | ✓       | ✓   | ✓   | ✓   | -     | Low-level supported |
| Script check (`Args`)            | No         | ✓         | ✓        | ✓   | ✓   | ✓   | -     | Requires agent `enable_script_checks`; security sensitive, low-level only |
| `DeregisterCriticalServiceAfter` | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | Consul minimum is 1 minute |
| Namespace / Partition on service | Planned    | ✓         | ✓        | ✓   | ✗   | ✓   | -     | Enterprise feature; never sent unless configured |
| Connect native / sidecar         | Planned    | ✓         | ✓        | ✓   | ✓   | ✓   | -     | Pass-through of official types |

### 4.2 Discovery

| Feature                       | High level | Low level | 1.x | 2.x | OSS | Ent | Tests | Notes |
| ----------------------------- | ---------- | --------- | --- | --- | --- | --- | ----- | ----- |
| Healthy instances by name     | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | `GET /v1/health/service/:name?passing` |
| Tag filtering (multi)         | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| Filter expressions            | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | `filter=` |
| Datacenter                    | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| Near / consistency / stale    | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| Blocking-query watch          | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| Load balancing (RR/random/weighted) | Planned | n/a    | ✓   | ✓   | ✓   | ✓   | -     | Client side |
| Prepared queries              | No         | ✓         | ✓   | ✓   | ✓   | ✓   | -     | Low-level supported |
| Sameness groups / peering     | No         | ✓         | ✓   | ✓   | partial | ✓ | -  | Low-level supported |

### 4.3 KV and configuration

| Feature                                 | High level | Low level | 1.x | 2.x | OSS | Ent | Tests | Notes |
| --------------------------------------- | ---------- | --------- | --- | --- | --- | --- | ----- | ----- |
| Get / Put / Delete / DeleteTree / List / Keys | Planned | ✓   | ✓   | ✓   | ✓   | ✓   | -     | Verified PUT/GET 1.22.7 & 2.0.4 |
| CAS / Acquire / Release                 | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| Transactions                            | No         | ✓         | ✓   | ✓   | ✓   | ✓   | -     | Low-level supported |
| Layered config (application/service/profile) | Planned | n/a  | ✓   | ✓   | ✓   | ✓   | -     | |
| Struct binding                          | Planned    | n/a       | ✓   | ✓   | ✓   | ✓   | -     | |
| Dynamic config (watch + validate)       | Planned    | n/a       | ✓   | ✓   | ✓   | ✓   | -     | |

### 4.4 Security and tenancy

| Feature                         | High level | Low level | 1.x | 2.x | OSS | Ent | Tests | Notes |
| ------------------------------- | ---------- | --------- | --- | --- | --- | --- | ----- | ----- |
| ACL token / token file          | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| ACL management (tokens, policies, roles, auth methods, binding rules) | No | ✓ | ✓ | ✓ | ✓ | ✓ | - | Low-level supported |
| TLS (CA, client cert, SNI)      | Planned    | ✓         | ✓   | ✓   | ✓   | ✓   | -     | |
| Namespaces                      | Planned    | ✓         | ✓   | ✓   | ✗   | ✓   | -     | Enterprise feature; `GET /v1/namespaces` is 404 on CE (verified) |
| Admin partitions                | Planned    | ✓         | ✓   | ✓   | ✗   | ✓   | -     | Enterprise feature; `GET /v1/partitions` is 404 on CE (verified) |

### 4.5 Other Consul APIs

All reachable through `Client.Raw()`; ConsulX adds no abstraction unless a
concrete need appears. Status: `Low-level supported` once `Raw()` ships.

Agent (self, members, metrics, monitor, reload, tokens), Catalog, Health,
Session, Lock/Semaphore helpers, Event, Prepared Query, Coordinate, Status,
Operator (raft, autopilot, keyring, area, license, usage, utilization,
segment, audit), Snapshot, Txn, Connect CA and intentions, Config Entries
(verified `GET /v1/config/service-defaults` = 200 on 1.22.7 and 2.0.4),
Discovery Chain, Peering, Exported/Imported services, Namespaces, Partitions,
Debug.

## 5. Sources

* Consul release notes: <https://developer.hashicorp.com/consul/docs/release-notes>
* Consul 2.0.x release notes: <https://developer.hashicorp.com/consul/docs/release-notes/consul/v2_0_x>
* Consul 1.22.x release notes: <https://developer.hashicorp.com/consul/docs/release-notes/consul/v1_22_x>
* Version-specific upgrade notes: <https://developer.hashicorp.com/consul/docs/upgrade/version-specific>
* Agent service API: <https://developer.hashicorp.com/consul/api-docs/agent/service>
* Agent check API: <https://developer.hashicorp.com/consul/api-docs/agent/check>
* Go client versions: <https://proxy.golang.org/github.com/hashicorp/consul/api/@v/list>
* Spring Cloud Consul reference: <https://docs.spring.io/spring-cloud-consul/reference/>
