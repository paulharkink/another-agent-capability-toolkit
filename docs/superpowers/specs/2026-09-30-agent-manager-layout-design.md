# Agent Manager: repository layouts and file examples

Date: 2026-09-30
Status: layout and TDD approach approved in conversation; implementation plan awaiting review.

This is the concrete design to review. The companion architecture document
records behavior and constraints. These trees describe proposed repositories;
this documentation change does not create them or implement their contents.

## 1. Public `agent-manager` repository

```text
agent-manager/
├── README.md
├── LICENSE                              # MIT
├── go.mod
├── cmd/
│   └── agent-manager/main.go             # CLI/TUI entry point
├── internal/
│   ├── catalog/
│   │   ├── manifest.go
│   │   ├── manifest_test.go              # Go tests live beside their code
│   │   └── testdata/                     # valid/invalid manifests
│   ├── forms/
│   │   ├── resolve.go
│   │   ├── resolve_test.go
│   │   └── collection_test.go            # single picker additions, validation
│   ├── render/
│   │   ├── render.go
│   │   ├── render_test.go
│   │   ├── generator_test.go
│   │   └── testdata/                     # templates and generator responses
│   ├── install/                         # implementation + *_test.go
│   ├── agents/                          # adapter code, *_test.go, JSON/JSONC fixtures
│   ├── mcp/                             # runtime code + *_test.go
│   ├── state/                           # state/migration code + *_test.go
│   ├── cli/                             # commands + *_test.go
│   └── tui/                             # models/navigation + *_test.go
├── tests/
│   ├── integration/
│   │   ├── checkout_install_test.go      # CLI through global install/uninstall
│   │   ├── generator_failure_test.go     # previous output survives failure
│   │   ├── migration_test.go
│   │   ├── docker_mcp_test.go            # opt-in real Docker integration
│   │   └── testdata/
│   │       ├── company-agent-skills/     # consuming repo fixture
│   │       └── agent-homes/              # isolated agent configs
│   └── packages/
│       ├── skill_contract_test.go        # shipped manifests/templates/resources
│       └── testdata/                     # expected rendered skill files
├── install.sh                           # install a versioned release
├── install.ps1
├── packages/
│   ├── cluster-inspector/               # skill + MCP bundle
│   │   ├── package.toml                 # definitions/questions, no private values
│   │   ├── SKILL.md.mustache
│   │   ├── scripts/
│   │   │   ├── git-context.sh
│   │   │   └── git-context.ps1
│   │   └── mcp/
│   │       ├── Dockerfile
│   │       ├── requirements.txt         # dependencies installed inside Docker
│   │       ├── server.py
│   │       ├── config.py
│   │       ├── tekton_tools.py
│   │       └── tests/
│   │           ├── test_cluster_inspector.py
│   │           └── test_tekton_tools.py
│   ├── grafana-inspector/               # package.toml, skill and Docker resources
│   ├── azure-inspector/                 # Azure CLI runs inside Docker
│   ├── forgejo/                         # package.toml, skill, upstream image config
│   ├── find-session/                    # package.toml, skill and Docker search tool
│   ├── non-interactive-ready-planning/
│   │   ├── package.toml
│   │   └── SKILL.md                     # ordinary skill, no rendering
│   └── git-repo-map/
│       ├── package.toml
│       ├── SKILL.md.mustache
│       ├── generators/
│       │   └── repo-map/
│       │       ├── main.go              # source of the optional native generator
│       │       ├── main_test.go
│       │       └── testdata/            # remotes, worktrees, duplicate checkouts
│       └── examples/
│           ├── request.json
│           └── response.json
└── docs/
    ├── package-format.md
    └── generator-protocol.md
```

`packages/` is the bundled public catalog. The manager reads its manifests
through the same catalog model used for local packages; inspector-specific
authentication belongs to package actions, rather than hard-coded TUI screens.
Supporting shared server/authentication resources are also shipped in the build
contexts that need them.

### Plain skill: `packages/non-interactive-ready-planning/package.toml`

```toml
schema_version = 1
id = "non-interactive-ready-planning"
name = "Non-interactive ready planning"

[skill]
name = "non-interactive-ready-planning"
```

Its existing `SKILL.md` is the skill itself. No questions or generator are
needed. A directory containing only `SKILL.md` also works as a local catalog
entry without requiring a wrapper manifest.

### Bundle: `packages/cluster-inspector/package.toml`

The following excerpt shows component declarations and two questions. The
package also declares the existing authentication, Tekton and database options
described in the architecture spec; they are not removed by this example.

