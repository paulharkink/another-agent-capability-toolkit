# UX journey fixtures

These fixtures exercise the production Bubble Tea `Update` and `View` paths
without opening native dialogs, launching Docker, invoking a local user agent,
or creating a terminal session.

## Deterministic fixtures

- `internal/tui/ux_journeys_test.go` maps the 17 approved journey IDs to real
  keyboard `Update` paths and service-boundary assertions. Form editor, picker,
  collection, and section-specific regression tests remain in
  `internal/forms/*_test.go` and run alongside the TUI journey tests.
- Journey 06 uses a temp HOME and state store, a real OpenCode JSON config
  write, and a real failure caused by `.claude.json` being a directory. Runtime
  observations return an empty deterministic fixture. The expected UI state
  after refresh is one actual OpenCode registration and no Claude registration.
- Journey 13 changes process CWD to an unrelated temp directory, then checks
  preview root, exact target TOML and path, resolved input, installed source
  content, and state ledger identity against the remembered source.

## Evidence

`evidence/task12-red.txt` is the captured behavioral RED from the focused TUI
suite. It shows the actual mixed-service operation persisted OpenCode while
the active TUI form failed to consume the returned profile refresh; the service
snapshot queried directly from the same state store already includes OpenCode.
This file is diagnostic evidence, not a screenshot or deployed-state claim.

Current production `Model.View` ANSI captures and rendered PNG cell views are
in [`implementation-evidence`](../../docs/ux/review-2026-10-06/implementation-evidence/).
The [verification report](../../docs/ux/review-2026-10-06/verification.md)
maps all 17 named journeys to their exact test scope, documents the deterministic
fixture boundaries, and links the final gate transcript. `evidence/final-gates.txt`
is the controller's exact output for immutable source tree 844d; later capture-only
test assertions are explicitly identified in the report.
