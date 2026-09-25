# ADR 0007: Configuration binding

* Status: accepted
* Date: 2026-09-25

## Context

Configuration stored in Consul KV must reach typed Go structs. Generic
decoders (mapstructure-like) accept almost anything silently; hand-written
parsing per service is repetitive and error-prone.

## Decision

* KV folders are decoded into a tree (`map[string]any`, lists as `[]any`).
  KeyValue folders map key paths to nested objects; YAML and JSON folders
  hold one document under the data key. Layers are deep-merged, most
  specific last, using the Spring Cloud Consul layout
  (`config/application`, `config/application,<profile>`, `config/<name>`,
  `config/<name>,<profile>`).
* `internal/bind` copies the tree into a struct with a bounded feature set:
  scalars, `time.Duration`, `encoding.TextUnmarshaler`, slices (lists,
  numeric KV folders, comma separated strings), string-keyed maps, nested
  and embedded structs, pointers and `any`.
* Tags: `consul:"name"`, `consul:"name,required"`, `consul:"-"`,
  `default:"value"`. Untagged fields match case-insensitively ignoring `-`,
  `_` and `.`, so `MaxConns` binds `max-conns`.
* A missing non-pointer section still gets its defaults and required checks;
  a missing pointer section stays nil (optional by design).
* Every problem is collected and returned together with its full key path
  (`*BindError`). Unknown keys are ignored by default and reported with
  `ErrorUnused`.
* Validation: types implementing `Validate() error` are checked after
  binding; `kvconfig.WithValidator` adds more.
* Dynamic configuration (`kvconfig.Watch[T]`) builds a fresh `T` on every
  change and publishes it only when binding and validation succeed. An
  invalid change is rejected, logged, counted and reported on `Errors()`;
  the previous value stays current. A generic function is used because Go
  methods cannot be generic, and writing into a caller-owned struct from a
  background goroutine would be a data race.

## Consequences

* No unsafe code, no writes to unexported fields, deterministic results.
* Readers of `Watcher.Current()` always see a complete, validated value.
* Features outside the set (custom decoders per field, env interpolation)
  are not supported; `any` fields give access to the raw tree.
