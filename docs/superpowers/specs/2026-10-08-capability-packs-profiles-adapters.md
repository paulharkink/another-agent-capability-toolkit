# Capability Packs, Profiles, Components, and Agent Adapters

Status: implementation authorized on 2026-10-08. The TOML and Go interfaces below remain the approved implementation contract. The original executing-plans direction was superseded by the user’s later requirement that all implementation and repository/terminal tool work run through Luna subagents, with the primary controller coordinating only.

This records the full discussion after the user selected the primary checkout's `feature/restructure-concepts` branch. It supersedes environment/target selection and the narrow MCP-only adapter contract in the older AACT designs. Existing input forms, navigation, rendering, ownership protections, and local-state storage remain the foundation.

## Binding constraints

- Use `/Users/pharkink/sources/another-agent-capability-toolkit` on the user-created `feature/restructure-concepts` branch. Do not create or reuse another branch or worktree.
- Preserve unrelated changes, including the existing untracked UX proposal images.
- The user now requires all implementation and repository/terminal tool execution through Luna subagents; use subagent-driven-development with TDD and independent review gates. The primary controller coordinates only; do not spawn nested subagents. Use Luna for implementation and final review unless the user authorizes escalation.
- Capability behavior comes from manifests, declared commands/scripts, and templates. AACT contains generic interpretation, rendering, execution, coordination, and state tracking.
- Agent detection, locations, config inspection, skill installation/removal, MCP registration changes, and supported native plugin installation/removal belong to agent adapters.
- Pack profile TOMLs are read-only inputs. Keep the current local-state root, JSON files, hashed identifiers, locks, ownership records, and credentials; do not replace them with mirrored TOMLs.
- User-facing and operation-request selection is Capability Pack → Capability → Profile. Environment and Target are not additional selection levels.
- Tests about AACT belong in AACT. Use TDD and isolated fixtures; do not change live user agent configs, credentials, existing MCP instances, or other repositories.
- Preserve keyboard-first Norton Commander interaction, mouse convenience, fixed palettes per layer, visible scrolling, inline editable inputs/paste, unsaved-change handling, progress, explicit failures, and restored terminal modes.
- Plan execution does not authorize a push, PR creation, merge, version tag, release, deployment, or live migration. Those actions need a fresh, specific user instruction.

## 1. Capability Pack and imported catalogs

A Capability Pack provides the intended environment and the company's or user's profiles. Its `aact.toml` composes capability definitions from local directories, bundled capabilities, and external catalog manifests already present on disk. The home screen presents one combined capability list.

The external catalog's relative references resolve against the file that declares them. Importing its catalog does not import its environment profiles, saved answers, machine-specific defaults, or local installation state. Capability manifest defaults and catalog component relationships are part of the reusable definition; profile values come from the importing pack.

AACT does not download URL-based capabilities or initialize Git repositories. Pack maintainers can vendor public repositories, preferably with pinned Git submodules, and run their own setup scripts. AACT will supply shell and PowerShell starting points and documentation. Initialization is explicit and remains outside AACT's runtime.

The catalog author's configured capability name determines its directory below the profile root. It defaults to the capability's manifest `id`. AACT respects explicit names; it does not derive or impose an origin namespace. Two same-named definitions are allowed when the author assigns different configured names. An unresolved duplicate is a clear configuration error naming both declarations.

Proposed additive catalog syntax:

```toml
schema_version = 1
pack_id = "company-pack"

[environments]
root = "./environments"

[[imports]]
catalog = "./vendor/community-tools/aact.toml"
include = ["handoff-kit", "repository-tools"]

[imports.overrides.handoff-kit]
id = "company-handoff"

[[catalog]]
source = "bundled:git-repo-map"
# id is optional; an explicit id chooses the configured name/profile directory.

[[catalog]]
id = "local-guidance"
source = "./capabilities/guidance"
```

Omitting `include` imports all entries. Imports retain declaration order, support local nested imports with cycle detection, and do not introduce another UI selection. An override can assign a configured name and replace that entry's component sets. The existing `source_id` spelling remains a read-compatible alias for `pack_id`; conflicting simultaneous values fail. Local and `bundled:` sources remain compatible. Catalog source spellings are configuration fields, not user-facing labels.

## 2. Profiles and existing state

The default layout is `<pack>/environments/<configured-capability-name>/<profile>.toml`. The profile root can be configured outside the pack, as today. There is no intermediate environment directory to select.

Each profile supplies values for the capability's declared inputs and the existing `[aact.input_policy]` values `default` or `fixed`. Keep existing direct-input, `[inputs]`, and `config_key` mapping compatibility. Data referenced by declared choice sources or passed to the capability's command remains capability-owned data; AACT gives it no domain-specific meaning. File input values resolve relative to their declaring profile TOML, not the vendor catalog or the process's working directory.

