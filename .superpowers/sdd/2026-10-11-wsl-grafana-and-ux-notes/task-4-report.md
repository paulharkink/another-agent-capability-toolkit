# Task 4 report: Git Repo Map directory picker shortcut

## Changes

- The split-layout help footer for focused file/directory collections now renders `b - Browse`, making the existing picker shortcut explicit on the Git Repo Map Add directory row.
- Added a focused test that selects the directory Add row and checks for the cue, plus a non-path string collection assertion that the unavailable Browse shortcut is absent.
- No picker behavior, navigation, or path expansion changed.

## RED/GREEN evidence

- RED: `go test ./internal/forms -run '^TestSplitDirectoryAddRowShowsBrowseShortcut$' -count=1` failed on the exact focused Add directory screen. The footer showed `b Browse`, without the requested separator/cue text.
- GREEN: the focused test passed after the split-layout footer change.

## Verification

- `go test ./internal/forms -run 'Browse|Directory' -count=1` — passed.
- `go test ./internal/forms -count=1` — passed.
- `git diff --check` — passed.

No pack files were edited. The pre-existing untracked plan file was left untouched. No push or merge was performed.
