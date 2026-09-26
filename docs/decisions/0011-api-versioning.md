# ADR 0011: API versioning

- Status: accepted
- Date: 2026-09-25

## Context

ConsulX will be a dependency of many services. Breaking changes are costly
for them, but the API needs room to evolve while it is validated.

## Decision

- Semantic Versioning. Releases start at `v0.x`; `v1.0.0` is tagged only
  when the Definition of Done in the project brief is met and the public
  API has been used by real services.
- During `v0.x`, breaking changes are allowed in minor releases and are
  always listed in CHANGELOG.md under "Breaking".
- From `v1`, no breaking change within a major version. Deprecations are
  announced with `// Deprecated:` comments at least one minor release before
  removal in the next major version.
- Heavy integrations (Prometheus, OpenTelemetry, Fiber, integration tests,
  examples) are separate Go modules with their own versions, so they can
  evolve without affecting the core.
- The `go` directive follows the oldest Go release still supported upstream
  that the official Consul client accepts (currently 1.26.7).
- The public surface is kept small: implementation lives in `internal/`,
  and configuration types are plain structs so they can gain fields without
  breaking callers.

## Consequences

- Adding fields to Config structs and new options is non-breaking; removing
  or renaming them is breaking.
- Users of unkeyed struct literals of ConsulX types are not protected;
  documentation recommends keyed literals.
