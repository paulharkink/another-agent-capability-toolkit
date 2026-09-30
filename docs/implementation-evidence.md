# AACT implementation evidence

Evidence is appended per implementation domain, with failing tests before code and passing verification after.

- Toolchain: Go 1.27.1, official darwin-arm64 archive checksum verified.
- Repository: isolated feature/aact-mvp; initial main contains only an empty commit.
- Docker: hard requirement, treated as available per user instruction. Real tests are reported separately from injected/mock boundaries.

## Shared process runner (Task 5 foundation)

- RED: `go test ./internal/process -count=1` failed with undefined Run/Executor/ErrOutputLimit before implementation.
- The oversized-output regression then caught an embedded bytes.Buffer ReaderFrom bypass; the bounded writer was corrected while retaining the failing test.
- GREEN: focused suite passes, including exact stdin, stderr, exit failure, deadline cancellation, 16 MiB cap and native executor contract.

## Scoped state and ownership records (Task 4)

- RED: `go test ./internal/state -count=1` failed with undefined Key/Open/Installation/DefaultRoot before implementation.
- GREEN: focused suite passes for scoped answers, atomic invalid-value preservation, idempotent records/removal, path containment, cross-Store locks, native roots/override, and exclusive credential adoption.
- Command: `go test -race ./internal/state ./internal/process -count=1`.
- Whole-project suite remains in development while parallel domains are incomplete; these results cover only the named packages.

## Integrated capabilities (review pending)

- Catalog/config/forms, native/terminal picker model and persistent TUI model: focused suites passed; contributor reports preserve test-first failures.
- Git mapper: synthetic native empty-PATH execution, race/vet and Windows cross-build passed (contributor report).
- Rendering/installation/agent registration: focused and race suites passed (contributor report). JSONC comments, ownership checks, generated companion sharing and copy fallback have regression tests.
- Docker runtime/actions: unit RED on missing APIs, GREEN focused/race; secret progress regression RED then GREEN. Real `go test -tags=integration ./tests/integration -run TestDockerMCP -count=1 -v` passed for build/start/mount/HTTP/SSE/list/stop against the synthetic fixture.
- Shared app/CLI: RED missing API, GREEN real temporary-home render/install/remove, missing-input exit2, JSON catalog, partial failure accounting and secret exclusion from saved answers. External URL registration uses a fake runtime proving Docker was not invoked.
- Public package contributor ran 92 container tests and real host transport checks for Azure/Forgejo against synthetic endpoints. These are not production-authentication checks.

No live agent configuration, production credentials or existing MCP containers were changed.
