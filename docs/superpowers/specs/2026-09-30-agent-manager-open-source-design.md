# Agent Manager: public tool, reusable packages, private configuration

Date: 2026-09-30
Status: written layout and TDD approach approved in conversation; implementation plan awaiting review.

For design approval, start with the
[concrete repository layouts and file examples](2026-09-30-agent-manager-layout-design.md).
This document supplies the supporting architecture and behavior.

## 1. Purpose and scope

Publish a portable Agent Manager and selected skills/MCP integrations in a new
public `agent-manager` repository under the MIT license. The manager installs and
configures content **globally for the user's configured agents and agent
environments**. Launching inside a checkout selects content and configuration;
it does not restrict installation to that checkout.

A consuming repository can contain private skills, private MCP packages,
configuration for public packages, or any combination. A company can keep all
of these together in one private repository. A separate environment-only
repository remains supported.

Users can install from the terminal without opening the TUI. The TUI offers a
persistent menu and editable forms for the same operations. Ordinary skill
directories and MCP resources remain usable through manual linking, copying,
or configuration without adopting Agent Manager.

### MVP content

| Package | Public content |
| --- | --- |
| Cluster Inspector | MCP integration/server, companion skill, existing Kubernetes authentication, optional Tekton and database inspection |
| Grafana Inspector | MCP integration/server, companion skill, existing supported authentication modes |
| Azure Inspector | MCP integration/container build, companion skill, device-code authentication |
| Forgejo | Integration for the existing upstream MCP container, companion skill |
| find-session | Skill and portable launcher for its Docker-based session search |
| non-interactive-ready-planning | Plain skill |
| git-repo-map | Generalized version of the generated mapper in `ozon-devtools` |

The current `agent-skills` repository becomes a consuming content repository.
Only the selected content moves to the public project. Company names, hostnames,
cluster details, credentials and personal configurations belong in consuming
repositories or local state, rather than public defaults.

## 2. Runtime and distribution

- Go executable, with a Go TUI framework from the Bubble Tea ecosystem.
- Shared application operations behind both CLI and TUI.
- Versioned release archives containing the executable, public packages,
  templates and any platform-specific helpers those packages need.
- Shell and PowerShell bootstrap installers download a selected version and
  verify its checksum. Installer and archive versions are pinned together.
- No system-wide Python, Go, Node.js, npm, jq or Make dependency for the manager
  or shipped package installation/lifecycle operations.
- Docker is required for containerized MCPs and Docker-based package tooling.
  Plain skills and template-only skills can be installed without starting Docker.
- An installed agent's own CLI may be used by its registration adapter where
  appropriate. That is an agent prerequisite, not a general scripting runtime.

Release targets are macOS and Linux on amd64/arm64 and native Windows on
amd64/arm64. Windows PowerShell and MinGW use the Windows executable; WSL uses
the Linux executable and Linux paths. Build and package compatibility across
these targets must be established before advertising a release as supported.

Make may remain a development convenience. It is not the installation,
configuration or lifecycle mechanism exposed to consumers.

The release installer stores versioned resources in a stable per-user location
and provides an executable launcher on PATH. A release update does not delete
an older resource directory while managed installations still reference it.
Uninstalling the executable does not silently uninstall registered packages.

## 3. Repository and catalog model

### Public project

```text
agent-manager/
  cmd/agent-manager/
  internal/                 # operations, forms, state, source and agent adapters
  packages/
    cluster-inspector/
      package.toml
      SKILL.md.mustache
      mcp/
    git-repo-map/
      package.toml
      SKILL.md.mustache
      generators/
    non-interactive-ready-planning/
      package.toml
      SKILL.md
  install.sh
  install.ps1
```

A package may supply a skill, an MCP, or both. A bundle exposes its components
explicitly; the manager does not infer a companion skill solely from a matching
directory name. Package IDs are distinct from agent-facing skill/registration
names, so collisions can be diagnosed before installation.

Source readers translate their format into a normalized catalog of packages,
components, inputs, resources and actions. Installation and UI code consume
that catalog, rather than reaching into TOML files directly. MVP readers cover
our TOML packages, local ordinary skill directories, and the bundled public
catalog. Additional marketplace readers can be added at this boundary later.

