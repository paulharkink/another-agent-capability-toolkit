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

## Native CI closure

Code commit `586110685231181e719f2ced7fd62f30b8a4c19a` passed
[native/container CI](https://github.com/paulharkink/another-agent-capability-toolkit/actions/runs/36780447912):

- Windows: full Go tests and vet, including native PowerShell installer fixtures.
- Linux and macOS: full Go tests, vet and race checks.
- Linux Docker: actual lifecycle and package/helper tests plus all retained container suites.

The mapper repair has a regression that failed with 24 retained worktree
metadata descriptors before the fix and passed afterward; Windows additionally
asserts that metadata can be deleted immediately after scanning. The credential
content test runs on every OS; Unix mode assertions are restricted to Unix
because Windows Go chmod only controls the read-only attribute.

Final six-target [archive CI](https://github.com/paulharkink/another-agent-capability-toolkit/actions/runs/36780447882)
also passed build, verification and artifact upload on code commit `5861106`.
After that repair, the local six archives were rebuilt and independently
verified again. Native empty-PATH smoke included both a main Git checkout and
a linked worktree in generator output and generated skill install/uninstall.

### Local archive SHA256

Archives and sidecars are under `dist/`; native extracted resources are under
`dist/native/`. These development builds are retained locally and are not a
published version tag.

| Archive | SHA256 |
| --- | --- |
| `aact_0.1.0-dev_darwin_amd64.tar.gz` | `b4e08077f7c6edcf784e4e4c69b832886afabfa8a37e74f3f1310653601625dc` |
| `aact_0.1.0-dev_darwin_arm64.tar.gz` | `87c240b605a1117a74c51bc33ac8f10c3a59c0a46289e91e118af598029271d8` |
| `aact_0.1.0-dev_linux_amd64.tar.gz` | `52d53790a9f44b15570bc06120471a7e83616fb599883a7aaf48c9485b37b075` |
| `aact_0.1.0-dev_linux_arm64.tar.gz` | `5c1f6ee6639d6cc454a8f7d09d858ddcaf849d41a6f060e21bb8dc9a8ff14a0a` |
| `aact_0.1.0-dev_windows_amd64.zip` | `fcd4976405e5aff8bbbd98d306d965bdd0ef52d46fff82a1718d6263411c2423` |
| `aact_0.1.0-dev_windows_arm64.zip` | `644979e721e608813ab32c9a8e0c53bae5e680225731c06238880b647b525cdb` |

### Acceptance coverage

| Approved criterion | Evidence |
| --- | --- |
| Native portable installation/runtime | Six real archive builds; native OS installer CI; empty-PATH packaged runtime smoke |
| Mixed private/public checkout | Actual packaged catalog returned 15 entries; consumer fixture tests |
| Questions, editable prefill and noninteractive CLI | Forms/app/CLI tests, dynamic prepare-choice regressions |
| Direct and computed Mustache | Rendering/generator fixtures, failure-preservation tests, native mapper smoke |
| One-at-a-time directory collections | Picker/form typed collection and fallback tests; GUI visual check pending |
| Global selected-agent installation and ownership | Native temporary-home round trips; cross-home, copy, shared content and foreign-resource tests |
| Inspector extraction and MCP inventory | 94 preservation tests, actual Docker/host transport fixtures, external inventory and runtime rollback tests |
| Migration with state preservation | Migration/integration fixtures; dry-run zero-write tests; no live apply |
| Portable mapper | Multi-root/host/worktree/duplicate checkout tests, leaked-handle regression and native empty-PATH generator |
| Shared CLI/TUI operations and independent menu | App/CLI/TUI models and native four-view PTY smoke |

All implementation tasks were delivered. The only deliberately unperformed
checks are real-account/vendor sign-in and native GUI visual testing. Marketplace
support remains the agreed future scope.

## 2026-10-02 Norton Commander TUI redesign (current draft PR work)

The preceding acceptance summary covers the original MVP. The later approved
two-pane TUI and independent Windows/WSL runtime ownership design are being
implemented on the existing `feature/aact-mvp` branch and draft PR #1.

- Runtime ownership uses a persistent state-root installation ID, Docker owner
  labels, and a local last-action record. List reports live Docker state and
  foreign ownership independently. Start/Stop/Logs reject foreign containers.
- The home TUI now has capability and related MCP-profile panes with keyboard,
  function-key, menu, scrolling, resize, and mouse paths. Saved never-started
  profiles and foreign Docker profiles remain visible; foreign runtimes can
  configure and remove current-environment agent registrations only. An explicit
  Check connection action observes the selected endpoint without registering it.
- The capability Install/Parameters action now opens one typed, scrollable form
  with editable declared inputs, provenance, and destination selection. Save passes
  its complete answers to the noninteractive install service. The Agents screen
  distinguishes client detection from config-file presence and can show exact
  config bytes without masking. The Environments screen reads actual target TOML,
  displays malformed target files as error rows, and views exact contents while
  keeping saved profiles visibly separate from files on disk.
- Owned MCP logs open in a bounded, scrollable viewer that polls the last 200
  Docker log lines once per second while Follow is active. Keyboard and mouse
  wheel scrolling pause it; `f` or the footer resumes it. Closing leaves the
  runtime running.
- Environment target rows now open the same typed setup form for that target;
  No environment file opens setup for the selected home capability. Saved-profile
  rows are not treated as target TOML. Completed operations show a full-screen
  scrollable result with all per-agent messages and errors.
- Skill-only installation supports the global `all` destination under
  `~/.agents/skills`; MCP packages reject it before any runtime mutation.
- Claude Code user-scope MCP JSON was verified with a temporary
  `CLAUDE_CONFIG_DIR` CLI probe. OpenCode's JSONC precedence and shadow-entry
  handling have tests. The incorrect JetBrains AI Assistant JSON writer was
  disabled after the installed IDE's XML settings were inspected.
- Agent discovery now probes CLI presence and lists user config paths on native
  Linux and Windows, with WSL explicitly scoped to Linux files. The new paths
  have fixtures and official documentation evidence; native Windows/WSL client
  installations have not been inspected live.
- Settings can save default named MCP agents for future setups without touching
  existing registrations. The default CLI and TUI destinations now honor
  `CODEX_HOME` and `XDG_CONFIG_HOME`; explicit agent homes retain their paths.
- A native PTY smoke opened the two-pane home, Main menu, Environments, Actions,
  and a no-environment setup form without changing live agent configuration.
  It exposed long provenance paths hiding values at 80 columns; a failing TUI
  regression led to separate selected-field source hints, leaving values visible.
- Independent review found that an unsupported JetBrains MCP destination could
  start Docker before adapter validation. A failing regression demonstrated the
  side effect; service validation now rejects it before runtime start, and the
  setup form omits unsupported MCP destinations. Registration-only reconciliation
  now leaves unchanged local agent registrations untouched.
- After the final changes, `go test ./... -count=1`, `go test -race ./...
  -count=1`, `go vet ./...`, `git diff --check`, and the isolated real-Docker
  `TestDockerMCP` fixture all passed. CGO-free `cmd/aact` cross-builds passed for
  darwin/arm64, linux/amd64, linux/arm64, windows/amd64, and windows/arm64.
  The Docker fixture's Stop assertion distinguishes a removed container from a
  retained local last-action record. Native Windows/WSL interaction remains
  unverified by these cross-builds.

The redesign is not yet at full mockup parity. Existing-profile parameter
Save/apply now retains valid answers on generator, skill, registration, and MCP
startup failure. Changed running MCP settings invoke Stop and a new Start,
without restoring the old runtime on failure. Existing-profile Parameters opens
its exact target, and an attempted setup preselects only fully installed
destinations. TDD regressions first failed for discarded inputs, default
checkboxes on uninstalled profiles, disabled Parameters, stale success rows,
and error details below the first result page. Focused tests then passed.
Fresh `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet
./...`, and the real Docker `TestDockerMCP` integration fixture passed after
this change. The real Docker fixture covers ordinary start/stop and health;
the changed-settings restart path is covered with a controlled runtime test.
The `exclusive_group` manifest field now validates mutually exclusive inputs
in the form and backend. Explicitly entering Token clears Source kubeconfig
and vice versa; a prefill alone clears neither. The public Cluster Inspector
manifest declares this group. A regression caught target prefills resurrecting
a cleared credential on reopening; saved local edits now take precedence over
editable source and target defaults. The target-only `[aact.input_policy]`
table now makes fixed target values authoritative in TUI, interactive CLI, and
noninteractive CLI paths; fixed fields are hidden from setup and Parameters.
After the credential and precedence changes, full tests, race tests, vet, the
real Docker fixture, and CGO-free builds for darwin/arm64, linux/amd64,
linux/arm64, windows/amd64, and windows/arm64 passed.

TOML authoring and complete native agent/plugin detection still need
implementation or review. Stop currently removes its Docker container while retaining last-action
state; the browser mock's retain-container choice was not yet accepted as a
terminal runtime contract. The public GitHub Pages mock remains unpublished.

## Terminal layout comparison, 2026-10-02

Rendered the TUI model at 120×24 for Home, capability Actions, Main menu,
Agents, profile Actions, and setup, and compared those screens with the approved
written interaction specification. Home uses the related two-pane layout, the
Main menu is an overlay rather than a pane, profile Actions include disabled
reasons, and setup combines inputs and destinations. The comparison found that
the shared input form was an unframed short block with Save floating above the
bottom. A failing layout test was added first; the form now fills the terminal
with a frame and keeps Save/Cancel and key guidance at the bottom. Existing
keyboard, mouse, scrolling, and validation tests pass with that layout.

The live browser mock at localhost requires a session key unavailable to this
terminal session. This was a comparison against its approved written spec and
captured TUI renders, not a pixel-by-pixel comparison with the browser. Native
terminal rendering, colors, and every mock interaction still need direct visual
review before claiming full mockup parity.
