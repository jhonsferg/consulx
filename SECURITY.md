# Security policy

## Supported versions

ConsulX is in the `v0.x` series. Security fixes are released for the latest
minor version only.

| Version | Supported |
|---------|-----------|
| latest `v0.x` release | yes |
| older releases | no, please upgrade |

Contrib modules (`contrib/prometheus`, `contrib/otel`, `contrib/fiber`) follow
the same policy for their own latest version.

## Reporting a vulnerability

**Do not open a public issue, pull request or discussion for security
problems.**

Report vulnerabilities privately through GitHub:

1. Open [Security → Report a vulnerability](https://github.com/jhonsferg/consulx/security/advisories/new).
2. Describe the problem, the affected versions, and how to reproduce it.
   A minimal program or configuration helps a lot.
3. Include the impact you expect (for example token disclosure, TLS bypass,
   denial of service).

You will receive an acknowledgement within **5 business days**. The
maintainer will confirm the problem, agree on a disclosure timeline with you,
prepare a fix and publish a GitHub security advisory together with the fixed
release. Reporters are credited in the advisory unless they prefer otherwise.

## Scope

In scope:

- the core module `github.com/jhonsferg/consulx` and its packages;
- the contrib modules;
- the release and CI workflows of this repository.

Out of scope:

- vulnerabilities in Consul itself: report them to
  [HashiCorp](https://www.hashicorp.com/security);
- vulnerabilities in third-party dependencies that ConsulX does not call;
  `govulncheck` runs on every build and weekly, and Dependabot proposes
  updates.

## Security practices of the project

- ACL tokens are held in a redacted `Secret` type and never logged.
- TLS certificate verification is on by default; disabling it logs a warning.
- Every pull request runs CodeQL, govulncheck, gosec, gitleaks secret scanning
  and dependency review.
- Production guidance: [docs/production.md](docs/production.md).
