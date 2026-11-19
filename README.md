# Another Agent Capability Toolkit

AACT installs agent skills and configures MCP servers for selected local agents.
A checkout can select a catalog and environment configuration; installed skills
remain global to the chosen agent. The native manager runs on macOS, Linux, and
Windows, on amd64 and arm64. WSL uses the Linux build and its own state.

The shipped catalog contains cluster-inspector, grafana-inspector,
azure-inspector, git-provider, find-session, non-interactive-ready-planning,
git-repo-map, and contribute-capabilities. Docker is required for containerized
MCPs and find-session.
The release includes native helpers: system Go, Python, Node.js, npm, jq and
Make are not needed to install, render, or manage these packages. Agent CLI
registration requires that agent's installed CLI for Codex and Copilot CLI.

## Install a published release

```sh
curl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v 0.7.5
```

On Windows PowerShell, use the PowerShell installer:

```powershell
$script = Join-Path $env:TEMP 'aact-install.ps1'
Invoke-WebRequest -Uri 'https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.ps1' -OutFile $script
& $script -Version 0.7.5
Remove-Item $script
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
aact version list
aact version upgrade
aact version install 0.7.0
aact catalog --json
aact agents
aact settings
```

The same manager commands work in PowerShell:

```powershell
aact
aact version
aact version list
aact version upgrade
aact version install 0.7.0
aact catalog --json
aact agents
aact settings
```

`aact version` and `aact --version` print the installed build version. Version
list, upgrade, and install query the GitHub Releases API for stable releases;
there is no offline release catalog. The TUI checks for a newer stable version
in the background and continues normally if that request fails. Explicit
version commands report a lookup error when GitHub is unavailable. Install and
upgrade verify the release asset SHA-256 digest, then use the managed installer
and its `checksums.txt` verification before activation.
Direct installer runs use the installed AACT lifecycle controller when
available, so an active daemon is stopped before replacement and restarted at
the same Vault API endpoint. Releases without a daemon leave the prior daemon
stopped. When an older installed controller cannot supervise an update, the
installer reports that a daemon-capable target remains stopped and needs a
manual restart.

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

# Use isolated agent locations and manager state:
aact install non-interactive-ready-planning --agent codex \
  --agent-home "$HOME/test-agent" --state-dir "$HOME/test-aact-state"
```

MCP adapters currently include Codex, Claude Code, Copilot CLI, OpenCode,
Copilot IntelliJ, and generic manual configuration. Generic manual configuration
produces a configuration artifact for the user to apply; it does not edit an
unrecognized agent's files. JetBrains AI Assistant's
installed configuration uses IDE XML, so its former JSON writer is disabled
until an XML adapter is verified. Select `--agent` more than
once to target several agents. `--agent-home ID=PATH` assigns separate homes;
a single shared path applies to all selected agents. Copilot IntelliJ requires
opening Copilot Chat and selecting **Add MCP Tools** to create its configuration
first. Generic MCP reports the path to its manual configuration artifact.
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

A Capability Pack contains an `aact.toml` catalog and, by default, an
`environments/` directory alongside it. Put per-environment configuration such
as `ota/` and `prod/` inside that directory. Target files are
`ENVIRONMENTS_DIRECTORY/ENVIRONMENT/PACKAGE/TARGET.toml`. A pack may point to an
environment directory outside the pack in `aact.toml`, or you can set a local
override with `aact config set-environment-directory PATH` or
`--environment-directory PATH`. The previous `set-environment-root` command and
`--environment-root` flag remain supported as aliases. The Settings screen labels these as the Capability Pack and Environment
directory. CLI input values override saved local edits,
which override editable target, project, and package defaults. A target TOML can
set `[aact.input_policy]` entries to `fixed` or `default`. A `fixed` input must
have a value in that target file, is omitted from setup and Parameters forms,
and cannot be overridden with `--set`. A `default` input remains editable.
Input policy is read only from target TOML. `--environment` and `--target` are
used together.

Pack authors can hide inputs or choices from interactive forms without
changing validation or saved values:

```toml
[packages.atlassian.inputs]
auth_method = "oauth"

