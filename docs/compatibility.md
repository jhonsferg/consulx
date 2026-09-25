# Compatibility

This document records what ConsulX supports, against which Consul versions,
and on what evidence. It follows one rule: **nothing is marked as supported
without implementation, tests and verification against a real Consul agent.**

Status legend:

| Label                 | Meaning                                                                                                                             |
|-----------------------|-------------------------------------------------------------------------------------------------------------------------------------|
| `Supported`           | High-level ConsulX API with tests; the Tests column says whether an integration test against real agents exists or only unit tests. |
| `Low-level supported` | Reachable through `Client.Raw()` (official `consul/api` client) only.                                                               |
| `Version dependent`   | Requires a minimum Consul version; ConsulX gates it at runtime.                                                                     |
| `Enterprise feature`  | Only meaningful on Consul Enterprise; never sent to CE by default.                                                                  |
| `Planned`             | Designed, not implemented yet. Do not rely on it.                                                                                   |
| `Not implemented`     | Out of scope for now.                                                                                                               |

> Last verified 2026-09-25 with the official client v1.34.5.

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

| Request                                       | 1.21.5                            | 1.22.7                         | 2.0.4                          |
|-----------------------------------------------|-----------------------------------|--------------------------------|--------------------------------|
| `PUT /v1/agent/service/register` with `Ports` | `400 json: unknown field "Ports"` | `200`                          | `200`                          |
| `PUT /v1/agent/service/register` with `AI`    | not tested                        | `400 json: unknown field "AI"` | `400 json: unknown field "AI"` |

The official Go client `github.com/hashicorp/consul/api` **v1.34.5** already
declares both `Ports` and `AI` on `AgentServiceRegistration`. A client that
simply forwards user input would therefore fail registration on older agents.
ConsulX detects the agent version (`GET /v1/agent/self` → `Config.Version`,
`+ent` suffix for Enterprise) on every (re)connection and returns
`ErrUnsupportedFeature` *before* sending a request that the agent would reject.

## 2. Consul versions

The full integration suite (13 scenarios, see [development.md](development.md))
passed on 2026-09-25 against every version below.

| Consul | Line status                | ConsulX status | Notes                               |
|--------|----------------------------|----------------|-------------------------------------|
| 2.0.4  | Current, supported to 2032 | Tested         | Same `/v1` API as 1.x               |
| 1.22.7 | Supported                  | Tested         | Multi-port services, IPv6           |
| 1.21.5 | Supported (LTS)            | Tested         | No `Ports` (gated)                  |
| 1.20.6 | Older                      | Tested         | **Minimum supported version**       |
| < 1.20 | -                          | Unsupported    | Not tested; basic features may work |

Run the matrix with `CONSUL_VERSION=<tag> go test ./...` in `integration/`.

## 3. Go client

| Item                        | Value                                                                                                                                                                                                                                                                             |
|-----------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Official client             | `github.com/hashicorp/consul/api` v1.34.5                                                                                                                                                                                                                                         |
| Client `go` directive       | `go 1.26.7`                                                                                                                                                                                                                                                                       |
| YAML parser                 | `go.yaml.in/yaml/v3` (maintained continuation of `gopkg.in/yaml.v3`, which is archived upstream)                                                                                                                                                                                  |
| Env vars honoured by client | `CONSUL_HTTP_ADDR`, `CONSUL_HTTP_TOKEN`, `CONSUL_HTTP_TOKEN_FILE`, `CONSUL_HTTP_AUTH`, `CONSUL_HTTP_SSL`, `CONSUL_HTTP_SSL_VERIFY`, `CONSUL_CACERT`, `CONSUL_CAPATH`, `CONSUL_CLIENT_CERT`, `CONSUL_CLIENT_KEY`, `CONSUL_TLS_SERVER_NAME`, `CONSUL_NAMESPACE`, `CONSUL_PARTITION` |

Context support caveat: several client methods have no `context.Context`
parameter (`Agent.Self`, `Agent.Services`, `Agent.UpdateTTL`,
`Agent.ServiceRegister`, ...). ConsulX always calls the `*Opts` variants
(`ServiceRegisterOpts{}.WithContext`, `QueryOptions.WithContext`,
`WriteOptions.WithContext`) so every network call is cancellable.