```toml
schema_version = 1
id = "cluster-inspector"
name = "Cluster Inspector"

[skill]
name = "cluster-inspector"
files = ["scripts"]

[[templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"

[[inputs]]
name = "api_server"
config_key = "cluster.api_server"
type = "string"
label = "Kubernetes API URL"
required = true

[[inputs]]
name = "local_port"
config_key = "mcp.local_port"
type = "integer"
label = "Local MCP port"
default = 8765
min = 1024
max = 65535

[mcp]
name = "cluster-inspector"
runtime = "docker"
build_context = "./mcp"
transport = "streamable-http"
container_port = 8765
endpoint_path = "/mcp"
host_port_input = "local_port"
```

`[skill]` and `[mcp]` declare components independently. A package can contain
either or both. `files` copies the listed supporting files/directories into
generated skill output; templates provide the remaining generated files.
For a plain skill, its source directory is linked directly. The Docker fields
describe the runtime; additional package actions perform authentication and
prepare target configuration using the package's existing behavior.

### `packages/cluster-inspector/SKILL.md.mustache`

```markdown
---
name: cluster-inspector
description: Inspect the configured Kubernetes cluster through its MCP.
---

# Cluster Inspector

Configured API: {{{inputs.api_server}}}

Use the registered Cluster Inspector MCP for cluster inspection.
Follow this skill's existing inspection and mutation rules.
```

This excerpt demonstrates parameter substitution. The published skill retains
its complete current guidance. Form answers alone render this template;
there is no skill generator just to pass an API URL into Mustache.
Default inspector companion guidance stays target-neutral so multiple registered
targets share one skill. The excerpt is a substitution example; custom rendered
skills with different content cannot silently replace each other at one name.

### Generated skill: `packages/git-repo-map/package.toml`

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

[generator]
command = ["./bin/repo-map"]

[generator.windows]
command = ["./bin/repo-map.exe"]
```

The release build compiles `generators/repo-map/main.go` and adds the matching
native executable under this package's `bin/`. These commands therefore refer
to release resources, not checked-in binaries or a required host Go install.
Other publishers can instead declare `sh`, `powershell.exe`, another installed
interpreter or a container. The manager interprets argv, not a shell expression.

### `packages/git-repo-map/examples/request.json`

```json
{
  "protocol_version": 1,
  "inputs": {"scan_roots": ["/home/alex/work", "/home/alex/personal"]},
  "context": {
    "package_dir": "/path/to/git-repo-map",
    "staging_dir": "/path/to/staging",
    "environment": "company",
    "target": "default"
  }
}
```

The manager supplies this structure on stdin. The generator performs the scan;
it does not display questions or implement pickers.

### `packages/git-repo-map/examples/response.json`

```json
{
  "repositories": [
    {"host": "git.example.com", "name": "platform/service", "path": "/home/alex/work/service"},
    {"host": "git.example.com", "name": "platform/service", "path": "/home/alex/work/service-fix"}
  ]
}
```

The generator writes this object to stdout. The manager places it under
`generated` in the Mustache context. Stderr carries progress/errors. The rows
above retain two checkouts of the same remote.

### `packages/git-repo-map/SKILL.md.mustache`

```markdown
---
name: git-repo-map
description: Resolve repository references to local checkouts.
---

# Local repositories

