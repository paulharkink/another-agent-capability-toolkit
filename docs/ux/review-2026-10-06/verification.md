# UX 17 journeys: implementation verification

Status: **DONE_WITH_CONCERNS — implementation and final recorded gates complete on source tree `ccc80714bc77be4057ffc894b408c30ccad66fe1`.** The concerns are verification boundaries: no live installation/migration, native chooser, interactive TTY session, production configuration, external runtime, remote endpoint, or Docker behavior was tested. Historical behavioral RED remains separate from current GREEN fixture evidence.

## Fixture and evidence boundaries

- The journey and visual fixtures load the real `packages/cluster-inspector/package.toml`, use a temporary target TOML (`api_server = 'https://cluster.fixture.invalid'`), temporary HOME/config/state roots, and an empty runtime that rejects unexpected Start/Stop/Logs calls. Production `Model.Update`, `Model.View`, `FormModel.Update`, and `FormModel.View` paths are exercised. Runtime detection may be empty because external executables are intentionally unavailable in the isolated fixture.
- The mixed registration test uses a temporary HOME and actual service adapters: OpenCode writes its JSON configuration and registration ledger; Claude is deliberately made unwritable by creating a directory where its `.claude.json` file belongs. After the service refresh message is delivered, the active workspace and reopened agent choices are checked against the achieved service state.
- The remembered-source service test changes process CWD to an unrelated temporary directory, then checks the remembered package root, exact target TOML and resolved input, installed source, and ledger identity.
- Captures under [`implementation-evidence`](implementation-evidence/) are ANSI output and color-rendered PNGs from production `Model.View` at the labeled fixture dimensions. They are deterministic fixture captures, not a running installed binary or native terminal screenshot. The 79×15 capture is the production minimum-size recovery screen.
- No Computer Use, native chooser, local user binary installation, new tmux session, Docker runtime, remote agent, or production user configuration was used. The headless macOS picker test gives the process an empty PATH so picker resolution returns unavailable before an OS dialog can be invoked.

## Historical behavioral RED

The initial focused command and output are preserved in [`task12-red.txt`](../../../tests/ux/evidence/task12-red.txt). It exited 1 on concrete behavior, not a missing API or compile failure:

1. At a supported 80×16 parent size, the active setup workspace clipped Save/Cancel; resizing that active workspace to 79×15 kept drawing the form instead of the main resize recovery message.
2. Actual mixed service registration persisted the successful OpenCode configuration and ledger row, and a direct service snapshot reported OpenCode. The still-open TUI workspace kept a stale profile after the returned load message because its form consumed that lifecycle message.

Additional regression tests were written before their corresponding production fixes and observed RED during the implementation session: the real successful-load retry remained busy, an unstructured successful settings result claimed `Saved: no`, a focused settings fact visually selected an unrelated action, and the active embedded picker let F3 reach the parent Model. Those failures were behavioral. The retained `task12-red.txt` is the original sizing/refresh RED transcript; it does not purport to contain every subsequent test's pre-fix output.

## Current GREEN gates

The exact final controller commands, exit codes, test stdout, and cross-test-binary compilation results are retained in [`final-gates.txt`](../../../tests/ux/evidence/final-gates.txt). Those gates used immutable source tree `ccc80714bc77be4057ffc894b408c30ccad66fe1` and include the named journey tests and chosen-auth capture assertions. Cross-target checks compile without cgo and do not execute Windows/Linux binaries.

The following commands have been run on this tree with the isolated Go SDK:

```sh
PATH=/tmp/aact-ux-go1.27.1/sdk/bin:$PATH GOROOT=/tmp/aact-ux-go1.27.1/sdk GOTOOLCHAIN=local GOPATH=/tmp/aact-ux-go1.27.1/gopath GOCACHE=/tmp/aact-ux-go1.27.1/gocache go test ./internal/forms ./internal/tui -run TestUX -count=1
# PASS: internal/forms, internal/tui

PATH=/tmp/aact-ux-go1.27.1/sdk/bin:$PATH GOROOT=/tmp/aact-ux-go1.27.1/sdk GOTOOLCHAIN=local GOPATH=/tmp/aact-ux-go1.27.1/gopath GOCACHE=/tmp/aact-ux-go1.27.1/gocache go test ./internal/forms ./internal/tui ./internal/app -count=1
# PASS: internal/forms, internal/tui, internal/app

PATH=/tmp/aact-ux-go1.27.1/sdk/bin:$PATH GOROOT=/tmp/aact-ux-go1.27.1/sdk GOTOOLCHAIN=local GOPATH=/tmp/aact-ux-go1.27.1/gopath GOCACHE=/tmp/aact-ux-go1.27.1/gocache go test ./... -count=1
# PASS: every package, including integration and package suites

PATH=/tmp/aact-ux-go1.27.1/sdk/bin:$PATH GOROOT=/tmp/aact-ux-go1.27.1/sdk GOTOOLCHAIN=local GOPATH=/tmp/aact-ux-go1.27.1/gopath GOCACHE=/tmp/aact-ux-go1.27.1/gocache go vet ./...
# PASS: no diagnostics

PATH=/tmp/aact-ux-go1.27.1/sdk/bin:$PATH GOROOT=/tmp/aact-ux-go1.27.1/sdk GOTOOLCHAIN=local GOPATH=/tmp/aact-ux-go1.27.1/gopath GOCACHE=/tmp/aact-ux-go1.27.1/gocache go test -race ./... -count=1
# PASS: every package
```

