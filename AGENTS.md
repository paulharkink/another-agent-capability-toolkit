# Guidance for agents working on AACT

AACT installs agent skills and configures MCP servers. Read `README.md` and
`CONTRIBUTING.md` for project context and contribution requirements. Use the
[project vocabulary](docs/project-vocabulary.md) for the agreed meaning of
Capability Instance and Profile.

Use the documented [release procedure](docs/releasing.md). Merge the reviewed
feature PRs first, then run the repository's **Request release** workflow. Do
not create tags or start publication manually.

## Repository layout

- `main/cmd/` contains executable entry points; `main/internal/` contains the
  manager, TUI, state, runtime, and agent adapter implementations.
- `main/packages/` contains Go package sources; `packages/` contains bundled
  Capability definitions and their non-Go resources, including MCP servers.
- `test/` contains black-box Go tests in a mirrored tree; `docs/`
  contains project documentation and design history.

## Working on changes

- Preserve existing work in the checkout and keep unrelated changes out of the
  task. Reuse existing code and conventions where practical.
- Keep Capability definitions, runtime behavior, and agent-specific adapter
  behavior in their appropriate layers. Discuss changes to public contracts or
  architecture with the maintainer before making consequential decisions.
- Update relevant tests, documentation, and examples when behavior changes.
  Keep credentials, local state, and machine-specific configuration out of the
  repository and test output.
- Use isolated temporary state and agent homes for tests. Do not modify real
  user configurations, credentials, or existing MCP instances during testing.

## Verification and delivery

- Keep Go tests in the separate mirrored `test/` tree and make them black-box.
  Do not add tests that import `main/internal`, access private state, or exist
  only to cover implementation lines. For Go changes, format changed files with
  `gofmt`; use `go test ./test/... -count=1` and `go vet ./main/...` when the
  user asks for testing or verification. Follow `CONTRIBUTING.md` for race and
  container checks.
- Report what was tested and any checks that could not be run.
- Every agent-authored commit must include an `Agent-Conversation` trailer for
  each contributing session: platform, durable link or stable ID, start time
  with timezone, and project or workspace and opening user message when
  available. Redact secrets.
- Changes intended for `main` go through a pull request. No agent may write to
  `main` in any way, or merge any branch or pull request, without the user's
  explicit one-time approval for that specific action. Approval covers only
  the specified action once; every later write or merge requires fresh approval.
  A request to fix or build something, permission to open a pull request, or
  approval for an earlier action does not authorize a write to `main` or a merge.
- Do not publish a release or deploy without the user's explicit request.