{{#generated.repositories}}
- {{{host}}}/{{{name}}}: {{{path}}}
{{/generated.repositories}}

An absent checkout does not mean the remote repository does not exist.
```

## 2. A private skill/configuration repository

```text
company-agent-skills/
├── README.md                            # release installation instructions
├── agent-manager.toml                   # select public and local catalog entries
├── skills/
│   ├── company-deployment/              # ordinary skill; manually linkable
│   │   └── SKILL.md
│   └── company-release/                 # templated skill; no generator needed
│       ├── package.toml
│       ├── SKILL.md.mustache
│       └── reference.md                 # ordinary supporting file
├── packages/
│   └── company-service/                 # optional private MCP or skill+MCP bundle
│       ├── package.toml
│       ├── SKILL.md
│       └── mcp/
│           ├── Dockerfile
│           ├── go.mod
│           ├── server.go
│           ├── server_test.go          # private MCP behavior tests
│           └── testdata/
├── environments/
│   └── company/
│       ├── cluster-inspector/
│       │   └── production.toml
│       ├── grafana-inspector/
│       │   └── production.toml
│       ├── git-repo-map/
│       │   └── default.toml
│       └── company-release/
│           └── default.toml
└── tests/
    ├── testdata/                        # sanitized sample target/input values
    └── expected/
        └── company-release/SKILL.md     # expected rendered result
```

The folders `skills/` and `packages/` are an organizational convention, not
required loader paths. Every local catalog entry names its directory explicitly.
An environment-only repo can omit them both. A content-only repo can omit
`environments/` and use forms or CLI values.

### `agent-manager.toml`

```toml
schema_version = 1
source_id = "company-agent-skills"

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
id = "company-release"
source = "./skills/company-release"

[[catalog]]
id = "company-service"
source = "./packages/company-service"

[environments]
root = "./environments"

[packages.company-release.inputs]
team = "Platform"
```

`bundled:` resolves into the installed versioned public release. Local entries
resolve relative to this file. The private repo contains neither a manager
checkout nor a copy of public packages.

### `skills/company-deployment/SKILL.md`

```markdown
---
name: company-deployment
description: Follow the company's deployment process.
---

# Deployment

Consult the deployment repository's AGENTS.md before changing a deployment.
Use the configured inspection MCPs to examine the current environment.
```

### `skills/company-release/package.toml`

```toml
schema_version = 1
id = "company-release"
name = "Company release process"

[skill]
name = "company-release"
files = ["reference.md"]

[[inputs]]
name = "team"
type = "string"
label = "Team name"
required = true

[[inputs]]
name = "release_repo"
type = "directory"
label = "Release repository checkout"
required = true

[[templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"
```

### `skills/company-release/SKILL.md.mustache`

```markdown
---
name: company-release
description: Follow the configured company release process.
---

# Release process for {{inputs.team}}

Read AGENTS.md in {{{inputs.release_repo}}} before preparing a release.
Consult reference.md for the company's release checklist.
```

`reference.md` contains ordinary company guidance and is copied unchanged.
The template gets values directly from the shared form engine: no custom code.

### `environments/company/company-release/default.toml`

```toml
[inputs]
release_repo = "~/sources/company/releases"
```

The `team` value comes from repo-wide defaults in `agent-manager.toml`;
`release_repo` comes from this target. Both appear as editable form answers.

### `environments/company/cluster-inspector/production.toml`

```toml
[mcp]
local_port = 8765

[cluster]
api_server = "https://kubernetes.company.example"
```

Existing target authentication/certificate/Tekton/database tables remain valid.
The manifest's `config_key` maps these existing values to form inputs, so this
repo does not repeat their type, labels or question definitions.

### `environments/company/grafana-inspector/production.toml`

```toml
[mcp]
local_port = 8081

[grafana]
url = "https://grafana.company.example"
auth_mode = "api_token"
```

The token is requested/stored in local authentication state. It is not written
back into this TOML by installing the package.

### `environments/company/git-repo-map/default.toml`

```toml
[inputs]
scan_roots = ["~/sources/company", "~/sources/personal"]
```

The form displays two rows with Add/Edit/Remove. Each Add opens one directory
picker. The resulting array reaches the generator without string conversion.

### `packages/company-service/package.toml`

For an MCP-only package, omit `[skill]` and the `SKILL.md` shown in the tree.
For a bundle, declare both:

```toml
schema_version = 1
id = "company-service"
name = "Company service inspector"

[skill]
name = "company-service"

[mcp]
name = "company-service"
runtime = "docker"
build_context = "./mcp"
transport = "streamable-http"
container_port = 8080
endpoint_path = "/mcp"
host_port_input = "local_port"

[[inputs]]
name = "local_port"
type = "integer"
label = "Local MCP port"
default = 8877
min = 1024
max = 65535
```

## 3. How these files are used

From the private checkout:

```text
agent-manager
agent-manager install company-release --environment company --target default --agent codex
agent-manager install company-release --environment company --target default --agent codex --interactive
agent-manager install cluster-inspector --environment company --target production --agent codex
```

The first command opens the main menu. The second installs with resolved values
without opening the TUI. The third shows those values in an editable form. The
fourth uses the public package definition with the private target configuration.
Missing required answers or authentication in a noninteractive command produce
an actionable error; interactive installation asks for them.

All destinations are global for the selected agent environment. Running inside
the company checkout selects its catalog/configuration; it does not create
project-local agent installations.

## 4. Files created locally, outside either repository

Logical state organization:

```text
~/.local/state/agent-skills/
├── manager/                             # settings, source registry, ownership ledger
├── answers/                             # saved typed input values
├── generated/                           # stable rendered skill directories
├── logs/
└── <mcp>/<environment>/<target>/         # existing auth/state locations retained
```

Manager-owned data includes source identity and selected agent environment in
its records. Legacy MCP state paths are adopted only when unambiguous; new
instances with conflicting source identities receive separate state directories.
The serialized filenames/key encoding are implementation details, not part of
the consumer TOML contract.

Unix respects `$XDG_STATE_HOME`, defaulting to the path above. Native Windows
uses `%LOCALAPPDATA%\agent-manager\state`; WSL follows the Linux rule.
Generated output is linked/copied into the agent's skill directory; ordinary
skills link directly to source. A cancelled form or failed generator leaves the
previous installation intact. Installing never writes runtime state into either
source checkout.

## 5. Installed release resources

Example Unix installation (Windows uses native per-user equivalents):

```text
~/.local/bin/agent-manager                # launcher for selected release
~/.local/share/agent-manager/releases/<version>/
├── agent-manager                        # executable; .exe on Windows
└── packages/
    ├── cluster-inspector/               # manifests, templates, container resources
    └── git-repo-map/
        ├── package.toml
        ├── SKILL.md.mustache
        └── bin/repo-map                 # platform binary; repo-map.exe on Windows
```

Other public packages appear alongside these examples. A release update keeps
old resources while owned installations still reference them. Local skills stay
in their consuming checkout; generated outputs and credentials stay in state.

## 6. TDD and test ownership

The implementation uses **test-driven development throughout**. For each new
behavior or regression fix:

1. Write a test that expresses the behavior.
2. Run it and observe the intended failure before production implementation.
3. Implement the minimum needed to pass.
4. Run the test and relevant suite; refactor while keeping them green.

The implementation plan must name the first failing tests and their expected
failure for each behavior. Tests are not deferred until a feature is finished.
Existing functionality being ported receives preservation tests before its
replacement implementation. Existing package tests are retained and extended.

### What belongs where

| Location | Tests and fixtures |
| --- | --- |
| `internal/<area>/*_test.go` | Manifest validation, value precedence, typed answers, collection editing, rendering, process protocol, ownership, agent config preservation, state migrations, CLI/TUI behavior |
| `internal/<area>/testdata/` | Small inputs and expected results local to that Go package |
| `tests/integration/` | Real CLI operations against temporary source/state/agent directories; cancellation, retries, generation failures, conflicting destinations and migration |
| `tests/packages/` | Public package manifests, plain skills, rendered skills, supporting resources and declared component contracts |
| `packages/<package>/mcp/tests/` | Existing Python MCP/server tests, executed inside their development/test containers |
| `packages/git-repo-map/generators/repo-map/` | Native generator tests beside the Go implementation; isolated Git metadata fixtures |
| Private repo `tests/` | Optional private inputs and expected skill outputs for its CI or the manager's integration harness |
| Private MCP's source directory | Its own implementation tests in its chosen language |

Consuming repo `tests/` does not introduce another package manifest format or
require a runtime test framework to use the repo. The public integration harness
contains a representative private-repo fixture; an actual private repo can use
the same expected-output approach in its CI by invoking the manager's CLI.

Example private golden output, `tests/expected/company-release/SKILL.md`, after
rendering with sanitized fixture answers `team = "Platform"` and
`release_repo = "/fixture/releases"`:

```markdown
---
name: company-release
description: Follow the configured company release process.
---

# Release process for Platform

Read AGENTS.md in /fixture/releases before preparing a release.
Consult reference.md for the company's release checklist.
```

Tests use actual rendering, file operations and serialization against isolated
directories. Fake only external boundaries where required, such as native
dialogs, running agents and Docker. Real Docker lifecycle checks are a separate
explicit integration suite; ordinary Go tests run without Docker or credentials.
Test subprocess fixtures cover malformed JSON, stderr, nonzero exits and
cancellation. TUI model tests cover navigation, prefilled edits and one-at-a-time
picker additions without requiring an interactive terminal.

The baseline Go suite is `go test ./...`; real Docker tests are opt-in with an
integration build tag. CI runs platform-independent and native path/installation
tests on macOS, Linux and Windows, plus container tests where supported. Native
dialog usability also receives a manual platform check. All test installs use
temporary state and agent homes, never the developer's actual registrations or
credentials.

Go/Python test toolchains are development or container dependencies, not runtime
dependencies of the installed manager. This revision defines the test design;
it adds no executable tests or product code yet.

## Related architecture

The [architecture specification](2026-09-30-agent-manager-open-source-design.md)
records input precedence, platform support, migration, ownership, existing MCP
behavior, licensing and deferred marketplace adapters. This layout document is
the primary artifact for the requested design approval.