After the final discoverability-only test update, each named journey was rerun with:

```sh
go test ./internal/tui -run '^TestUXJourney(0[1-9]|1[0-7])$' -count=1
# PASS: all 17 separately discoverable journey entries
```

Cross-target compile checks in the linked transcript passed with `CGO_ENABLED=0`, `GOOS=linux GOARCH=amd64`, and `GOOS=windows GOARCH=amd64`: `go build ./...` plus `go test -c` for `internal/app`, `internal/forms`, `internal/tui`, `tests/integration`, and `tests/packages` on each target. Ten test binaries were written under `/tmp/aact-ux-final-cross.yVfl2k`; the checks compile but do not execute them. The Windows build downloaded `github.com/Microsoft/go-winio v0.6.2`, already in the module graph, into the isolated Go module cache; repository `go.mod` and `go.sum` were not changed.

## Journey map and evidence scope

`TestUXJourney01` through `TestUXJourney17` are separately discoverable top-level tests. Each shares the journey case table and invokes the mapped production interaction/service assertion; adjacent focused tests below cover additional details. The table describes the exact scope of each mapping. It does not mean every behavior is a dedicated end-to-end scenario or a separate screenshot.

| Journey | Executable evidence | Scope and boundary |
|---|---|---|
| 01 | `TestUXMultipleTargetsOpenLocalChooserAndSelectionOpensCorrectKey` | Keyboard target chooser selection reaches the exact target key. |
| 02 | `TestUXCapabilityAndTargetDetailsContainRealInformation`; `TestUXCaptureProductionViewsForReview` | Actual package, target, provenance and destination detail; production fixture views at 100×23 and 170×42. |
| 03 | `TestUXEnvironmentTargetUsesSharedInsetEditorAndBackRestoresParent` | Shared editor and return to the originating Environments selection. |
| 04 | `editPasteJourney` | Production Model keyboard route edits an existing value, handles bracketed Unicode paste, submits Ctrl-S, and checks the exact backend request. |
| 05 | `TestUXGrafanaSessionCookieInputsAllInAuthenticationAndIrrelevantCredentialsConditional`; production Authentication capture | Authentication alternatives, relevant/irrelevant credential grouping, and labeled fixture rendering. |
| 06 | `TestUXReturnToConfigurationPreservesSubmittedDraftAndOrigin`; `TestUXSaveErrorRecoveryPreservesActualAchievements` | Foreground error/draft recovery plus actual successful/failed registration outcomes from the service. |
| 07 | `TestInteractionSetupShowsDatabaseAndNamedMCPDestinationsInRightPane`; production Agents/Destinations capture | Database and named MCP destinations; the latter is a separate actual catalog view. |
| 08 | `TestUXForeignEndpointCanRegisterWithoutRuntimeControl` | Foreign endpoint can be registered to a named local agent without local runtime control. |
| 09 | `TestUXActivePickerOwnsParentShortcutKeys`; `TestUXPickerEscapeReturnsSameFieldAndDraft`; `TestInteractionSetupEscapeCancelsWithoutApplying` | Headless actual parent Model route verifies F3 stays inside unavailable-native picker and Escape restores the same field/draft; form tests verify picker selection/cancel. Separate setup cancellation test covers canceling the setup itself. |
| 10 | `directoryCollectionJourney`; `TestUXDirectoryCollectionAddUsesEmbeddedPickerAndAddsOneRow`; `TestUXPickerSelectionUpdatesOneDirectoryRow` | Model keyboard edit/backspace and picker-driven collection add/update; not an OS chooser. |
| 11 | `TestUXForeignEndpointCanRegisterWithoutRuntimeControl`; `TestUXRemoveShowsLegacyRecordedMCPAndSendsExplicitIDs`; `TestUXRemoveDoesNotStopServer` | Registration ownership boundary and explicit, narrow removal request without runtime stop. |
| 12 | `TestUXNoContainerLogsExplainsNextStepWithoutCallingDockerLogs`; `TestUXRunningLogsBelongToSelectedTargetAndPauseDoesNotMoveSections`; `TestLogFollowRefreshesAndPauseStopsPolling`; `TestFollowingLogsStartsAtTailAndScrollingPauses`; `TestUXClosingLogsDoesNotStopRuntime` | Empty state avoids a runtime logs call; a running target's logs follow/pause, scroll from tail, remain scoped to the selected target, and close without stopping its runtime. All use deterministic backends; no live Docker behavior is asserted. |
| 13 | `TestUXEnvironmentPresetDoesNotImplyInstalledAndNoSavedDuplicateRows`; `TestUXJourney13RememberedSourcePreviewAndInstallIgnoreProcessWorkingDirectory` | Preset identity/navigation and app-level remembered-source preview/install after actual process CWD change. |
| 14 | `TestUXAgentsOverviewEnterFocusesDetailsAndDetailsShowsResolution`; `TestUXSaveErrorRecoveryPreservesActualAchievements` | Agent detection/resolution paths plus actual achieved-registration state after mixed success/failure. External-agent discovery remains absent in the isolated fixture. |
| 15 | `TestUXSettingsCategoriesHaveRelatedControlsOnly`; `TestUXHelpMatchesCurrentKeyGrammarAndReturnsOrigin`; `TestUXSettingsHighlightsOnlyTheFocusedDetailControl` | Settings category/action grouping, help return route, and focused fact/action visual distinction. |
| 16 | `TestHomeLayerTwoScrollShowsContinuationCues`; `TestUXFocusedPaneShowsOffscreenCueAndBoundedButtonsAtAllSupportedSizes` | L2 continuation and standalone editor scrolling/action bounds at 80×16, 100×23, 170×42; active workspace geometry/recovery also checked. |
| 17 | `TestUXAzureAndForgejoTaskSections` | Capability-specific task sections and field placement. |