### Example private consuming repository

```text
company-agents/
  README.md
  agent-manager.toml
  skills/company-deployment/
    SKILL.md
  packages/company-service/
    package.toml
    SKILL.md
    mcp/
  environments/company/cluster-inspector/production.toml
  environments/company/grafana-inspector/production.toml
  environments/company/git-repo-map/default.toml
```

Illustrative consumer configuration:

```toml
schema_version = 1

[[catalog]]
id = "cluster-inspector"
source = "bundled:cluster-inspector"

[[catalog]]
id = "grafana-inspector"
source = "bundled:grafana-inspector"

[[catalog]]
id = "git-repo-map"
source = "bundled:git-repo-map"

[[catalog]]
id = "company-deployment"
source = "./skills/company-deployment"

[[catalog]]
id = "company-service"
source = "./packages/company-service"

[environments]
root = "./environments"
```

This selects which bundled public packages appear alongside private content.
The consumer does not need to clone the public source repository, embed a
submodule, or maintain a Make dependency. Its README can simply explain how to
install a compatible versioned release. A standalone environment repository
can select only public packages with the same configuration format.

Discover `agent-manager.toml` from the current directory upwards to the checkout
root. Explicit `--config` takes precedence. Without a consumer configuration,
the bundled public catalog is available. Paths declared by a TOML file resolve
relative to that file unless absolute; `~` expands to the user's home. CLI path
arguments resolve relative to the caller's current directory.

## 4. Public input definitions and private values

Public package manifests define questions, types, validation and defaults.
Consuming TOMLs supply values; they do not have to duplicate the question schema.

An illustrative template-only or generated skill manifest:

```toml
schema_version = 1
id = "git-repo-map"
name = "Git repository map"

[skill]
name = "git-repo-map"

[[inputs]]
name = "scan_roots"
type = "directory"
multiple = true
min_items = 1
label = "Repository locations"
required = true

[[templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"
```

Private values in `environments/company/git-repo-map/default.toml`:

```toml
[inputs]
scan_roots = ["~/sources/company", "~/sources/personal"]
```

Environment and target selection remain explicit. Existing
`<environment>/<mcp>/<target>.toml` discovery and existing tables such as `[mcp]`,
`[cluster]`, `[grafana]`, `[tekton]` and `[dbms.*]` remain supported. Public input
definitions can specify a `config_key`, such as `cluster.api_server`, mapping
those existing table values into a named form input. New skill parameters can
use the `[inputs]` table directly. If a file defines both mappings for one input,
reject the ambiguous value rather than silently choosing one.

Resolution order, lowest to highest priority:

1. Public package defaults.
2. Previously saved local answers for this source/package/environment/target.
3. Consumer defaults, followed by its selected environment/target TOML.
4. Explicit CLI arguments.
5. Edits made in an interactive form for this operation.

Consumer-wide defaults use `[packages.<package-id>.inputs]` in
`agent-manager.toml`. Repository configuration consequently overrides older
local answers. The form shows the effective values. A successful installation
persists its local answers without rewriting the consuming repository.
Required fields and cross-field constraints are validated after resolution.

All package inputs retain their JSON types: booleans, numbers and collections
are not flattened to strings. Undeclared input names and invalid manifest
versions produce useful diagnostics, including the source file and field.

## 5. CLI and TUI behavior

Bare `agent-manager` opens the TUI. The main navigation contains:

- **Catalog:** browse skill/MCP/bundle packages, inspect inputs and dependencies,
  select components and destination agents, install or update.
- **MCPs:** global inventory of managed instances, source/environment labels,
  running status, authentication, parameters, start/stop and logs.
- **Agents:** detected and explicitly configured agent environments,
  registrations and skill locations.
- **Settings:** source/configuration selection and remembered manager preferences.

Views are independently reachable. Completing an action returns to the same
view with its outcome visible. Selecting skills does not force a transition to
the MCP screen.

Representative CLI operations:

```text
agent-manager catalog list
agent-manager install cluster-inspector --environment company --target production --agent codex
agent-manager install git-repo-map --environment company --target default --agent codex --interactive
agent-manager mcp list
agent-manager mcp start cluster-inspector --environment company --target production
agent-manager uninstall cluster-inspector --environment company --target production --agent codex
```

CLI subcommands are noninteractive by default. Complete configuration installs
without the TUI, as the old Make commands did. Missing required answers or a
missing agent selection fail with actionable guidance and a suggestion to use
`--interactive`; commands do not unexpectedly prompt in a pipeline.

`--interactive` opens the same forms used by the TUI, already filled from the
resolution rules above. Without configuration, these forms ask for the required
package inputs. Agent selection may use an explicitly saved selection; missing
selection is never interpreted as permission to install into every detected agent.

Authentication that requires human interaction is explicit. Interactive Azure
device-code login attaches/streams the container's authentication flow. A
noninteractive operation reuses valid authentication or reports the separate
authentication step required. External MCP URL registration remains available
without starting a container.

## 6. Forms, paths and collections

The shared form engine supports string, secret, boolean, integer, number,
choice, multiple-choice, file and directory inputs, with required/default
values and relevant bounds or allowed values.

File and directory inputs offer real native dialogs where available, plus a
terminal browser fallback. Windows dialogs and macOS system dialog facilities
must not introduce additional required dependencies. Linux graphical dialog
helpers are optional; headless systems remain fully usable through the terminal.
Manual path entry is always available. Paths returned by a picker must be valid
for the running executable's OS, including WSL and MinGW boundaries.

For `multiple = true`, the manager presents an Add/Edit/Remove collection.
Each picker invocation adds **one** path. The manager validates, normalizes and
deduplicates the collection, honoring platform case sensitivity. Minimum and
maximum counts apply to the collection. Native multiple-selection dialogs are
not needed for the MVP.

Secrets are masked in forms and omitted from routine diagnostics. Credentials
are stored in the package's local authentication state with appropriate file
permissions. A consuming repository can supply references or values as supported
by the package, but installing never writes credentials back into repository
TOMLs.

## 7. Mustache and optional generators

Each packaged skill may have zero or one logical generator declaration and any
number of Mustache templates or ordinary supporting files. OS-specific command
variants are alternatives for that generator, not additional processing stages.

The rendering context always includes `inputs`, containing the resolved form
answers. This directly supports `{{inputs.team_name}}` and list sections without
a generator. Optional computed results are available under `generated`.

Mustache sections handle lists and booleans. Publishers use raw triple-brace
interpolation deliberately where HTML escaping is inappropriate for Markdown;
values intended for Markdown tables must be escaped for that syntax by the
publisher. The manager validates required inputs before rendering because
Mustache itself treats missing values as empty.

### Generator launch and protocol

Publishers can use shell, PowerShell, compiled Go or another executable. The
manifest describes an argument vector, selected OS variants, dependencies and,
optionally, a container command. The manager does not choose a mandatory
embedded scripting language. Commands run from the package directory with
resolved executable/resource paths and an isolated staging directory.

```toml
[generator]
command = ["sh", "generate.sh"]

[generator.windows]
command = ["powershell.exe", "-NoProfile", "-File", "generate.ps1"]
```

That is a publisher example, not a required runtime for shipped public packages.
Those packages use bundled native helpers or containers to uphold the runtime
requirements in section 2. Missing third-party runtimes are diagnosed before
installation, with the package's declared prerequisite shown.

The generator receives one JSON request on stdin:

```json
{
  "protocol_version": 1,
  "inputs": {"scan_roots": ["/home/user/work", "/home/user/personal"]},
  "context": {
    "package_dir": "/path/to/package",
    "staging_dir": "/path/to/staging",
    "environment": "company",
    "target": "default"
  }
}
```

It writes exactly one JSON object to stdout, for example:

```json
{"repositories": [{"host": "git.example.com", "name": "team/service", "path": "/home/user/work/service"}]}
```

That object becomes `generated`; it cannot overwrite `inputs` or manager context.
Stderr is for progress and diagnostics. A nonzero exit, cancellation or malformed
response fails generation and preserves the previous installation. Diagnostics
do not dump stdin requests containing secrets. The contract computes template
data; package resources and declared templates define the installed files.

