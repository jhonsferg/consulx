# Contributing to ConsulX

Thank you for your interest in improving ConsulX. This guide explains how to
propose changes and what a pull request needs before it can be merged.

By participating you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
Security problems must **not** be reported in public issues: follow the
[security policy](SECURITY.md).

## Table of contents

1. [Ways to contribute](#1-ways-to-contribute)
2. [Development setup](#2-development-setup)
3. [Repository layout](#3-repository-layout)
4. [Making a change](#4-making-a-change)
5. [Tests and checks](#5-tests-and-checks)
6. [Commit messages](#6-commit-messages)
7. [Pull requests](#7-pull-requests)
8. [Documentation](#8-documentation)
9. [Releases](#9-releases)

## 1. Ways to contribute

- **Report a bug** with the [bug report form](https://github.com/jhonsferg/consulx/issues/new?template=bug_report.yml).
- **Propose a feature** with the [feature request form](https://github.com/jhonsferg/consulx/issues/new?template=feature_request.yml).
  For large changes, open the issue first so the design can be agreed before
  you write code.
- **Improve the documentation** (README, `docs/`, examples, godoc).
- **Fix an issue**: comment on it so work is not duplicated.

## 2. Development setup

| Tool | Version | Used for |
|------|---------|----------|
| Go | 1.26.7 or later | building and testing |
| Docker | recent | integration tests (Testcontainers) and the race detector on machines without a C toolchain |
| golangci-lint | v2.14.0 | static analysis (`.golangci.yml`) |

```sh
git clone https://github.com/jhonsferg/consulx.git
cd consulx
go test ./...
```

A local Consul agent for manual testing:

```sh
docker run -d --name consul -p 8500:8500 hashicorp/consul:1.22 agent -dev "-client=0.0.0.0"
```

## 3. Repository layout

| Path | Content |
|------|---------|
| `/` (module `github.com/jhonsferg/consulx`) | the core library |
| `health/`, `discovery/`, `balancer/`, `kvconfig/` | public packages |
| `internal/` | implementation details, not part of the API |
| `contrib/prometheus`, `contrib/otel`, `contrib/fiber` | separate modules with optional integrations |
| `integration/` | separate module: tests against real Consul agents |
| `examples/` | separate module: runnable examples |
| `docs/` | architecture, compatibility, production and release documentation, ADRs |

Every module except the root uses a `replace` directive pointing to the
working copy, so a change is tested across modules at once.

## 4. Making a change

1. Fork the repository and create a branch from `main` named after the change
   type: `feat/...`, `fix/...`, `refactor/...`, `docs/...`, `chore/...`.
2. Keep the change focused: one pull request per topic.
3. Follow the existing style: idiomatic Go, small interfaces, `context.Context`
   on every network call, no global state, errors wrapped with `%w`.
4. Every exported identifier needs a godoc comment.
5. Every goroutine needs a documented purpose and exit condition; tests of
   packages that start goroutines use `goleak`.
6. Add or update tests for every behaviour you change.
7. Update the documentation affected by the change (see [section 8](#8-documentation)).

Architecture decisions are recorded in [docs/decisions/](docs/decisions/);
significant design changes should add or update an ADR.

## 5. Tests and checks

Run before opening a pull request:

```sh
gofmt -l .                     # must print nothing
go vet ./...
go test ./...
go test -race ./...            # needs cgo; or use the container below
golangci-lint run ./...
```

Race detector without a local C toolchain:

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.27 go test -race ./...
```

Integration tests against a real agent (need Docker):

```sh
cd integration
CONSUL_VERSION=1.22 go test ./...          # add -short to skip the slow crash test
```

CI runs all of this on every pull request, plus the integration suite against
Consul 1.20, 1.21, 1.22 and 2.0, fuzzing, CodeQL, govulncheck, gosec, gitleaks
and dependency review. Coverage of the library must stay at or above 80 %.
See [docs/development.md](docs/development.md) for details.

## 6. Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org):

```text
<type>(<optional scope>): <imperative summary, at most 72 characters>
```

| Type | Use for | Release effect |
|------|---------|----------------|
| `feat` | new functionality | minor version |
| `fix` | bug fixes | patch version |
| `refactor` | code changes without behaviour change, performance | patch version |
| `docs` | documentation only | none |
| `style` | formatting only | none |
| `chore` | tests, dependencies, CI, tooling | none |

- Use the imperative mood in English: "add retry policy", not "added".
- Append `!` after the type (`feat!:`) for breaking changes and explain them in
  the body.
- Keep commits small and focused; each commit must build and pass its tests.
- Do not add AI attribution or co-author trailers for tools.

## 7. Pull requests

1. Open the pull request against `main` and fill in the
   [template](.github/PULL_REQUEST_TEMPLATE.md).
2. The title must follow Conventional Commits; CI checks it.
3. All checks must pass. A maintainer reviews the change and may ask for
   adjustments.
4. Pull requests are merged with a merge commit, which keeps every commit in
   the history and in the release notes. The branch is deleted automatically
   after the merge.

## 8. Documentation

- User-facing behaviour: update the [README](README.md) and the relevant file
  in `docs/`.
- Every diagram must be a [Mermaid](https://mermaid.js.org) block
  (`flowchart`, `stateDiagram-v2`, ...). ASCII diagrams are not accepted; in
  places where Mermaid cannot render (YAML or Go comments), describe the flow
  in prose.
- Code samples in documentation must compile against the current API.
- Add an entry under `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md).

## 9. Releases

Releases are automatic: after a merge to `main`, once CI and security scans
pass, the release workflow computes the next version from the commit types,
tags every module whose Go files changed, publishes the release notes and
notifies pkg.go.dev. Changes that touch no Go file (`*.go`, `go.mod`,
`go.sum`) never trigger a release. See [docs/release.md](docs/release.md).