[packages.atlassian.ui]
hide_inputs = ["internal_note"]
hide_choices = { auth_method = ["personal"] }
```

Hidden inputs and choices remain valid in existing saved values and explicit
CLI/API overrides. A form edit preserves hidden values; hiding is a display
preference, not an authorization or validation rule.

For target-backed checkbox inputs, a package can declare `type = "multichoice"`
and `options_from = "dbms.*.tenants.*"`. Each `*` reads table names from the
selected target TOML; the selected names are joined with `/` (for example,
`shared_postgres/plane`). An optional `label` in the leaf table supplies a
display name. These choices are selected in the form, not entered as free text.
Without a Capability Pack manifest, AACT exposes the bundled catalog.

Existing configuration can be previewed with `aact migrate --dry-run --json` and
adopted with `aact migrate --apply`. Dry-run does not create manager state.

Before upgrading from a build that created generic manual MCP registrations,
remove those registrations with that older build. See [Upgrade instructions](docs/upgrading.md).

State defaults to `$XDG_STATE_HOME/agent-skills` or
`~/.local/state/agent-skills` on Unix and `%LOCALAPPDATA%\aact\state` on native
Windows. Override with `--state-dir` or `AACT_STATE_DIR`. Installed releases and
manager state are separate: updating the launcher retains resources that existing
managed skill links still reference.

AACT has a daemon that hosts the KeePass-backed secret service. Use
`aact daemon status` and `aact daemon stop` to inspect or stop it. The older
`aact vault` commands remain available. AACT does not currently restart the
daemon automatically after it exits. The lifecycle without a usable OS
credential store is also not implemented; commands that need the vault may
fall back to an interactive master-password prompt, and noninteractive use
requires a configured password or a usable saved credential.

AACT normally asks for the vault master password interactively. To supply it
without a prompt, set either `AACT_VAULT_PASSWORD_FILE` to a readable file path
or `AACT_VAULT_PASSWORD` to the password. Set only one. AACT removes one final
newline from a password file and does not pass these variables to capability
subprocesses. The same password must be used to open an existing vault.

AACT can optionally use an OS credential store through a script hook. The hook
supports only `get` and `set` operations for the vault password; it does not
manage daemon lifecycle or recover a password the OS store cannot return. When
no password environment override is set, AACT tries a saved vault password on
each unlock; there is no 15-minute cooldown. A missing, unavailable, or
invalid saved credential falls back to the master-password prompt. Saving
remains opt-in:
after a successful manual unlock, the TUI offers the `Ctrl+S` toggle and
interactive CLI commands ask whether to save the password. Linux needs
`secret-tool` and an available Secret Service session for this option.

AACT does not currently apply a cooldown to repeated manual password attempts.
The planned password-attempt cooldown and keyring-independent daemon lifecycle
are not implemented.

The bundled Windows script uses the Windows Runtime Credential Locker through
the PowerShell 5.1 WinRT type projection supplied by Windows; it needs no
downloaded module or compiled helper. Windows Credential Locker supports up to
20 credentials per app. If WinRT access is unavailable, AACT uses the normal
password prompt. WSL invokes the same Windows PowerShell script and Credential
Locker for the current Windows user when C: is mounted at `/mnt/c`; it fails
closed if that trusted system path cannot be confirmed. Nonstandard drive or
mount layouts can use a custom absolute-path hook. The macOS shell connector uses
the built-in `security` and `plutil` commands. The Linux example uses Python 3
and `secret-tool` with an available Secret Service session. To use
another store, configure an absolute-path hook with
`AACT_VAULT_CREDENTIAL_HOOK`; custom `.ps1`, `.cmd`, `.bat`, and executable
hooks are supported. Hook secrets travel over stdin and stdout, not process
arguments or environment variables.

To use a custom connector, set `AACT_VAULT_CREDENTIAL_HOOK` to an absolute
script path. AACT sends a versioned JSON request to the script's standard input
and reads one JSON response from standard output. The request supports `get`
and `set`; secrets are carried only through these pipes, never as command-line
arguments or environment variables. The connector protocol and bundled scripts
are under `scripts/keyring/`.

## Log in to an MCP

MCPs can declare a Login action in their Capability Pack. In the TUI, open the
capability instance, select the relevant MCP profile, and choose **Login** in
its Runtime actions. Save valid inputs first. A host-side Login action can run
before the MCP starts; a container-side action requires that MCP to be
running. AACT waits for an authentication health check only when the pack
declares one. The login method and provider-specific steps belong to the
Capability Pack.

For an MCP that declares a browser login helper, start that MCP first, then
choose **Log in to …** under Runtime actions. AACT starts its dedicated browser
helper and returns the helper's viewer URL for accessing the login session.
This is separate from an MCP's declared Login command. No 15-minute login
cooldown is currently enforced.

## Build and verify from source

Development uses the Go version pinned in go.mod. Go is needed to develop or
build releases, rather than to run an installed release.

Tests in this project must check functionality through an observable public
boundary. Unit tests that assert implementation details—such as private methods,
internal state, call sequences, or literal source text—have no place here. Prefer
no test over one coupled to how the code is written. Keep useful integration
tests under `test/integration/`.

```sh
go -C test test ./...
go vet ./main/...
go -C test test -race ./...
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
go -C test test -tags integration ./integration -count=1
go -C test test -tags docker_integration ./integration -count=1
sh tools/test-containers.sh
```

CI runs native tests on macOS/Linux/Windows, race tests where supported, Docker
integration on Linux, and archive-content checks. Only the version-tag workflow
publishes releases. Creating a development archive does not publish or tag it.

Version-tag releases also build each bundled `packages/*/mcp` Docker context and
publish a version-pinned image to GHCR. New GHCR container packages start private;
the release workflow checks package visibility before publishing the GitHub
release and stops if any image is not public. For the first release, set each
new package to **Public** in its GitHub Packages settings, then rerun the
workflow. The release workflow also requires the repository Actions secrets
`DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` (a Docker Hub access token) so its
multi-architecture builds can authenticate before pulling QEMU and base images.
Image digest files are attached as workflow artifacts. The manager
uses release images by default when declared; choose **Settings → MCP images →
Build locally** or run `aact config set-image-source local` to build from the
bundled package context instead. Restore prebuilt images with
`aact config set-image-source release`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development, tests, agent adapters,
capability packages, and the pull request process.

AACT is MIT licensed. Bundled third-party notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Capability Packs

A Capability Pack supplies a catalog and read-only configuration profiles.
See [Creating and using Capability Packs](docs/capability-packs.md) and the
[offline example](examples/capability-pack/aact.toml). See also
[HCL expressions in TOML](docs/hcl-expressions.md). Select a capability to see
all its profiles, or create a local profile without changing pack files.