Template destinations are relative to the generated skill directory and cannot
escape it. Rendering and ordinary resource copying happen in staging, followed
by publication of a complete skill directory. Template source files and package
control manifests need not appear in the installed skill.

### Public Git repository mapper

Port the mapper from
`ozon-devtools/agent-skills/kadaster-repo-map` into a general public package.
Its scan roots are an editable directory collection. Remote/host filters are
configuration, not embedded Kadaster assumptions. A bundled native generator
reads Git metadata, supports normal repositories and worktrees, and emits
structured repository rows for Mustache.

Preserve host identity, normalized repository coordinates and multiple local
checkouts of the same remote. Do not silently discard duplicate checkouts.
Prune common dependency/cache directories and surface inaccessible scan roots.
The generated instructions explain that an absent checkout does not imply an
absent remote repository. The mapping logic belongs to this package, not to the
manager's form or rendering engine.

## 8. Installation, agent adapters and ownership

Ordinary skills link to their source directory. Generated skills link to a
stable managed output directory in local state. On native Windows, use a
directory symlink where available, a suitable junction for directories where
possible, and a managed copy fallback otherwise. Record the installation mode
so updates and uninstall work correctly. Copied skills require an explicit
update to reflect source changes.

Retain the existing agent integrations: Codex, OpenCode, Copilot CLI, Copilot
IntelliJ, IntelliJ and generic MCP configuration. Detect conventional locations
and respect existing explicit agent-home overrides. Allow users to configure
additional named environments for the same agent. Agent adapters preserve
unrelated registrations, JSONC comments and other settings.

An ownership ledger records source identity, package/component, selected
environment/target, destinations, generated resources, registration names and
installed release identity. Separate sources claiming the same destination
produce a conflict, not a silent overwrite. Removal only touches resources
owned by the selected installation. Shared companion skills remain while another
managed registration needs them; unrelated files remain untouched.

Package installation can register an existing external MCP URL, or configure
and manage a local Docker instance. Instances are keyed by source, package,
environment and target. The MCP overview aggregates managed instances across
source checkouts, while Catalog reflects the active source selection.

Operations prepare and validate changes before publishing them. Generation is
atomic per skill. Agent configuration writes use safe replacement. A failure
across several agents reports each result and records successful changes so
retry/uninstall is reliable; the manager does not claim a global transaction
across independent files and external processes. Concurrent manager invocations
must coordinate state and destination writes through local locks.

## 9. MCP behavior retained during extraction

Package-specific authentication and server behavior stay behind package/action
interfaces. The Go core must not grow direct imports of a particular inspector's
authentication implementation, as the current Python manager has done.

- Cluster Inspector retains explicit target Kubernetes access, supported
  token/kubeconfig and certificate settings, optional Tekton configuration and
  database/tenant configuration. It does not fall back to an unrelated default
  host kubeconfig.
- Grafana retains token/session and supported Vault/auth refresh behavior.
- Azure keeps its Azure CLI and server dependencies inside Docker, with
  target-specific persisted authentication and tenant/subscription selection.
- Forgejo keeps its pinned upstream image integration and token configuration.
  Upstream software keeps its original attribution and license.
- Existing ports, transports, loopback exposure, target configuration and
  external URL registration remain configurable without inventing new server
  features during this rewrite.

Container discovery reconciles recorded state with Docker's actual state.
Stopped, missing and failed instances appear distinctly. Authentication and
start/stop errors retain package-specific diagnostics and offer a retry from
the current view. Docker is checked only for operations that require it.

## 10. Persistent state and migration

Runtime state stays outside checkout directories.

| Environment | Default state root |
| --- | --- |
| macOS/Linux/other normal Unix environments | `$XDG_STATE_HOME/agent-skills`, otherwise `~/.local/state/agent-skills` |
| WSL | Same Linux/XDG rule, within the WSL user environment |
| Native Windows, including PowerShell/MinGW | `%LOCALAPPDATA%\agent-manager\state` |

