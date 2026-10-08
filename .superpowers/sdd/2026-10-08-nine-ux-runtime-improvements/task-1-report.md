# Task 1 — Persist and display active credential method

Base: `d5423fb080b8542fe792e5239d12a0f12145ce2e`
Branch: `feature/restructure-concepts`
Status: implementation and focused verification complete; awaiting independent review and root commit authorization. Two explicitly authorized Task 9 fixture-test updates are present separately in `internal/app/service_test.go` and `internal/app/ui_setup_test.go`. No files staged or committed.

## Architecture ruling

Per root and planner ruling, answer records retain JSON version 1 and gain optional `active_input_groups` and `pending_auth_input_groups` metadata. Active selections map a declared `ExclusiveGroup` ID to a declared member input name. Pending transitions map each MCP child identity to the group/member selection whose authenticate action still needs to run. Pending state is only an unfinished transition marker; it is not credential provenance, auth validity, or service verification. Secret values remain excluded from answer values and action input maps. State root, hash, file paths, and `Answers()` behavior stay the same. Old answer records without metadata load normally. Invalid active group/member values are ignored when read from state and rejected when explicitly submitted. Fixed profile inputs take precedence over current selection. Legacy profiles derive a method only from one uniquely populated declared member. Managed credential-file presence does not set the selected method or establish authentication success.

A method change marks each selected MCP child's transition before auth. An auth error, cancellation, or `AuthRequired` response leaves that child's marker pending. A successful action clears only that child's markers matching the exact active-group snapshot. Other MCP children retain independent markers. This allows unchanged managed credential material to be reused only when no method transition is pending.

Review adjudication refined fixed-input behavior. A fixed but unfilled boolean (`false`) is permitted by profile policy resolution and must not be treated as an active auth method just because submitted or saved metadata names it. The resolver now ignores such explicit/saved selections and falls back to a uniquely populated editable member or unknown. Fixed populated members still override both explicit and saved selections. A fixed empty string is rejected earlier by `fixedTargetInputs`; it cannot reach active-method resolution. Explicit selection of an editable omitted secret remains valid for re-entry.

## Fail-first evidence

Before adding the state API, the focused state regression failed at compile time because `SaveAnswersWithActiveInputGroups` and `ActiveInputGroups` did not exist:

```text
internal/state/store_test.go:116:17: store.SaveAnswersWithActiveInputGroups undefined
internal/state/store_test.go:123:26: reopened.ActiveInputGroups undefined
FAIL internal/state [build failed]
```

The managed-credential switch regression was also run red before its implementation:

```text
--- FAIL: TestSwitchingCredentialMethodDoesNotReuseAnotherMethodsManagedCredential
    ux_save_auth_apply_test.go:473: switched method reused managed material without authentication: []app.uxActionCall(nil)
FAIL internal/app
```

The first failure established missing durable metadata; the second showed that an old managed credential was incorrectly reused after switching methods.

An expanded fail-first retry regression initially remained red:

```text
--- FAIL: TestSwitchingCredentialMethodDoesNotReuseAnotherMethodsManagedCredential
    ux_save_auth_apply_test.go:477: retry reused old method's managed material: []app.uxActionCall{... Action:"authenticate" ...}
FAIL internal/app
```

The first failed auth was retried without another auth action because selected-method persistence alone cannot represent an unfinished transition. The pending marker then fixed this case; the follow-up test runs auth twice while the old managed file remains present.

The fixed-false regression was run red before the resolver guard:

```text
--- FAIL: TestActiveInputGroupResolutionIgnoresFixedFalseMemberSelection/explicit
    fixed false member displaced populated editable member: map[string]string{"credential-choice":"use_source"}
    fixed false member was treated as an active auth method without an alternate: "use_source"
FAIL internal/app
```

The characterization test for a fixed empty string passed before the guard, demonstrating that case is rejected before active-method resolution rather than overridden there.

## Changes

