# Contributing to AACT

Contributions to Another Agent Capability Toolkit are welcome through pull requests.
All changes intended for `main` go through a PR; do not push directly to `main`.

## Discuss architectural changes

Describe the problem, proposed behavior, and trade-offs before implementing a new
subsystem or changing an existing contract. Architectural decisions belong to the
project maintainer. Keep proposed decisions distinct from accepted decisions.

The TUI and agent adapters are currently being redesigned. Read the design status
in [docs/superpowers/specs](docs/superpowers/specs) before extending those areas;
the current implementation does not yet implement every design decision.

## Development setup

Install the Go version specified in [go.mod](go.mod) and Docker for container
development and integration tests. These are contributor dependencies; release
users receive native executables and packaged resources.

The [README](README.md#build-and-verify-from-source) describes native development
archives and their layout. To compile the manager directly:

```sh
go build ./cmd/aact
```

Keep the bundled `packages` resources available when running development builds;
the README documents `AACT_BUNDLED_ROOT` and the release archive layout.

## Tests and formatting

Use TDD for features and bug fixes: write the intended failing test, confirm the
failure, implement the change, and confirm it passes. Keep tests next to the Go
package they exercise; put larger scenarios under `tests/` and fixtures under
`testdata/` where appropriate.

Run the tests for the packages you changed, then the native checks:

```sh
go test ./... -count=1
go vet ./...
go test -race ./... -count=1
```

Race tests require a supported platform/toolchain; CI runs them where supported.
Format changed Go files with `gofmt`. For changes affecting containers, use the
Docker test commands in the README. CI also checks macOS, Linux, Windows, and
release archives.

Use temporary agent homes and manager state during development and tests. Never
change real user agent configs, credentials, or existing MCP instances in tests.
The CLI supports `--agent-home` and `--state-dir` for isolated development runs.

## Agent adapters

Agent integrations are compiled Go implementations of a common adapter contract.
New integrations are contributed in source and shipped in a new AACT build.

An adapter must own agent detection, supported config locations and precedence,
MCP entry edits, missing-config creation, and config rollback. Keep these behaviors
testable without launching the TUI. Shared file-editing helpers are encouraged;
agent-specific file rules belong in the adapter.

Before submitting an integration, verify assumptions using official documentation
and installed clients where available. Include evidence for the tested versions,
platforms, and config formats. Distinguish live verification from mocked tests and
document environments you could not verify. Include cases for unrelated settings,
absent files, overlapping config files, conflicting registrations, and write
failures. Read the [adapter design](docs/superpowers/specs/2026-10-02-aact-agent-adapters-design.md)
and [verification notes](docs/agent-adapter-verification.md) for current decisions
and known implementation gaps.

## Skills and MCP packages

Packaged capabilities live under `packages/`. Use an existing package as a
reference for `package.toml`, input declarations, resources, and templates.
Generators may use the launch mechanisms supported by the package manifest;
test the declared inputs and generated output. Keep machine-specific defaults and
credentials out of public packages.

Changes to package formats, generator protocols, or consumer configuration need
design review because other repositories depend on those contracts. Include
focused package tests and update examples when behavior changes.

## Pull requests

- Use a feature branch or fork and open a PR against `main`.
- Explain what changed, why, and any compatibility or migration effects.
- List the checks you actually ran, their results, and verification gaps.
- Update relevant documentation and examples.
- Keep unrelated changes out of the PR and exclude credentials and local state.

AACT is [MIT licensed](LICENSE). Preserve applicable third-party license notices
when adding or changing bundled resources.
