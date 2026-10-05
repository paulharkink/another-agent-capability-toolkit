# Another Agent Capability Toolkit

AACT installs agent skills and configures MCP servers for selected local agents.
A checkout can select a catalog and environment configuration; installed skills
remain global to the chosen agent. The native manager runs on macOS, Linux, and
Windows, on amd64 and arm64. WSL uses the Linux build and its own state.

The shipped catalog contains cluster-inspector, grafana-inspector,
azure-inspector, forgejo, find-session, non-interactive-ready-planning, and
git-repo-map. Docker is required for containerized MCPs and find-session.
The release includes native helpers: system Go, Python, Node.js, npm, jq and
Make are not needed to install, render, or manage these packages. Agent CLI
registration requires that agent's installed CLI for Codex and Copilot CLI.

## Install a published release

```sh
curl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v 0.1.3
```

## Use the manager

The [interactive TUI design mock](docs/demo/index.html) shows the intended
Mac layout and navigation using illustrative data. It is a static, browser-only
simulation: clicking Install or Save does not change local agent configuration,
credentials, files, or containers. The self-contained `docs/demo/` directory
can be served by GitHub Pages when the design is ready to publish.

```sh
aact                         # editable terminal menus
aact version
aact catalog --json
aact agents
aact settings
```

The terminal home has a Capabilities pane and a related MCP profiles pane.
Use Tab or the arrow keys to change panes, Up/Down to select, Enter for item
Actions, and `m` for the Main menu. Mouse clicks and scrolling work in
terminals that report mouse events. An MCP started by another AACT installation
can be observed and registered with agents here without claiming its runtime.
The Environments menu browses target TOML from the current checkout and opens
setup with the chosen target; Settings can choose default named MCP agents for
future installs. Profile Actions can check a connection and view recent logs
with Follow/Pause (`f`); operation results remain scrollable until dismissed.

CLI subcommands are noninteractive by default. Supply declared inputs with
repeated `--set name=value` options, or use `--interactive` for prefilled forms.
Repeated values for a collection add items; scalar inputs accept one value.
In terminal forms, collections use Enter/a to add, e to edit, r to remove,
and brackets to select a row; path pickers select one path at a time. Install
and unregister forms allow multiple agents. Catalog a/s actions authenticate or
start an MCP before it appears in runtime inventory; they ask for its context.
Inputs sharing `exclusive_group` in `package.toml` cannot both be set. Entering
one in a form clears the other; noninteractive commands reject conflicting
values. Cluster Inspector uses this for Token and Source kubeconfig.

```sh
aact install git-repo-map --agent codex \
  --set scan_roots="$HOME/projects" --set scan_roots="$HOME/worktrees"
aact uninstall git-repo-map --agent codex
aact install non-interactive-ready-planning --agent all

# Register an existing MCP URL without starting a container:
aact install forgejo --agent opencode --external-url http://localhost:8765/mcp \
  --set base_url=https://git.example.com

# Use isolated agent locations and manager state:
aact install non-interactive-ready-planning --agent codex \
  --agent-home "$HOME/test-agent" --state-dir "$HOME/test-aact-state"
```

MCP adapters currently include Codex, Claude Code, Copilot CLI, OpenCode,
Copilot IntelliJ, and generic manual configuration. JetBrains AI Assistant's
installed configuration uses IDE XML, so its former JSON writer is disabled
until an XML adapter is verified. Select `--agent` more than
once to target several agents. `--agent-home ID=PATH` assigns separate homes;
a single shared path applies to all selected agents. Copilot IntelliJ requires
opening Copilot Chat and selecting **Add MCP Tools** to create its configuration
first. Generic MCP produces a manual configuration artifact and reports its path.
The skill-only `all` destination installs under `~/.agents/skills`; it cannot
be used for an MCP registration.