## 4. Capability matrix

"1.x" and "2.x" state availability in Consul (official docs and live
checks). "Tests" names the integration test run on 1.20.6, 1.21.5, 1.22.7
and 2.0.4; "unit" means unit tests only.

### 4.1 Registration and health

| Feature                                              | Status              | 1.x    | 2.x        | OSS | Ent | Tests                                                                       |
|------------------------------------------------------|---------------------|--------|------------|-----|-----|-----------------------------------------------------------------------------|
| Agent service register                               | Supported           | ✓     | ✓         | ✓  | ✓  | `TestRegistrationLifecycleWithHTTPCheck`                                    |
| Agent service deregister                             | Supported           | ✓     | ✓         | ✓  | ✓  | same, `TestACLEnforced`                                                     |
| `replace-existing-checks`                            | Supported           | ✓     | ✓         | ✓  | ✓  | unit                                                                        |
| Tags, Meta, Weights                                  | Supported           | ✓     | ✓         | ✓  | ✓  | `TestRegistrationLifecycleWithHTTPCheck`, unit                              |
| TaggedAddresses                                      | Supported           | ✓     | ✓         | ✓  | ✓  | unit                                                                        |
| Multi-port (`Ports`)                                 | Version dependent   | ≥ 1.22 | ✓         | ✓  | ✓  | `TestFeatureGateMatchesAgent` (gate agrees with the agent on every version) |
| IPv6 service address                                 | Version dependent   | ≥ 1.22 | ✓         | ✓  | ✓  | unit                                                                        |
| `AI` service block                                   | Rejected by ConsulX | ✗     | ✗ (2.0.4) | -   | -   | unit (gate)                                                                 |
| Service maintenance                                  | Supported           | ✓     | ✓         | ✓  | ✓  | `TestMaintenanceMode`                                                       |
| HTTP check                                           | Supported           | ✓     | ✓         | ✓  | ✓  | `TestRegistrationLifecycleWithHTTPCheck`, `TestFrameworks`                  |
| TTL check + heartbeat                                | Supported           | ✓     | ✓         | ✓  | ✓  | `TestTTLHeartbeat`                                                          |
| TCP / TCP+TLS check                                  | Supported           | ✓     | ✓         | ✓  | ✓  | unit (definition)                                                           |
| gRPC check                                           | Supported           | ✓     | ✓         | ✓  | ✓  | unit (definition)                                                           |
| UDP, H2PING, OSService, Docker, Alias, Script checks | Low-level supported | ✓     | ✓         | ✓  | ✓  | via `WithRegistrationHook`                                                  |
| `DeregisterCriticalServiceAfter`                     | Supported           | ✓     | ✓         | ✓  | ✓  | `TestCrashProtectionReapsCriticalService`                                   |
| Re-registration after agent restart                  | Supported           | ✓     | ✓         | ✓  | ✓  | `TestReconnectAndReRegister`                                                |
| Namespace / Partition on service                     | Enterprise feature  | ✓     | ✓         | ✗  | ✓  | unit (gate); not integration tested                                         |
| Connect native / sidecar, proxy                      | Low-level supported | ✓     | ✓         | ✓  | ✓  | via `WithRegistrationHook`; not integration tested                          |

### 4.2 Discovery

| Feature                                    | Status              | 1.x | 2.x | OSS     | Ent | Tests                           |
|--------------------------------------------|---------------------|-----|-----|---------|-----|---------------------------------|
| Healthy instances by name                  | Supported           | ✓  | ✓  | ✓      | ✓  | `TestDiscoveryWatchAndBalancer` |
| Tag filtering                              | Supported           | ✓  | ✓  | ✓      | ✓  | same                            |
| Meta and filter expressions                | Supported           | ✓  | ✓  | ✓      | ✓  | unit                            |
| Datacenter, Near, consistency              | Supported           | ✓  | ✓  | ✓      | ✓  | unit (parameters sent)          |
| Blocking-query watch                       | Supported           | ✓  | ✓  | ✓      | ✓  | `TestDiscoveryWatchAndBalancer` |
| Load balancing                             | Supported           | ✓  | ✓  | ✓      | ✓  | same, unit                      |
| Prepared queries, peering, sameness groups | Low-level supported | ✓  | ✓  | partial | ✓  | -                               |