Additional regression gates: `TestUXUnstructuredSettingsSuccessDoesNotInventUnsuccessfulSave` confirms unknown result metadata is omitted rather than rendered as false success/failure; `TestUXSuccessfulLoadRetryEndsOnlyItsOwnProgress` verifies the actual load-retry completion path; `TestUXDecorativeFactsCannotLookLikeSelectableActions` and `TestUXSettingsHighlightsOnlyTheFocusedDetailControl` inspect selection styling; `TestUXAllScreensKeyboardOnlyNoBatch` checks keyboard submission reaches one concrete setup request without a batch step.

## Visual artifacts

All captures are generated from the real production `Model.View` output and paired with the ANSI source. Reproduce them from the repository root with:

```sh
export AACT_UX_CAPTURE_DIR="$PWD/docs/ux/review-2026-10-06/implementation-evidence/ansi"
go test ./internal/tui -run '^TestUXCaptureProductionViewsForReview$' -count=1
/Users/pharkink/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3 tests/ux/render-captures.py \
  docs/ux/review-2026-10-06/implementation-evidence/ansi \
  docs/ux/review-2026-10-06/implementation-evidence/png
```

- [Overview at 80×16](implementation-evidence/png/overview-at-minimum.png) · [ANSI](implementation-evidence/ansi/overview-at-minimum.ansi)
- [Connection at 100×23](implementation-evidence/png/connection.png) · [ANSI](implementation-evidence/ansi/connection.ansi)
- [Authentication at 100×23](implementation-evidence/png/authentication.png) · [ANSI](implementation-evidence/ansi/authentication.ansi)
- [Authentication detail pages 1 and 2](implementation-evidence/png/authentication-detail-page-1.png) · [page 2](implementation-evidence/png/authentication-detail-page-2.png) · [scrolled view](implementation-evidence/png/authentication-detail-scrolled.png)
- [Token selected; kubeconfig visibly inactive](implementation-evidence/png/authentication-token-selected-kubeconfig-inactive.png) · [kubeconfig selected; token visibly inactive](implementation-evidence/png/authentication-kubeconfig-selected-token-inactive.png)
- [Agents and destinations at 100×23](implementation-evidence/png/agents-destinations.png) · [pages 1–3](implementation-evidence/png/agents-destinations-page-1.png) · [page 2](implementation-evidence/png/agents-destinations-page-2.png) · [page 3](implementation-evidence/png/agents-destinations-page-3.png)
- [Information at 170×42](implementation-evidence/png/information.png) · [Settings](implementation-evidence/png/settings.png) · [foreground failure](implementation-evidence/png/foreground-failure.png) · [skill-only, no MCP](implementation-evidence/png/skill-only-no-mcp.png)
- [Below-minimum recovery at 79×15](implementation-evidence/png/workspace-below-minimum.png) · [ANSI](implementation-evidence/ansi/workspace-below-minimum.ansi)

The two chosen-auth captures use the real package and Model keyboard route to paste and commit synthetic fixture values, assert the stored choice and visibly muted sibling, and never open a native picker. The focused visual interaction suite additionally asserts the 80×16 active workspace keeps Save/Cancel and a complete Back hint below the main pane header, and that 79×15 switches to the recovery message while preserving its draft and allowing only the documented recovery/quit controls. A completed result remains visible and its own controls remain usable at that size; it does not expose hidden form controls. The suite checks editor scroll cues and fixed actions across all three supported sizes. The captures include representative screens, not 17 separate complete journeys. Remaining native picker, live TTY, external runtime, remote endpoint, and Docker behavior are unverified by design.