```sh
aact mcp list --json
aact mcp status
aact mcp prepare cluster-inspector --environment personal --target laptop
aact mcp authenticate cluster-inspector --environment personal --target laptop --interactive
aact mcp start cluster-inspector --environment personal --target laptop
aact mcp logs cluster-inspector --environment personal --target laptop
aact mcp stop cluster-inspector --environment personal --target laptop
```

Authentication is an explicit action; noninteractive installation does not open
a live login. Foreign skills, foreign registrations, and edited managed copies
are preserved. Several targets can share identical companion skill content;
different rendered content at the same global skill name reports a conflict.
MCP instance inventory is global across configured sources. If a copied checkout
or a new bundled release changes an owned plain skill's source location, repeat
installation with `--update-source` to explicitly repoint its link; the existing
source remains selected until that update succeeds.

## Select a checkout catalog

Place `aact.toml` in the checkout, or select it with `--config PATH`:

```toml
schema_version = 1
source_id = "my-project"

[environments]
root = "./environments"

[[catalog]]
id = "git-repo-map"
source = "bundled:git-repo-map"

[[catalog]]
id = "my-skill"
source = "./skills/my-skill"

[packages.git-repo-map.inputs]
scan_roots = ["./repositories"]
```

A local catalog entry needs a `package.toml` or a plain `SKILL.md`; the entry ID
must match its package ID. Relative paths resolve against the file that declares
them. Target files are `ENVIRONMENT_ROOT/ENVIRONMENT/PACKAGE/TARGET.toml`.
Set a default environment root with `aact config set-environment-root PATH`, or
supply `--environment-root PATH`. CLI input values override saved local edits,
which override editable target, project, and package defaults. A target TOML can
set `[aact.input_policy]` entries to `fixed` or `default`. A `fixed` input must
have a value in that target file, is omitted from setup and Parameters forms,
and cannot be overridden with `--set`. A `default` input remains editable.
Input policy is read only from target TOML. `--environment` and `--target` are
used together.
For target-backed checkbox inputs, a package can declare `type = "multichoice"`
and `options_from = "dbms.*.tenants.*"`. Each `*` reads table names from the
selected target TOML; the selected names are joined with `/` (for example,
`shared_postgres/plane`). An optional `label` in the leaf table supplies a
display name. These choices are selected in the form, not entered as free text.
Without a checkout manifest, AACT exposes the bundled catalog.

Existing configuration can be previewed with `aact migrate --dry-run --json` and
adopted with `aact migrate --apply`. Dry-run does not create manager state.

State defaults to `$XDG_STATE_HOME/agent-skills` or
`~/.local/state/agent-skills` on Unix and `%LOCALAPPDATA%\aact\state` on native
Windows. Override with `--state-dir` or `AACT_STATE_DIR`. Installed releases and
manager state are separate: updating the launcher retains resources that existing
managed skill links still reference.

## Build and verify from source

Development uses the Go version pinned in go.mod. Go is needed to develop or
build releases, rather than to run an installed release.

```sh
go test ./...
go test -race ./...
goreleaser release --snapshot --clean
```

GoReleaser builds all six platform/architecture combinations with
`CGO_ENABLED=0`, packages the manager, helpers, package resources, and license
notices, and generates `checksums.txt`. Use `--snapshot` to build without
publishing. Unpack an archive and run its `bin/aact`; keep the `packages`
directory next to `bin`.
`AACT_BUNDLED_ROOT` can override the package directory during development.

Docker verification uses synthetic fixtures, no live service credentials:

```sh
go test -tags integration ./tests/integration -run TestDockerMCP -count=1
go test -tags docker_integration ./internal/mcp ./internal/packagehelpers ./internal/sessionsearch ./tests/packages -count=1
sh tools/test-containers.sh
```

CI runs native tests on macOS/Linux/Windows, race tests where supported, Docker
integration on Linux, and archive-content checks. Only the version-tag workflow
publishes releases. Creating a development archive does not publish or tag it.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development, tests, agent adapters,
capability packages, and the pull request process.

AACT is MIT licensed. Bundled third-party notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