The existing Unix state directory name is retained for compatibility. A
manager-specific explicit state-root override is supported on every platform.
Local answers, ownership, generated skills, logs and target-specific auth state
live under that root. Settings currently in
`$XDG_CONFIG_HOME/agent-skills/agent-manager.json` (normally
`~/.config/agent-skills/agent-manager.json`) are imported into manager state on
first use, preserving the old file. Native Windows state roots are independent
of a separate WSL installation.

Migration recognizes the existing config-root pointer, target layouts,
authentication stores, generated-skill markers and links owned by this repo or
the legacy ozon tooling. Inspect and adopt only positively identified owned
resources. Preserve existing authentication data rather than forcing everyone
to authenticate again. Moving a package to the public release must provide an
explicit owned-link update, not break installations by removing old paths first.

Saved local answers and authentication are scoped to source/package/environment/
target. Agent destinations and settings are user-global. A consumer may declare
an explicit `source_id`; otherwise use its normalized Git origin and configuration
path within the repository. This identity survives checkout moves. Sources with
neither receive a local identity and require explicit reassociation after a move.
Copied checkouts sharing an identity must not silently repoint existing links;
the manager reports the changed source location and offers an explicit update.
The implementation plan will detail serialization and migration mechanics
without changing these ownership guarantees.

## 11. Acceptance criteria

Implementation is acceptable when:

1. A clean supported host installs a release and opens the manager without
   system-wide scripting/development runtimes. Docker-based packages work with
   Docker and their declared agent prerequisites.
2. One private checkout exposes company skills alongside selected public
   inspectors and environment values, with no public source checkout required.
3. Public questions appear when configuration is absent; existing environment
   values prefill editable forms; a fully configured CLI installation is
   noninteractive.
4. A template-only skill renders directly from typed inputs. A generated skill
   additionally receives computed JSON. Failure leaves its previous files intact.
5. Directory collections add one selection at a time and behave consistently
   with manual entry and terminal fallback.
6. Selected agents receive global installations. Unrelated configuration and
   resources survive install/update/uninstall and conflicts are reported.
7. Existing inspector capabilities, authentication and external URL installation
   survive extraction. The MCP menu reflects actual running instances.
8. Existing target TOMLs and owned installations can migrate while retaining
   credentials and state outside the repository.
9. The public Git mapper handles several roots, different hosts, worktrees and
   multiple checkouts without company-specific assumptions.
10. CLI and TUI call the same operations; menu navigation remains independent.

The implementation plan must cover manifest validation, resolution precedence,
generator protocol, rendering, ownership, migration and agent adapter behavior,
plus platform checks for paths, dialogs and installation fallbacks. Container
and authentication integration checks should use isolated fixtures/targets.
Implementation follows TDD: write and observe the intended failing test before
implementing each behavior, make it pass, then refactor with tests green. Ported
behavior receives preservation tests before its replacement implementation.
The [layout specification](2026-09-30-agent-manager-layout-design.md#6-tdd-and-test-ownership)
defines colocated Go tests, integration tests, package tests, fixtures and the
development-only test dependencies. Every implementation-plan task must include
its failing-test-first step and subsequent verification.
No product implementation or runtime testing is part of this design change.

## 12. Deferred work and references

Future source readers may browse/install OpenAI/Codex, Claude, Hermes or other
skill/plugin catalogs. The normalized catalog boundary keeps this possible.
Their marketplace clients, manifest adapters and browser/installer experiences
are outside the MVP. No embedded language runtime or arbitrary marketplace
dependency solver is needed for the initial version.

Public project licensing is MIT. Retain attribution and notices for extracted
and third-party material; distributing an integration does not relicense its
upstream container or agent software.

Design references:

- [Go executable builds](https://go.dev/doc/tutorial/compile-install)
- [Go cross-compilation for Windows](https://go.dev/wiki/WindowsCrossCompiling)
- [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- [Mustache rendering rules](https://mustache.github.io/mustache.5.html)
- [Native dialog backend options](https://github.com/ncruces/zenity)
- [Windows symbolic-link requirements](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createsymboliclinkw)
- [MIT license](https://opensource.org/license/mit)
