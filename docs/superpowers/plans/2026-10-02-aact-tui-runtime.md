# AACT TUI and Runtime Ownership Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current four-screen prototype with the approved Norton Commander style TUI and give each AACT installation truthful, independent MCP runtime ownership.

**Architecture:** The TUI has two home panes, a modal stack, and management screens; it calls typed application operations and renders service-provided status rather than interpreting Docker directly. Docker inspection provides live observation, while a local installation identity and action record provide ownership and last intent. Windows and WSL never read one another's agent configs or state, even when they observe the same Docker Engine.

**Tech Stack:** Go 1.27.1, Bubble Tea v2.0.10, Lip Gloss v2.0.6, Docker CLI, standard Go testing. Use `/Users/user/.cache/aact-build-tools/go/bin/go` for local commands.

**Spec:** `docs/superpowers/specs/2026-10-02-aact-tui-interaction-design.md`, `docs/superpowers/specs/2026-10-02-aact-agent-adapters-design.md`, and the approved interaction mock in `.superpowers/brainstorm/6056-1790946803/content/aact-menu-interaction-v4.html`.

## Global Constraints

- Preserve the existing `feature/aact-mvp` branch and PR; create no additional branch or worktree for this effort.
- The user owns architecture. Save persists and immediately applies edits; there is no separate Apply. Windows and WSL have separate native agent configs and state. A foreign observed MCP can be registered with agents in the current environment, but its runtime cannot be stopped there.
- Do not use Computer Use, modify live agent configuration or running MCPs, or require host-wide Go/Python/Node dependencies. All tests use temporary state, fake agents, and fake Docker or isolated Docker fixtures.
- Every product change starts with a failing test, then a minimal implementation and green verification. The coordinator owns commits and PR updates during parallel work.
- The user chose to keep valid edited answers on failed Save/apply, show the concrete error, and leave achieved agent destinations distinct from requested ones. Environment TOML authoring/root precedence and whether a stopped container is retained remain unresolved. Do not silently choose those in the TUI.
- A foreign endpoint is useful only when it is reachable from the current environment; registration reports connection failures and never claims runtime ownership.

## Review Focus

1. Scrolling/resizing keeps selected stable IDs and pane scroll offsets; test both a list larger than the viewport and deletion of the selected row.
2. A Docker observation failure preserves configured profiles and shows a timestamp/error; test failure after a successful observation.
3. Same profile key on Windows and WSL does not imply same runtime owner; test distinct installation IDs against one Docker listing.
4. Foreign runtime registration changes only current-environment agent config/state; test no Docker Start/Stop and no foreign config writes.
5. A terminal without mouse or function keys still reaches every action through visible menu paths; test keyboard equivalents.

---

### Task 1: Independent runtime identity and observation

**Files:** `internal/state/store.go`, `internal/state/store_test.go`, `internal/mcp/docker.go`, `internal/mcp/docker_test.go`, `internal/mcp/ownership_test.go`.

**Interfaces:** `Store.InstallationID() (string,error)` returns a persistent random ID under this state root. `mcp.Instance` gains an ownership/status projection that distinguishes locally owned, other AACT, and unknown. `Runtime.List` combines Docker inspection with local runtime records without treating a matching capability key as proof of ownership.

- [ ] Write failing tests for persistent installation ID, two state roots seeing the same labeled container, legacy container ownership via exact local container ID, and Docker/current-state disagreement.
- [ ] Run `/Users/user/.cache/aact-build-tools/go/bin/go test ./internal/state ./internal/mcp -count=1`; capture the expected failures.
- [ ] Add an installation-owner Docker label and local last-successful-action record. Live Docker state remains observation, not a derived state-file status. Never expose foreign Start/Stop.
- [ ] Run focused tests, then the full `go test ./...`; record evidence in `docs/implementation-evidence.md`.

### Task 2: Two-pane home and navigation