Input precedence remains manifest defaults → pack defaults → profile prefills → saved editable overrides → supplied CLI/form edits. A profile's fixed values override editable layers and cannot be replaced by saved, CLI, or form values. Resetting a saved editable override restores the effective inherited value. Fixed policy belongs in the profile file.

Selecting a capability immediately lists its declared profiles together with profiles previously created locally. `Create another profile` replaces `Set up another target`; it asks for a name and all declared inputs without requiring a prefilled profile TOML. Creating/saving a local profile uses the existing profile/answer state records and does not write a pack file. There is no synthetic default-profile row before a local profile exists. Preserve the existing non-interactive skill-only invocation without a prefilled file; it can use the existing local default identity.

The public operation identity is `ProfileRef{PackID, CapabilityID, Name}`. Legacy environment/target fields can remain inside a storage compatibility bridge so existing hashes, answer files, auth directories, runtime ownership, and installations are reusable. Core workflows do not select an environment or target. Ambiguous old records are reported explicitly rather than guessed or rewritten. Additive record metadata for multiple skills/plugins is acceptable; wholesale state relocation, serialization changes, or automatic live migration is excluded.

## 3. Multiple skills, MCPs, and selectable items

A capability can declare several skills, several MCPs, and optional native plugin artifacts. Normalize the legacy `[skill]` and `[mcp]` forms alongside the new lists. Each skill has a component name and its own source directory, resources, optional templates, and optional generator; every skill uses the capability's declared input context. Retain the existing single-skill manifest's generator/template behavior.

The catalog can declare component sets tying named skills and MCPs together. A linked skill/MCP pair or explicit set is one selectable item and installs/removes together. Unlinked skills are individually selectable. All skill items start selected; `Select all` checks the eligible items, after which the user can uncheck individual skills or linked items. Selecting all skills does not invent credential values or silently change an MCP's declared `enabled_input`.

Proposed syntax for a reusable capability and a catalog relationship:

```toml
# package.toml
[[skills]]
name = "review-guidance"
source = "./skills/review-guidance"

[[skills]]
name = "release-notes"
source = "./skills/release-notes"

[[mcps]]
name = "repository-api"
# Existing generic launch/action configuration follows here.

# In that capability's [[catalog]] entry:
[[catalog.sets]]
name = "repository-access"
skills = ["review-guidance"]
mcps = ["repository-api"]
```

`release-notes` remains an independent item; `review-guidance` and `repository-api` travel together. Reject unknown component references and overlapping explicit sets rather than invent a general dependency graph. Preserve the existing single-skill/MCP capability as a linked unit when it has no explicit set declarations. Multiple enabled MCPs sharing that skill install the skill once. Public catalogs may ship these relationships; an importing pack may override them. An omitted selection uses defaults or the saved selection; an explicitly empty selection means deselect everything and must not be mistaken for the default. Selected intent and achieved installation status are distinct.

Skill output paths are scoped within the existing generated-state directory so two skills cannot overwrite one another. Installation observations and records distinguish component names and destinations while retaining old single-skill records. Existing ownership checks and shared-resource reference counting still apply.

One capability Apply coordinates its selected components for its selected agents. A failed step reports the exact error and achieved effects; successful component effects remain truthfully recorded. Preserve the agreed no-auto-revert behavior. A checkbox or aggregate status cannot claim an unachieved effect. A native plugin is an explicitly declared native artifact, not an automatic conversion of unrelated skills and MCPs.

## 4. Complete agent adapter boundary

Adapters remain compiled Go implementations. Every adapter provides identity, supported features, detection, and read-only inspection. Optional feature interfaces provide skill install/remove, MCP add/update/remove, and supported native plugin install/remove. Feature claims and implemented interfaces must agree.

Adapters own OS-specific paths, environment-variable overrides, config creation rules, JSON/JSONC precedence, CLI-versus-file registration behavior, agent installation evidence, and inspection of actual installed components. Generic app, CLI, and TUI code consume adapter results and do not branch on agent IDs, enumerate agent-specific files, or infer installation paths.

The shared `~/.agents/skills` destination is a generic agent adapter, labeled `All` for skill-only installations. Its features are skills only; MCPs and plugins are unavailable. Preserve the old `all` selection as a compatibility alias. The old manual `generic-mcp` writer does not turn this generic adapter into an MCP manager; old manual records may remain readable as historical observations.

An installed agent without a config file is distinct from an absent agent. The adapter reports whether it can create the missing file. Undetected agents and unsupported operations have a clear reason and cannot be selected as if supported. Native plugins are selectable only for adapters advertising the matching format. Skills/MCPs still install as one capability operation through the applicable feature interfaces.

Rendering manifests/templates and executing capability generators remain generic AACT work. Adapters install the staged results for their agents. Docker runtime build/start/stop/observation remains in the generic runtime manager; adapters manage the agent's MCP registration.

