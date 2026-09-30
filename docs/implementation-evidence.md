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
