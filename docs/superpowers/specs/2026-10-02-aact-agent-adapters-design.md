# Installation destinations and agent adapters

Status: design additions from the user; implementation pending the TUI design discussion.

## Installation destinations

- For a skill-only installation, show `All — ~/.agents/skills` as a selectable destination, checked by default.
- Also offer named agent destinations for their own supported skill directories.
- When the installation requires an agent MCP registration, omit `All` entirely. Require one or more named agents.
- Decide this from the components being installed: an optional MCP that is not selected does not prevent a skill-only installation.
- Show the resolved destination paths in the installation summary.
- Shared-directory discovery differs between agents. An adapter must describe which skill directories its agent actually reads; do not silently promise that every application reads the shared directory.

Example skill-only destination section:

```text
╔═ Install skill: git-repo-mapper ═══════════════════════════╗
║ Destinations                                             ║
║ [x] All          ~/.agents/skills                         ║
║ [ ] Claude Code  ~/.claude/skills                         ║
║ [ ] OpenCode     ~/.config/opencode/skills                ║
║                                                          ║
║                 [ Install ]  [ Cancel ]                  ║
╚══════════════════════════════════════════════════════════╝
```

Example destination section when MCP registration is required:

```text
╔═ Install capability: cluster-inspector ══════════════════╗
║ Register with agents                                     ║
║ [x] Codex                                                ║
║ [ ] OpenCode                                             ║
║ [ ] Claude Code                                          ║
║                                                          ║
║                  [ Install ]  [ Cancel ]                 ║
╚══════════════════════════════════════════════════════════╝
```

These illustrate the destination controls; the rest of the setup dialog still follows the agreed single-form design.

## Adapter responsibilities

Agent integration belongs in independently registered adapters, shared by CLI and TUI. The TUI displays their results and does not know agent config formats or discovery rules.

Each adapter must:

1. Detect the agent using executable/application/plugin evidence. A missing config does not mean an agent is absent. Report installation evidence separately from whether a license or authentication was verified.
2. Resolve config locations for the current OS and supported environment overrides. Report candidate files, scope, and precedence instead of guessing one filename.
3. Read current MCP registrations and plan a narrowly scoped add/remove operation.
4. Create the smallest valid config and required directories when none exists, if the agent supports this.
5. Preserve unrelated entries, fields, comments where the format supports them, and application settings.
6. Handle every applicable config file, including overlapping JSON/JSONC registrations, using the agent's verified precedence. Removal must not unexpectedly expose an older AACT-owned registration underneath a higher-priority one.
7. Distinguish user-created registrations from AACT-owned registrations and surface conflicts.

The user accepted temporary config copies for rollback and assigned that responsibility to the adapters. Each adapter owns capture and restoration of the config state it changes. The installation service can request rollback when a later operation fails, without knowing agent filenames or formats.

## Adapter extension mechanism

Decision: compiled Go adapters. Each agent integration implements a common Go interface and is explicitly registered with AACT. Adding or changing an adapter requires rebuilding AACT; contributors can submit a PR or maintain a fork. Native shared-library plugins and external executable adapter plugins are outside this selected design.

The adapter transaction interface discussed in chat remains a proposal; accepting compiled Go adapters does not approve every method signature or transaction lifecycle detail.

Treat distinct clients explicitly: Claude Code versus Claude Desktop, JetBrains AI Assistant versus Junie or Copilot in JetBrains, and Copilot CLI versus Copilot used as an OpenCode model provider. Shared backend/config may be reused only when verified.

## Agents action behavior agreed in discussion

- View configuration files opens a read-only, scrollable viewer of the exact file contents. Do not mask or redact values, including tokens.
- Refresh detection updates the selected agent's installation evidence and discovered locations in place; retain explicit location preferences. F5 triggers this directly from the agent list.
- Back closes the action menu and restores the selected agent row. Esc from the Agents view returns to the main two-pane screen.

## Separate adapter tests

Adapter tests run without launching the TUI and without modifying the user's real config. Use temporary homes/config roots, injected OS/path/process discovery, and sanitized fixtures from verified formats.

Required cases per applicable adapter:

- Installed executable, desktop-only installation, IDE plugin installation, and absent agent.
- macOS, Windows, Linux, and WSL discovery, including directory overrides.
- Missing directory and missing file; valid creation accepted by the agent where locally testable.
- Add, repeat add, remove, and repeat remove.
- Preserve unrelated MCP entries and unrelated settings.
- JSON only, JSONC only, both present, conflicting definitions, and comments/trailing commas.
- Malformed config, user-owned name conflict, and failed writes with no partial changes.

Before committing adapter implementation, attach evidence for each assumption. Label documented behavior, live CLI checks, fixture tests, and unverified environments separately.

See [current investigation](../../agent-adapter-verification.md). No adapter implementation is certified by this design document.