Initial native plugin proposal: implement the verified Claude Code local plugin format and user-scope CLI installation first. Other adapters advertise plugin support only after their native contract has been verified and implemented. Accept an explicitly supplied native plugin directory; do not invent a plugin converter or implement marketplace browsing in this work. Native plugin items are separate from the skill/MCP set relationships and require explicit selection or a declared true `enabled_input`; Select all skills does not implicitly add a native plugin. Claude plugin operations must use an isolated or explicitly selected agent context and the adapter's supported native CLI, preserving unrelated plugins and marketplace declarations. References: [native manifest](https://code.claude.com/docs/en/plugins-reference), [local marketplace](https://code.claude.com/docs/en/plugin-marketplaces). Local read-only CLI help for `claude plugin install`, `list`, `marketplace add/remove`, and `uninstall` was checked while planning; no plugin was installed.

## 5. Validation and generic capability execution

Support declared `type = "boolean"` inputs with a visible checkbox (`[x]` / `[ ]`) in the TUI. Space or Enter toggles the focused editable checkbox; a mouse click is optional convenience. Store and pass a typed Boolean, not a string. Both `true` and `false` are supplied values, so a required Boolean set to `false` is valid. Apply the same default/fixed/reset rules as other inputs: an editable default can be toggled, a saved override can be reset, and a fixed checkbox displays its value and lock reason without accepting changes. Non-interactive `--set enabled=false` has the same typed semantics. Retain existing Boolean parsing/toggling where it already works; add the missing checkbox presentation and regressions.

Add optional `regex` to an input definition. Compile it at manifest loading; malformed patterns identify the manifest and input. Use Go regular-expression semantics without adding implicit anchors. Validate a normalized scalar's text; validate each collection element independently. Optional empty values retain current optional-value behavior; requiredness remains its own rule. Declared defaults, accepted profile/saved values, form acceptance, picker/collection acceptance, CLI values, and non-interactive Apply all use the shared validator.

An invalid editable value remains visible with a field-specific message so the user can correct it; accepting/saving/applying that value is blocked. Non-secret failures include the rejected value, regex, input name, and collection index when applicable. Preserve existing secret protection at process/log boundaries and existing form presentation; do not expose credentials through diagnostic logs. Hidden-input requiredness follows the current visibility policy, while an explicitly submitted hidden value still undergoes type/regex validation.

Remove inspector-helper fallback handling from `internal/mcp/actions.go` and `cmd/aact/main.go`, input-name authentication assumptions such as `kubeconfig`, and capability-ID-specific credential observation in `internal/app/ui_save_apply.go`. Declared commands resolve against their capability/component directory and run through the generic process boundary. Missing declared executables produce their actual path and OS error; AACT does not substitute its own executable.

Package-specific helpers remain separately shipped declared commands. They can consume profile data through the generic JSON protocol and return their existing preparation/auth/runtime results. Any compatibility with their old target-shaped request is handled by the separately packaged helper, not by choosing a package ID in AACT. Declare observable managed credential filenames in manifest metadata if needed to preserve the current credential-presence display.

The AACT executable must no longer depend on `internal/packagehelpers`. This does not authorize changing an MCP server's implementation language, replacing vendor services, or changing their API behavior.

## 6. UI, CLI, status, and verification

The home screen shows Capabilities on the left and the selected capability's profiles immediately on the right, plus `Create another profile` and relevant details. Opening a profile leads to its declared input sections, component choices, and agent destinations. Preserve the accepted two-pane layering and navigation; the user should encounter no environment/target chooser.

For each profile, expose configuration readiness, each skill's actual installation by agent, each MCP's actual registration by agent, and each MCP's running/stopped/unknown state. Native plugin installation is observed as an additional component status. Complete inputs mean configured even when no installation succeeded; a partial profile says needs input. Stale or failed observations say unknown/unavailable with the real error. An AACT record is ownership/history evidence, not proof that an external config or container still exists.

The CLI's `--profile` selects a capability configuration profile. `--mcp` selects an MCP child for a lifecycle/log operation. Do not reuse the word profile for an MCP child. Removed environment/target selector flags return a useful migration message; storage-directory aliases can remain compatible. Settings/help/diagnostics use Capability Pack, Catalog, Profile, and profile configuration directory terminology. Existing `--skills-only`, per-agent overrides, external endpoint attachment, JSON results, progress, cancellation, and exit status conventions remain supported.

Tests cover local/bundled/imported definitions, profile discovery/creation, input policies, Boolean checkboxes and regex across entry points, multi-skill installation, linked selections, optional native plugins, adapter capability/detection/config behavior, truthful observed status, generic script execution, keyboard interactions, state compatibility, and packaged resource delivery. Use neutral fixtures and temporary agent homes/state/configs. Cross-platform coverage includes native CI on macOS/Linux/Windows, with MinGW/WSL behavior checked through the existing platform abstraction; do not confuse either with a remote agent's identity. Native/plugin/runtime checks that cannot run are explicitly reported rather than presented as tested.