- `internal/state/store.go`: optional answer-record metadata and atomic read/write accessors; regular `SaveAnswers` preserves metadata unless explicitly replaced.
- `internal/state/store.go`: per-MCP pending-auth group markers, snapshot-matched clearing, and preservation across ordinary answer writes.
- `internal/forms/editor_ui.go`: track selected declared group member separately from field values, expose it for submission, display the selected method after reopen, and prompt to re-enter an omitted secret.
- `internal/app/profile_apply.go`, `internal/app/service.go`, `internal/app/ui_setup.go`: validate active group selections against declarations, apply fixed-value and legacy-value precedence, persist selection with redacted answers, and require authentication when a selected method changes.
- `internal/viewmodel/setup.go`, `internal/tui/model.go`, `internal/tui/setup.go`: carry the selection separately through setup preview, form submission, failure/retry, and reset handling. The model captures the selection before clearing the completed form.
- Regression tests cover generic declared alternatives, empty alternate values, redaction, close/reopen, pasted secret re-entry, invalid members, fixed-input precedence, retry after auth failure with stale managed material, AuthRequired retention, and independent multi-MCP markers across one child success and another child failure.
- Fixed false explicit/saved method metadata cannot displace a populated editable alternative or create an active method by itself; a fixed populated method wins over both saved and explicit selection.

## Verification

Pinned toolchain: `.superpowers/sdd/2026-10-08-capability-packs-profiles-adapters/go/bin/go` (Go 1.27.1).

Focused rerun after the pending-transition implementation:

```text
PASS internal/state
PASS internal/forms
PASS internal/app
PASS internal/tui
```

Race run with the focused Task 1 selection:

```text
go test -race ./internal/forms ./internal/app ./internal/state ./internal/tui -run 'TestActiveInputGroups|TestActiveCredentialMethod|TestActiveInputGroupResolution|TestSwitchingCredentialMethod|TestActiveExclusiveMethod|TestReopenedSecretMethod|TestSwitchingAuthenticationClearsPreviouslyPrefilledCredential|TestBothPrefilledCredentialsKeepOnlySelectedMethod|TestExclusiveCredentialsShowMethodAndInactiveBranch|TestExplicitEmptyOptionalFieldClearsPreviousSourceValue' -count=1
PASS all four packages
```

Affected package suite:

```text
go test ./internal/forms ./internal/app ./internal/state ./internal/tui -count=1
```

The two Task 9 tests were updated per root's cross-lane fixture ruling: IntelliJ is excluded from default-eligible options, and the disabled-reason regression now injects an undetected MCP-capable supported adapter. These are test-only Task 9 changes, not Task 1 implementation changes. The rerun passed all four packages:

```text
ok internal/forms
ok internal/app
ok internal/state
ok internal/tui
```

Focused Task 9 fixture tests also passed:

```text
go test ./internal/app -run 'TestUIAgentDefaultOptionsExcludesAllAndManualOrUnsupportedAdapters|TestUISetupPreviewDisablesUndetectedSupportedMCPAdapterWithReason' -count=1
PASS
```

Task 1 fixed-input edge-case regression race run:

```text
go test -race ./internal/app -run 'TestActiveInputGroups|TestPendingAuthInputGroups|TestActiveCredentialMethod|TestActiveInputGroupResolution|TestFixedEmptyStringInputPolicyIsRejected|TestSwitchingCredentialMethod|TestPendingCredentialTransition|TestPendingCredentialTransitions|TestActiveExclusiveMethod|TestReopenedSecretMethod|TestSwitchingAuthenticationClearsPreviouslyPrefilledCredential|TestBothPrefilledCredentialsKeepOnlySelectedMethod|TestExclusiveCredentialsShowMethodAndInactiveBranch|TestExplicitEmptyOptionalFieldClearsPreviousSourceValue' -count=1
PASS internal/app
```

`git diff --check` passed.

## Review and commit

Independent review round 1 otherwise aligned with the implementation; the fixed-false guard above was the only code finding and is covered by a new fail-first regression. Await scoped review of that resolution and root's serial commit authorization. No local commit created. Task 6's separate generic diagnostic-consumer integration was intentionally left out of this task checkpoint.
