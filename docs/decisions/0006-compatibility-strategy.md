# ADR 0006: Compatibility strategy for Consul 1.x and 2.x

- Status: accepted
- Date: 2026-09-25

## Context

The brief assumed Consul 1.x and 2.x might expose different APIs. Discovery
(see docs/compatibility.md) established that:

- Consul 2.0 is a numbering change (IBM V.M.F). The HTTP API is still `/v1`.
- The "v2 catalog" resource API was an experiment (1.17, deprecated 1.19);
  it is not Consul 2.x and returns 404 on 1.22.7 and 2.0.4.
- Agents decode request bodies strictly: an unknown field fails the whole
  request with HTTP 400. Verified: `Ports` fails on 1.21.5 and works on
  1.22.7 and 2.0.4; `AI` fails on 1.22.7 and 2.0.4 although the official
  client v1.34.5 declares it.

## Decision

- One implementation, no per-version clients. Compatibility is a feature
  gate in `internal/compat`.
- The agent version and edition are detected with `GET /v1/agent/self`
  (`Config.Version`, which carries `+ent` for Enterprise, as built by
  `VersionWithMetadata()` in Consul's source) on every (re)connection.
- Before sending an optional field, ConsulX checks the gate and returns
  `*UnsupportedFeatureError` (matching `ErrUnsupportedFeature`) with the
  requirement and the detected version, instead of letting the agent fail
  the registration.
- When the version cannot be read (for example the token lacks
  `agent:read`), the gate is permissive and the agent stays the final
  authority. Fields that no verified agent accepts are always rejected.
- Enterprise-only fields (namespace, partition) are sent only when
  configured, so Community Edition installs are never affected.
- Every requirement in the gate must be backed by an integration test run
  against the versions of the CI matrix.

## Consequences

- Upgrading the official client requires reviewing new registration fields
  and adding gate entries; a test guards against ungated fields.
- Supporting a future Consul version means adding it to the CI matrix and
  updating gate entries, not rewriting the public API.
