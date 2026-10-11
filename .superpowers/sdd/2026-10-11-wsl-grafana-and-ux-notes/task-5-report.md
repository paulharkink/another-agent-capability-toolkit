# Task 5 report: Render information and logs using the actual pane width

## Changes

- Added `FormModel.SectionContentWidth`, derived from the split-pane layout and reserving both the two-cell read-only content prefix and the scrollbar cell. The same value now controls Information pre-wrapping and the form renderer's read-only content wrapping.
- Updated `splitDisplayLine` to wrap at whitespace when possible while preserving source text; long unbroken strings still hard-wrap by terminal cell width.
- Clamped composed log overlay rows to the terminal width. Horizontal pan and the original log row data remain intact.
- Added wide/narrow resize coverage for long Information and log rows, form and terminal row geometry, word-boundary wrapping, and horizontal log panning.

## TDD and verification

- RED: `go test ./internal/tui -run 'TestWorkspaceInformationUsesAvailablePaneWidthAcrossResizes' -count=1` failed at 200 columns because a long Information line occupied only 99 cells. The 80-column case remained within the pane.
- RED: `go test ./internal/tui -run 'TestInformationWrapKeepsWordsTogetherWhenPaneAllows' -count=1` failed because the old helper split “material” between lines.
- The long-log resize test also detected a rendered log overlay row one cell wider than the 200-column terminal before clamping.
- GREEN: `go test ./internal/tui -run 'Information|Logs|Result.*(Width|Geometry)|Wrap|WorkspaceInformationUsesAvailablePaneWidth|LongLogLineClips' -count=1` passed.
- GREEN: `go test ./internal/forms ./internal/tui -count=1` passed.
- `git diff --check` passed.

No pack files were changed.
