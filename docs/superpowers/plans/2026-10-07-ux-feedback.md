# UX feedback implementation plan and ledger

## Approved scope

- Make form dirty tracking semantic for typed defaults and saved lists. Reverting a value or active text buffer to its original value is clean. The unsaved summary contains field labels only: up to three labels plus a remaining count, never values or credentials.
- Let the workspace seed the baseline after applying preview defaults and destination state. Restored overrides retain the preview baseline. The workspace action is a single **Save and apply** button; Escape remains the guarded exit path.
- Keep keyboard navigation consistent: Up/Down stay within the focused panel, Left/Right switch panes or controls outside text editing, Tab/Shift+Tab cycle sections, details, and bottom actions, and text-edit arrows move the cursor.
- Make focus visible with bold reverse styling across pane headings, selected controls, area tabs, and bottom actions. Render scrollbar tracks and thumbs in scrollable form panes while retaining the existing scroll APIs.
- Preserve keyboard-only save, cancel, and picker flows and avoid layer-number or “FOCUSED” labels.
- Allow an MCP definition to name an unconditional declared string input for its registration name. Cluster Inspector and Grafana Inspector expose this as a public input. The home `target-a` environment packs default to `cluster-inspector-target-a` and `grafana-inspector-target-a`.

## Implementation sequence

1. Add failing form tests for dirty summaries, typed nil/empty lists, restored values, the single bottom action, panel navigation, and scrollbars.
2. Implement semantic answer equality, safe copying, label-only summaries, submit label/hide-cancel APIs, restored-value application, focus styling, navigation, and pane scrollbars.
3. Add failing catalog tests for missing, wrong-type, and conditional MCP registration-name references; add the typed field and validation.
4. Add the registration-name input to public inspector manifests and the selected home `target-a` pack values.
5. Run scoped form/catalog tests and report evidence to the root coordinator for integration verification.

## Ledger

- RED: `TestCopyAnswersPreservesTypedNilSliceSemantics` failed because copying `[]string(nil)` yielded `[]string{}`.
- RED: `TestEmptySavedListRepresentationsAreSemanticallyEqual` failed because `reflect.DeepEqual` treated nil and empty saved lists as different.
- RED: registration-name reference validation test failed to compile before `MCP.RegistrationNameInput` existed.
- GREEN: `go test ./internal/forms ./internal/catalog -count=1` passed after implementation (Go SDK at `/tmp/aact-ux-go1.27.1/sdk`).
- The root coordinator owns full-suite, race, lint, build, and UI walkthrough verification.
