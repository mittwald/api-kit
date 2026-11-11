# Contributing to api-kit

Thanks for helping! api-kit turns a declarative description of a REST API
into a command-line tool: definitions, authentication, fetching, joins,
calls, and the commands that expose them. Contributions that make these
pieces more robust or more broadly useful are welcome.

## Getting started

You need Go 1.27 or newer and [golangci-lint](https://golangci-lint.run) v2.

```shell
git clone https://github.com/mittwald/api-kit.git
cd api-kit
make check      # vet, lint, race tests, cross-compile, go mod tidy check
make test-min   # run the tests on Go 1.27.0, the oldest supported release
```

## Ground rules

- **No API of its own.** api-kit contains no resource definitions, URLs,
  header names, environment variable names or product names of any real
  API. Everything specific comes from the definition a tool embeds or from
  the options it passes. Tests and examples use invented APIs on
  `example.test`.
- **One module.** All packages share one `go.mod` and one version.
- **Few dependencies.** Every dependency is paid for by every user of the
  module. Prefer the standard library, `golang.org/x` and cli-kit. Explain
  any new dependency in the pull request.
- **Go 1.27 compatibility.** Do not use standard-library APIs newer than the
  `go` directive in `go.mod`. CI runs the tests on Go 1.27.0 to catch this.
- **Platforms.** Everything must build on Linux, macOS and Windows.
- **Libraries do not print or exit.** Packages below `cli` return typed
  errors and diagnostics; only `cli` writes to the terminal and chooses an
  exit code.
- **No hidden global state.** Configuration is passed in as options.
- **Never print secrets.** Errors and logs must not contain tokens or
  passwords.

## Code style

- Run `make fmt` and `make lint` before you push.
- Every exported identifier has a godoc comment that ends with a period.
  Each package has its overview in `doc.go`.
- Write comments and documentation in US English.
- Add tests for new behavior. Add an `Example` function where the output is
  deterministic: it documents the package and is tested at the same time.

## Pull requests

- Keep a pull request to one topic.
- Use [Conventional Commits](https://www.conventionalcommits.org) for commit
  messages, for example `feat(engine): retry idempotent requests` or
  `fix(expr): keep a literal dollar sign before a brace`.
- Mark breaking API or definition-schema changes with `!`
  (`feat(definition)!: …`) and the `breaking-change` label. api-kit is
  `v0.x`, so breaking changes are allowed, but they bump the minor version
  and are listed first in the release notes.

## Releases

Maintainers tag releases from `main` as `vMAJOR.MINOR.PATCH`. Pushing the
tag runs the release workflow: it tests the tagged commit, creates the
GitHub release with generated notes, and asks the Go module proxy to fetch
the version so it appears on pkg.go.dev. Tags are never moved or deleted;
a broken release is withdrawn with a `retract` directive in `go.mod`.

## Reporting security issues

Please do not open public issues for vulnerabilities. See
[SECURITY.md](SECURITY.md).