**Files:** `internal/tui/model.go` (or focused new files in `internal/tui/`), `internal/tui/model_test.go`, `internal/tui/home_test.go`.

**Interfaces:** Keep `tui.Backend` compatible while introducing typed local `CapabilityRow` and `ProfileRow` view projections. `Model` stores separate selected stable IDs and scroll offsets for Capabilities and related MCP profiles, a focused pane, screen, and modal state.

- [ ] Write failing tests for two-pane rendering, selected-capability profile filtering by source+package, skill-only and no-profile empty states, Tab/left/right pane focus, up/down/page/home/end scrolling, and resize/refresh selection preservation.
- [ ] Run `/Users/user/.cache/aact-build-tools/go/bin/go test ./internal/tui -count=1`; confirm the intended red assertions.
- [ ] Implement the approved Norton Commander layout and keyboard navigation with visible F-key/menu affordances. Agents, Environments, Settings, and Help are destinations from Main menu, not Tab destinations.
- [ ] Add mouse hit regions for rows, pane focus, visible controls, and wheel scrolling; test click/scroll against resized and scrolled layouts. Keep keyboard use complete.
- [ ] Run focused and full Go tests; record evidence.

### Task 3: Typed profile and operation service boundary

**Files:** `internal/app/ui.go` and focused new files/tests in `internal/app/`; `internal/state/store.go` only after Task 1's interface is settled.

**Interfaces:** Add typed UI profile snapshots containing `state.Key`, source/capability identity, registration targets, local ownership, observed Docker status, observation time/error, and action availability with reasons. Add typed operation requests/results for profile actions; retain the old `UIRun` until the TUI has migrated.

- [ ] Write failing tests for configured never-started profiles, foreign running profile visibility, per-capability filtering identity, failed Docker refresh retaining configured profiles, and foreign `Configure registrations` without runtime mutation.
- [ ] Run focused tests and confirm red.
- [ ] Implement read projection from catalog, target configuration, answers/installation state, and Docker observation. Keep each fact separate; no inferred `Stopped` on inspection error.
- [ ] Implement registration-only service operation through agent adapters with per-agent results and ownership-safe removal. Do not invoke package installation or Docker for this operation.
- [ ] Run focused and full Go tests; record evidence.

### Task 4: Menus, forms, management screens, and operation progress

**Files:** `internal/tui/` and tests; `internal/forms/` only for reusable behavior proved by failing tests.

**Interfaces:** Use Task 3's typed snapshots/requests where available. Main menu opens Agents, Environments, Settings, Help; F2/Enter opens item Actions. A single scrollable install/parameter form displays all declared inputs, fixed/default provenance, conditional disabled fields, and destination controls. Save invokes one persist-and-apply operation.

- [ ] Write failing tests for F9/Main menu and F2/Actions navigation, menu focus/escape restoration, disabled action reasons, form up/down and Tab/Shift+Tab, single Save, selected-agent destinations, and mouse clicks.
- [ ] Run focused tests and confirm red.
- [ ] Implement menus and forms against typed service operations. Show foreground progress and per-target results. Do not expose unimplemented actions as if operational; show a reason.
- [ ] Implement Agents, Environments, Settings, and Help with read-only details and exact config view without masking. Add writes only where the service contract exists and architecture is approved.
- [ ] Run focused/full tests and record evidence.

### Task 5: Integration, review, and existing PR

**Files:** `tests/integration/`, TUI/application tests, docs, and the existing PR.

- [ ] Add integration tests proving navigation from capability to related profile to Configure registrations, a foreign runtime cannot be stopped, and a skill-only capability defaults to `All — ~/.agents/skills`.
- [ ] Run `go test ./...`, `go vet ./...`, platform cross-builds, and Docker fixture tests where isolated; report every failure precisely.
- [ ] Compare terminal views with the approved mock and review every requirement in the spec. Run an independent code review and resolve findings.
- [ ] Commit verified changes on `feature/aact-mvp`, push that branch, and update the existing PR. Do not merge or publish a release.
