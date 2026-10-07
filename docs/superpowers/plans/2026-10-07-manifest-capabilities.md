# Manifest Driven Capability Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make capability setup forms and MCP cardinality declarative in schema version 1 manifests.

**Architecture:** Extend catalog parsing and validation with additive presentation metadata and zero-to-many MCP definitions. Carry the declarations through setup previews; bind and unbind skills and MCP registrations by capability; address MCP runtime and external endpoints by service identity; UI consumers use explicit declarations and generic fallback.

**Tech Stack:** Go, TOML, Go tests.

**Spec:** `docs/superpowers/specs/2026-10-07-manifest-capabilities.md`

## Global Constraints

- Schema version remains 1 with additive compatibility.
- Environment packs provide prefill/default values and target policy only.
- No package/input name heuristics in the manager.
- Conditions are simple equality against declared input controllers.
- All conditions on an input must match; only visible required inputs are validated, and hidden values are preserved.
- MCPs use a named identity for runtime, endpoint, registration, and installation records; no package-level singleton assumption may affect list declarations.
- Destination removal unbinds that capability's recorded effects while retaining unrelated installs and runtime lifecycle.
- TUI has generic Overview, Agents, and Information sections; Runtime and Logs require MCP; skill-only setup needs no target selection.
- Unsaved setup and registration drafts remain guarded across exit and overlay close paths.
- No systemwide dependencies; preserve existing workspace changes.

## Review Focus

- Legacy `[mcp]` remains loadable and cannot coexist with `[[mcps]]`.
- Every singleton `.MCP` access outside catalog is exercised against both legacy and list declarations.
- Duplicate MCP names and bad section/condition references fail with actionable errors.
- Inputs and conditions retain metadata through catalog load and preview.
- Skill-only and zero-input packages do not receive invented UI or target requirements.
- Optional inputs without UI sections keep input order.
- CLI and form validation agree when conditional required fields are hidden, and inactive values survive controller changes.
- External URLs can be supplied and registered independently for each named MCP; named runtime actions address the same service.
- Reapplying a capability removes only unchecked destination bindings and reports every MCP registration change.
- Hermes `--skills-only` remains explicit and does not start or register any MCP.
- Exit, back, and overlay-close paths retain unsaved setup and registration drafts behind a guard.

---

### Task 1: Catalog manifest schema

**Files:** `internal/catalog/model.go`, `internal/catalog/manifest.go`, `internal/catalog/manifest_test.go`, `internal/catalog/testdata/*`

- [x] Write failing tests for list MCPs, UI sections, conditions, fallback and invalid declarations.
- [x] Verify red, implement validation and accessors, verify green (`go test ./internal/catalog`).

### Task 2: Setup preview metadata

**Files:** `internal/viewmodel/setup.go`, owning setup preview builder and tests.

- [x] Add fields for sections, manifest UI presence, and MCP definitions while preserving the legacy boolean; setup builder population is owned by the app integration task.
- [x] Verify preview metadata survives package loading (`go test ./internal/app ./internal/catalog ./internal/tui`).

### Task 3: Public package manifests

**Files:** cluster-inspector, grafana-inspector, azure-inspector, forgejo, git-repo-map `package.toml`.

- [x] Declare sections, labels, and conditions in TOML; `TestPublicManifestsDeclareSetupPresentation` loads all five packages.

### Task 4: Integration ledger

- [x] Report catalog and preview interfaces to coordinator; waiting on app builder/UI integration.
- [x] Add shared visibility evaluation/filtering APIs, tests, and the additive multi-service external URL field.
- [x] Backend desired-state binding/unbinding for all components and named MCP runtime/external endpoints; selected destinations carry the complete desired binding set and removals preserve unrelated capability state.
- [x] Conditional validation parity across CLI and forms, including preserving inactive field values.
- [x] Generic TUI routes for skill-only and multi-MCP capabilities with no package/input heuristics.
- [x] Regression coverage for explicit Hermes `--skills-only` and unsaved exit/overlay guards.
- [x] Review production `.MCP` accesses outside catalog; package-helper/service uses normalize each selected MCP definition to the existing single-MCP implementation boundary, while UI/state/runtime identity remains per named child.

#### Final implementation and verification ledger (2026-10-07)

- Catalog schema/version-1 compatibility, additive `ui` presentation, input hints/conditions, and legacy `[mcp]` plus `[[mcps]]` validation are implemented and covered by parser/load tests.
- Setup preview carries manifest section metadata and all MCP definitions. Public package manifests declare the migrated labels, section order, hints, and Grafana conditions.
- Service and CLI support complete capability binding state, exact named MCP endpoints/runtime identities, per-child results, and removal of only the capability effects no longer selected. Hermes skill-only remains explicit.
- Forms share equality-condition evaluation with CLI validation; inactive values survive controller changes and hidden required values are not validated.
- TUI renders manifest sections and generic fallback routes, keeps Runtime/Logs conditional on MCP definitions, routes child runtime/diagnostic actions by exact identity, guards foreign/unknown ownership, and preserves unsaved drafts through exit paths.
- The scoped implementation and TUI fixture migration tests passed. Parent verification reports `go test ./... -count=1`, `go test -race ./internal/app ./internal/catalog ./internal/forms ./internal/mcp ./internal/state ./internal/tui -count=1`, and `go vet ./...` green.
- Native macOS build succeeded with version `pr9-3473264-manifest-workspaces`. Linux amd64 and Windows amd64 cross-builds succeeded; these are compile checks, not native Windows runtime tests.
- The opt-in Docker integration test passed for named-child isolation (root verification log: `/tmp/aact-manifest-real-docker-final.log`).
- No commits, pushes, or merges were made during implementation.

#### Final MCP preset route verification (2026-10-07)

- After restoring the MCP primary setup route, root reran `go test ./... -count=1` (PASS; `/tmp/aact-manifest-full-final3.log`), `go test -race ./internal/forms ./internal/tui -count=1` (PASS; `/tmp/aact-manifest-race-routes-final.log`), and `go vet ./...` (PASS; final4 log). Native macOS plus Linux amd64 and Windows amd64 builds exited 0.
- Manual native TUI walkthrough used the existing `codex/aact:0.0` process and real `/Users/pharkink/sources/agent-skills/aact.toml`: “Set up another target” opened a chooser with `home/pms15` and “Without an environment preset”; selecting `home/pms15` opened “Configure · Cluster Inspector · home / pms15” with saved port `18766`.
- Evidence is ANSI terminal capture, not an operating-system screenshot: `/tmp/aact-manifest-runtime-probe/evidence/final-real-mcp-preset-chooser.ansi` and `/tmp/aact-manifest-runtime-probe/evidence/final-real-named-workspace.ansi`.

#### Fixture migration note (2026-10-07)

- Setup tests now declare `HasManifestUI` and section metadata for package-specific layouts instead of relying on removed package-name or input-name grouping.
- Skill-only setup fixtures exercise immediate setup; tests that need an environment preset use the explicit preset action.
- Generic agent destination navigation is asserted as `Agents`; `Destinations` remains a package-declared section title when manifests provide it.
- Grafana conditional-auth fixtures declare equality visibility conditions, and Azure/Forgejo fixtures declare their public sections.
- These changes preserve target identity, credential switching, fixed-target exclusion, source provenance, and saved-input assertions while moving presentation ownership into manifests.
- Manifest malformed-declaration coverage now includes duplicate section IDs, inputs assigned to more than one section, and nonscalar condition values.
