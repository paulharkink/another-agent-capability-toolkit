# Creating and using Capability Packs

A Capability Pack is a catalog and profile configuration for its intended
setting. AACT selects **Pack → Capability → Profile**. There is no additional
Environment or Target selector. The `environments` directory stores profiles;
its historical name remains a storage convention.

## A pack you maintain

```text
company-pack/
├── aact.toml                         # catalog, aliases, imported definitions
├── bootstrap.sh / bootstrap.ps1      # explicitly run by the maintainer/user
├── environments/
│   └── company-guidance/             # the catalog author's chosen ID
│       ├── ota.toml
│       └── prod.toml
├── capabilities/                     # optional company-private definitions
│   └── private-guidance/
│       ├── package.toml
│       └── SKILL.md
└── vendor/
    └── neutral-tools/                # pinned local Git submodule or copy
        ├── aact.toml
        └── guidance/
            ├── package.toml
            └── skills/{guide,review}/SKILL.md.mustache
```

The [offline example](../examples/capability-pack/aact.toml) includes the small
vendor catalog as ordinary files so it can load without network access. In a
real pack you can replace that directory with a pinned Git submodule:

```sh
git submodule add https://github.com/YOUR-ORG/neutral-tools.git vendor/neutral-tools
git -C vendor/neutral-tools checkout COMMIT_SHA
git add .gitmodules vendor/neutral-tools
```

Commit the gitlink alongside your pack. The provided bootstrap scripts run
`git submodule update --init --recursive`, honor that commit, preserve paths
with spaces, and return Git's errors. **AACT never runs bootstrap, clones a
repository, or fetches a URL.** Maintainers review and update dependency pins.

## Catalogs and imports

```toml
schema_version = 1
pack_id = "company"

[[catalog]]
id = "planning"
source = "bundled:non-interactive-ready-planning"

[[catalog]]
id = "private-guidance"
source = "./capabilities/private-guidance"

[[imports]]
catalog = "./vendor/neutral-tools/aact.toml"
include = ["guidance"]
[imports.overrides.guidance]
id = "company-guidance"
[[imports.overrides.guidance.sets]]
name = "reference"
skills = ["guide"]
mcps = ["reference-api"]
```

Omit `include` to import every entry. Overrides choose the catalog ID and linked
sets. IDs must be unique after imports; AACT reports both declarations on a
collision. This ID also names the profile directory. Imported definitions keep
their own resource roots and original manifest ID. Their company profiles,
pack defaults and local state do **not** get imported. Bootstrap a third-party
repository locally and point `catalog` or `source` at its local path.

The default profile directory is `<pack>/environments`. You may use an external
location through `[environments] root = "../company-profiles"` in `aact.toml`,
`--profile-directory PATH`, or `aact config set-profile-directory PATH`. Relative
manifest paths resolve from their owning TOML file. Relative file/directory
input values in profiles resolve from that profile's TOML. Explicit CLI input
paths resolve from the caller's current directory.

## Profiles are input values

`environments/company-guidance/ota.toml`:

```toml
[inputs]
project = "example-project"
enabled = false
[aact.input_policy]
project = "fixed"
enabled = "default"
```

A profile fills the capability's declared inputs. `fixed` locks a value and
wins over saved/form/CLI overrides. `default` fills an editable value. Fixed
policy is allowed only in the profile file. Precedence is capability default,
pack default, profile, saved editable override, then explicit input. Restoring
an inherited value removes its saved override; it does not modify the profile.
Boolean `false` is a supplied value, including for a required input. The TUI
uses a keyboard/mouse checkbox, with the same lock/reset policy as text fields.

AACT treats pack/catalog/profile TOMLs as read-only. Save and apply records
editable answers and selection intent in the existing user state store;
installation records describe achieved effects separately. **Create another
profile** creates local state, with no new file in the pack. Promote a useful
profile to your pack TOML yourself if colleagues should receive it.

## Several skills and linked MCPs

A `package.toml` can declare `[[skills]]` with `name`, definition-relative
`source`, `files`, `[[skills.templates]]`, and optional `[skills.generator]`.
The legacy single `[skill]`, package templates and generator remain readable;
do not mix single and repeated declarations for the same component kind.

```toml
[[skills]]
name = "guide"
source = "./skills/guide"
[[skills.templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"

[[skills]]
name = "review"
source = "./skills/review"
[[skills.templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"

[[inputs]]
name = "project"
type = "string"
required = true
regex = '^[a-z][a-z0-9-]*$'
[[inputs]]
name = "enabled"
type = "boolean"
required = true
default = false
```

Every stage must produce `SKILL.md`. Templates receive `inputs` directly; no
script is needed for simple substitution or Boolean Mustache sections. A
per-skill generator may be any declared executable or script, with an optional
Windows command variant. It receives protocol v1 JSON on stdin with `inputs`
and `context.profile`, `package_dir`, `staging_dir`. It returns one JSON object
used as `generated` by Mustache. Stderr/exit failures are shown with their
actual diagnostics. Publishers supply their own runtimes or standalone builds;
AACT does not silently replace missing commands with embedded behavior.

Catalog `sets` link named skills and MCPs as one selectable item. Sets cannot
overlap. Independent skills can be unchecked; **Select all skills** rechecks
skill-containing items without enabling plugins or changing MCP enable inputs.
The legacy one-skill/MCP capability remains linked automatically. MCPs may use
`enabled_input` to refer to a declared Boolean. Local Docker lifecycle is
interpreted generically from each `[mcp]`/`[[mcps]]` definition and declared
`prepare`/`authenticate` commands. `credential_files` describes files relative
to managed auth state for presence reporting; presence is not proof of login.

## Optional native plugins and destinations

```toml
[[plugins]]
name = "guidance-plugin"
format = "claude-code"
source = "./native-plugin"
# Optional: enabled_input = "use_native_plugin" (declared Boolean)
```

The artifact must contain its native manifest (for Claude Code,
`.claude-plugin/plugin.json`). Claude's compiled adapter installs it through
Claude's native CLI and a local marketplace. Unsupported formats/destinations
are unavailable; AACT does not substitute a skill for a plugin. **All** is the
generic `.agents/skills` adapter and supports only skills. Agents' own compiled
adapters handle detection, config paths, skills, MCP registrations and optional
plugins. Docker startup and skill rendering remain generic AACT services.

## Non-interactive use

```sh
aact install company-guidance --config /path/to/company-pack/aact.toml \
  --profile ota --agent codex

aact install company-guidance --config /path/to/company-pack/aact.toml \
  --profile ota --agent all --skills-only --item skill:review

aact profile create company-guidance experiment --config /path/to/company-pack/aact.toml

aact mcp start company-guidance --profile ota --mcp reference-api
```

One existing profile is inferred when `--profile` is omitted; multiple profiles
produce an error listing their names. `--profile` selects configuration and
`--mcp` selects one server. `--item` is repeatable; omission uses saved/default
selection. For a new no-prefill CLI installation, an explicitly accepted local
`default` profile is created. `--set enabled=false` stays typed as false. Regex
validation applies to interactive, non-interactive, generated picker values,
and every element of a collection. Use anchors when a whole-value match is
required. Invalid values identify the input before commands or adapters run.

An apply failure keeps saved edits and earlier successful effects. It does not
roll them back or show unfinished agent bindings as installed. The TUI reports
exact failed steps, child stderr, live runtime ownership and stale observations.
Local state keeps its existing platform root and formats; TOMLs remain untouched.
