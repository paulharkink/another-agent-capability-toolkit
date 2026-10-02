# Agent adapter investigation

Date: 2026-10-02. Read-only inspection of installed agents plus isolated temporary-config probes. No real agent config, credentials, registrations, or MCP instances were modified. No Computer Use was used. This is investigation evidence, not completion of adapter implementation.

## Locally verified

| Agent | Evidence | Result |
| --- | --- | --- |
| Codex CLI | `/opt/homebrew/bin/codex`, version `0.153.4` | `mcp add --url` creates a missing config file in an existing `CODEX_HOME`; add/remove preserves an unrelated MCP entry, a model setting, and a TOML comment. |
| Claude Code CLI | `~/.local/bin/claude`, version `2.1.217` | Explicit user scope creates `~/.claude.json` when absent. With `CLAUDE_CONFIG_DIR` set, the file is instead `<CLAUDE_CONFIG_DIR>/.claude.json`. Removal preserves an unrelated MCP entry and an unrelated object. |
| OpenCode CLI | `/opt/homebrew/bin/opencode`, version `1.18.30` | `debug config` accepts JSON-only, JSONC-only, and no existing config. Both files merge; JSONC wins conflicting MCP values. With neither present, this installed version creates `opencode.jsonc` on config loading. Probe MCPs were disabled. |
| Claude Desktop | Installed `/Applications/Claude.app`, version `2.110.1`; its separate desktop config exists | Presence/config location inspected. Desktop reload and MCP functionality not exercised. |
| OpenCode Desktop | Installed `/Applications/OpenCode.app`, version `1.18.31` | Presence verified; Desktop-specific behavior not exercised. |
| JetBrains | IntelliJ IDEA `2026.2.0.1` installed; 2026.2 settings contain `options/llm.mcpServers.xml` with `McpApplicationServerCommands`, `commands`, and `urls` | Existing generic JSON adapter assumption is wrong for the installed AI Assistant settings. Real XML was inspected for structure only; no server values printed and no write performed. |

Additional existing config paths inspected: `~/.codex/config.toml`, `~/.config/opencode/opencode.json`, `~/.claude.json`, and `~/Library/Application Support/Claude/claude_desktop_config.json`. Their contents were not emitted. `~/.ai/mcp/mcp.json` and the current assumed Copilot configs were absent.

The first Codex probe established that an explicitly configured `CODEX_HOME` must already exist. After creating that temporary directory, the missing-file probe passed. The adapter must handle directory creation, not just missing files.

## Official references checked

- [OpenCode config](https://opencode.ai/docs/config): JSON/JSONC, merging, scope precedence, custom config overrides.
- [OpenCode skills](https://opencode.ai/docs/skills): discovery includes the global shared directory as well as OpenCode and Claude-compatible directories.
- [OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli): TOML MCP configuration and shared configuration across listed OpenAI clients. Local CLI behavior was checked separately.
- [Claude Code MCP](https://code.claude.com/docs/en/mcp): explicit transport and scope; user versus project/local registration.
- [Claude Code skills](https://code.claude.com/docs/en/skills): personal skills documented under `~/.claude/skills`. Shared-directory support has not been established for this installed Claude version.
- [JetBrains AI Assistant MCP](https://www.jetbrains.com/help/ai-assistant/mcp.html): UI accepts JSON snippets and offers global/project scope. This does not establish a standalone JSON config file; local settings use XML.
- [GitHub Copilot MCP in IDEs](https://docs.github.com/en/copilot/how-tos/copilot-in-your-ide/customize-copilot/extend-copilot-with-tools-and-context/extend-copilot-chat-with-mcp?tool=jetbrains): JetBrains plugin uses an MCP configuration file. Exact OS paths, absent-file bootstrap behavior, and installed plugin behavior still need direct verification.

## Current code assumptions needing correction or verification

- Existing adapter interface only exposes register/unregister; detection and config discovery are not adapter responsibilities yet.
- Claude Code now has a scoped user-config JSON adapter. A temporary `CLAUDE_CONFIG_DIR` CLI probe confirmed the `mcpServers` entry shape with `type = http` and URL. Claude Desktop remains separate and unsupported.
- OpenCode now writes to the effective JSONC file when present and avoids creating a JSON sibling. It validates both files and retires matching AACT-owned shadow entries on update/removal so an older lower-priority value cannot reappear. This has fixture coverage; live OpenCode reload after a write was not exercised.
- JetBrains AI Assistant's previously assumed JSON writer has been disabled: the installed IDE stores its MCP settings in XML. Its XML writer/detection are not implemented, so AACT must report it as unavailable rather than claim a successful registration.
- Missing-config handling must distinguish agent presence from config presence.
- Copilot-specific discovery, config overrides, and native Windows paths are not certified by the existing path constants.

## Not yet live verified

- Codex Desktop config loading/reload was not independently exercised.
- Nora: `ssh` with batch mode and strict host-key checking could not resolve hostname `nora` from this environment. No remote commands ran.
- No work-machine connection was available in this investigation; Copilot in Windows/WSL remains unverified live.
- No Copilot CLI was found in local PATH. Provider access in OpenCode does not establish a separately installed Copilot client.
- JetBrains XML serialization/editing, external-agent config scopes, Junie, and Copilot plugin behavior still need isolated fixtures and application/source evidence. No license was acquired or activated.
- macOS observations must not be presented as Windows/Linux/WSL live verification.
