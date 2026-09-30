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
- Whole-project verification is recorded below.

## Integrated capabilities

- Catalog/config/forms, native/terminal picker model and persistent TUI model: focused suites passed; contributor reports preserve test-first failures.
- Git mapper: synthetic native empty-PATH execution, race/vet and Windows cross-build passed (contributor report).
- Rendering/installation/agent registration: focused and race suites passed (contributor report). JSONC comments, ownership checks, generated companion sharing and copy fallback have regression tests.
- Docker runtime/actions: unit RED on missing APIs, GREEN focused/race; secret progress regression RED then GREEN. Real `go test -tags=integration ./tests/integration -run TestDockerMCP -count=1 -v` passed for build/start/mount/HTTP/SSE/list/stop against the synthetic fixture.
- Shared app/CLI: RED missing API, GREEN real temporary-home render/install/remove, missing-input exit2, JSON catalog, partial failure accounting and secret exclusion from saved answers. External URL registration uses a fake runtime proving Docker was not invoked.
- Public package contributor ran 94 container tests and real host transport checks for Azure/Forgejo against synthetic endpoints. These are not production-authentication checks.

No live agent configuration, production credentials or existing MCP containers were changed.


## Whole-project verification after review repairs

On 2026-09-30, the coordinator ran and observed successful exits from:

- `go test -race ./... -count=1` (all packages, including installer and migration integration tests).
- `go vet ./...`.
- `go test -tags docker_integration ./internal/mcp ./internal/packagehelpers ./internal/sessionsearch ./tests/packages -count=1` (real Docker lifecycle, auth preparation, native session launcher, Azure/Forgejo transport fixture tests).
- `go test -tags integration ./tests/integration -run TestDockerMCP -count=1 -v` (actual Docker HTTP/SSE fixture lifecycle).
- `sh tools/test-containers.sh` (94 retained Python tests in Docker across shared auth, cluster, Grafana, Azure, Forgejo and find-session).
- Consumer repo: `python3 -m unittest discover -s tests/aact -v` (2 tests).
- Independent review's temporary regression overlay across install/app/forms/MCP/config: all five reproduced failures now pass.
- `git diff --check` in both repositories.

### Review findings and regression coverage

The independent review identified 14 important findings and one minor finding.
Each actionable finding was repaired; the following coverage is included in the
final suites:

| Finding | Regression coverage |
| --- | --- |
| Selected-home uninstall and saved custom homes | Skill/MCP two-home preservation; persisted custom profile UI removal |
| MCP-menu agent selection and default target actions | Real service default-target start/auth orchestration; uninstall selection form |
| Scalar typed collections | Add/edit/remove string, integer, number and boolean elements |
| Unverified CLI lookup failures | Failed lookup refuses mutation; genuine absent name permits registration |
| Failed health/retry and replacement rollback | Unit failure compensation plus real Docker unhealthy-new cleanup and prior-container restoration |
| External MCP inventory | External registration appears in CLI/TUI inventory with Docker actions disabled |
| Worktree identities and concurrent discovery | Common-directory origin resolution; independent and same-source concurrent identity publication |
| Copied-checkout reassociation | Ordinary install refuses relocation; explicit update is required; failed zero-success update retains selected checkout |
| Prepare-derived options | Editor receives declared dynamic choices; required choices need no preliminary guessed value |
| Settings root and migrated agent preferences | Service/TUI use consistent settings keys and imported preferred agents |
| Open-ended SSE initialize response | Incremental matching response parsing without waiting for EOF or treating notifications as results |
| Registration name collisions | Full scoped key hash disambiguates hyphenated names |
| Agent config/ledger failure compensation | Register/unregister failures restore exact config bytes, mode and existence |
| Additional domain review: generic profiles | Default manual artifact names are portable and scoped to both profile and home |

The domain review's two additional app tests were observed RED before the fixes
and GREEN afterward. Original independent review reports remain unchanged as
historical evidence; a post-fix approval from that reviewer is not implied.

### Native UI smoke

A native macOS arm64 binary was opened in an isolated shell PTY. Catalog showed
all seven public packages; MCPs, Agents and Settings were independently reachable,
and quit exited successfully. No actual agent installation or existing state was
used. Native GUI dialog visual checks remain manual follow-up.

### Delivery boundaries

No live agent configuration, production credentials, legacy launcher or existing
MCP container was migrated or replaced. Vendor login uses fixtures; real
Kubernetes/Grafana/Forgejo/Azure sign-in remains a user follow-up. Native Windows
runtime evidence comes from CI, not from cross-compilation alone. Releases are
development archives; no version tag, release publication or main merge is part
of this delivery.

## Final archives and consumer integration

All six actual CGO-free archives built and passed `--verify-only` after final
notice normalization. The native darwin/arm64 release passed empty-PATH static
and generated Git-mapper install/uninstall checks. The coordinator independently
verified version `0.1.0-dev`, all six archive validations, and the actual consumer
checkout catalog containing 7 public plus 8 local entries.

Windows CI initially failed a Unix-permission assertion, a quoted Windows-path
diagnostic assertion, and worktree cleanup because pinned go-git leaked its
`commondir` handle. Native Linux/macOS test/vet/race, actual Docker CI, and the
six-target CI artifact job passed. Final Windows repair/rerun evidence follows.
