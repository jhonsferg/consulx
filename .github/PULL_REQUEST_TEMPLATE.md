<!--
Thank you for contributing to ConsulX.
The pull request title must follow Conventional Commits, for example:
  fix(discovery): keep index when a watch is retried
It is checked by CI and used in the release notes.
-->

## Summary

<!-- What does this change do, and why? Link the issue it addresses. -->

Closes #

## Type of change

- [ ] `feat`: new functionality (minor release)
- [ ] `fix`: bug fix (patch release)
- [ ] `refactor`: no behaviour change, or performance (patch release)
- [ ] `docs`: documentation only (no release)
- [ ] `chore`: tests, dependencies, CI or tooling (no release)
- [ ] Breaking change (`!` in the title, explained below)

## Changes

<!-- The main changes, one bullet each. -->

-

## How it was tested

<!-- Commands run, new tests, Consul versions used for integration tests. -->

- [ ] `go test ./...`
- [ ] `go test -race ./...`
- [ ] `golangci-lint run ./...`
- [ ] Integration tests: `CONSUL_VERSION=... go test ./...` in `integration/`
- [ ] Not applicable (documentation or CI only)

## Checklist

- [ ] Tests cover the new or changed behaviour.
- [ ] Exported identifiers have godoc comments.
- [ ] New goroutines have a documented exit condition and are covered by `goleak`.
- [ ] Documentation is updated (README, `docs/`, examples); code samples compile.
- [ ] Diagrams, if any, are Mermaid blocks (no ASCII diagrams).
- [ ] `CHANGELOG.md` has an entry under `## [Unreleased]`.
- [ ] No secrets, tokens or credentials are included.

## Breaking changes

<!-- Describe the impact and the migration path, or write "None". -->

None.