### 4.3 KV and configuration

| Feature                                                             | Status              | 1.x | 2.x | OSS | Ent | Tests                          |
|---------------------------------------------------------------------|---------------------|-----|-----|-----|-----|--------------------------------|
| Layered configuration with profiles                                 | Supported           | ✓  | ✓  | ✓  | ✓  | `TestDistributedConfiguration` |
| Key/value and YAML formats                                          | Supported           | ✓  | ✓  | ✓  | ✓  | same, `TestYAMLConfiguration`  |
| JSON format                                                         | Supported           | ✓  | ✓  | ✓  | ✓  | unit                           |
| Struct binding                                                      | Supported           | ✓  | ✓  | ✓  | ✓  | integration, unit, fuzz        |
| Dynamic configuration (validated)                                   | Supported           | ✓  | ✓  | ✓  | ✓  | `TestDistributedConfiguration` |
| KV Get / Put / Delete / List / Keys / CAS / Acquire / Release / Txn | Low-level supported | ✓  | ✓  | ✓  | ✓  | through `Raw().KV()`           |

### 4.4 Security and tenancy

| Feature                                                               | Status              | 1.x | 2.x | OSS | Ent | Tests                                                     |
|-----------------------------------------------------------------------|---------------------|-----|-----|-----|-----|-----------------------------------------------------------|
| ACL token                                                             | Supported           | ✓  | ✓  | ✓  | ✓  | `TestACLEnforced`                                         |
| ACL token file                                                        | Supported           | ✓  | ✓  | ✓  | ✓  | unit                                                      |
| ACL management (tokens, policies, roles, auth methods, binding rules) | Low-level supported | ✓  | ✓  | ✓  | ✓  | used through `Raw()` by `TestACLEnforced`                 |
| TLS, mutual TLS                                                       | Supported           | ✓  | ✓  | ✓  | ✓  | `TestTLS`                                                 |
| Namespaces                                                            | Enterprise feature  | ✓  | ✓  | ✗  | ✓  | unit (gate); `GET /v1/namespaces` is 404 on CE (verified) |
| Admin partitions                                                      | Enterprise feature  | ✓  | ✓  | ✗  | ✓  | unit (gate); `GET /v1/partitions` is 404 on CE (verified) |

### 4.5 Other Consul APIs

Low-level supported through `Client.Raw()`, which shares ConsulX's address,
token, TLS and datacenter: Agent (self, members, metrics, monitor, reload,
tokens), Catalog, Health, Session, Lock/Semaphore helpers, Event, Prepared
Query, Coordinate, Status, Operator (raft, autopilot, keyring, area,
license, usage, utilization, segment, audit), Snapshot, Txn, Connect CA and
intentions, Config Entries (verified `GET /v1/config/service-defaults` =
200 on 1.22.7 and 2.0.4), Discovery Chain, Peering, Exported/Imported
services, Namespaces, Partitions, Debug.

## 5. Sources

* Consul release notes: <https://developer.hashicorp.com/consul/docs/release-notes>
* Consul 2.0.x release notes: <https://developer.hashicorp.com/consul/docs/release-notes/consul/v2_0_x>
* Consul 1.22.x release notes: <https://developer.hashicorp.com/consul/docs/release-notes/consul/v1_22_x>
* Version-specific upgrade notes: <https://developer.hashicorp.com/consul/docs/upgrade/version-specific>
* Agent service API: <https://developer.hashicorp.com/consul/api-docs/agent/service>
* Agent check API: <https://developer.hashicorp.com/consul/api-docs/agent/check>
* Go client versions: <https://proxy.golang.org/github.com/hashicorp/consul/api/@v/list>
* Spring Cloud Consul reference: <https://docs.spring.io/spring-cloud-consul/reference/>
