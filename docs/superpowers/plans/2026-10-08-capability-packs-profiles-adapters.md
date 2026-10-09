# Capability Packs, Profiles, Components, and Agent Adapters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Historical execution direction (superseded by the user’s later instruction):** The user selected superpowers:executing-plans and authorized implementation on 2026-10-08. The current direction requires all implementation and repository/terminal tool work through Luna subagents; the primary controller coordinates only. Use subagent-driven-development, TDD, independent review gates, and no nested subagents. Task checkpoints and the shared ledger remain required.

**Goal:** Make AACT operate on a Capability Pack's profiles, composed catalogs and selectable skill/MCP/plugin components through complete agent adapters, with generic execution and truthful status.

**Architecture:** Normalize local, bundled and imported catalog entries into capabilities; resolve pack-owned profile inputs through the existing answer/policy/state machinery. The app coordinates one capability operation, optional agent feature interfaces perform agent-specific work, and the runtime manager handles Docker. Preserve current state storage and the accepted TUI interaction model.

**Tech Stack:** Go, existing TOML decoder, Go `regexp`, existing Mustache/render/process/state packages, Bubble Tea/forms, existing native CI and release packaging. Pack bootstrap examples use Git plus POSIX shell or PowerShell; AACT itself does not gain a Git dependency.

**Spec:** `docs/superpowers/specs/2026-10-08-capability-packs-profiles-adapters.md`.

**Status:** implementation in progress on the user-selected branch; detailed task status and verification are in this plan's `.superpowers/sdd/` ledger. No push, PR, merge, release or live migration is authorized.

## Global Constraints

- Use `/Users/pharkink/sources/another-agent-capability-toolkit` on the user-created `feature/restructure-concepts` branch. Do not create or reuse another branch or worktree.
- Preserve unrelated changes, including the existing untracked UX proposal images.
- The user requires all implementation and repository/terminal tool execution through Luna subagents; follow subagent-driven-development with TDD and independent review gates. The primary controller coordinates only. Do not spawn nested subagents. Use Luna for implementation and final review unless the user authorizes escalation.
- Capability behavior comes from manifests, declared commands/scripts, and templates. AACT contains generic interpretation, rendering, execution, coordination, and state tracking.
- Agent detection, locations, config inspection, skill installation/removal, MCP registration changes, and supported native plugin installation/removal belong to agent adapters.
- Pack profile TOMLs are read-only inputs. Keep the current local-state root, JSON files, hashed identifiers, locks, ownership records, and credentials; do not replace them with mirrored TOMLs.
- User-facing and operation-request selection is Capability Pack → Capability → Profile. Environment and Target are not additional selection levels.
- Tests about AACT belong in AACT. Use TDD and isolated fixtures; do not change live user agent configs, credentials, existing MCP instances, or other repositories.
- Preserve keyboard-first Norton Commander interaction, mouse convenience, fixed palettes per layer, visible scrolling, inline editable inputs/paste, unsaved-change handling, progress, explicit failures, and restored terminal modes.
- Plan execution does not authorize a push, PR creation, merge, version tag, release, deployment, or live migration. Those actions need a fresh, specific user instruction.

## Review Focus

1. A vendored catalog moves or has its own profile directory: relative command/resource references must still resolve to its definition, while profile values come only from the active pack. Tests: Tasks 3, 4 and 10.
2. A same-named profile has legacy records or several MCP children: preserve unambiguous state/auth keys, and report ambiguity without guessing or selecting an environment. Tests: Tasks 4 and 12.
3. An agent is installed but its config file is absent, or JSON and JSONC both exist: the adapter alone decides the effective path and creation support; the UI and writer agree. Tests: Tasks 6 and 8.
4. A linked item shares a skill across enabled MCPs, then one step fails: install the skill once, preserve ownership/reference counts, and report only achieved effects. Tests: Tasks 5, 11 and 12.
5. An unsupported plugin or invalid collection value appears during a draft: keyboard selection must remain understandable, errors must identify the item/value, and no unsupported or invalid operation is dispatched. Tests: Tasks 2, 9 and 15.
6. A Boolean is explicitly false or fixed to false: requiredness, CLI decoding, dirty detection and checkbox presentation must distinguish false from an omitted value. Tests: Tasks 2, 4, 13 and 15.

## Scope and decisions

This covers every topic discussed after the branch selection: the original eight requirements, the replacement of direct URL fetching with pack bootstrap scripts, external catalog imports, configured profile-directory names, several skills per capability, Select all with individual/linked-item deselection, unchanged local-state storage, Boolean inputs with checkboxes, and complete optional-feature agent adapters including native plugins and the generic skills adapter.

The additive TOML/interface spellings in the spec are concrete proposals for plan review. The initial native plugin implementation is proposed for Claude Code's verified local format; this is the only new vendor-specific plugin scope in the plan. The user owns architecture: an unresolved architectural or trust-boundary change is not an implementation ruling.

Existing product code observed while planning uses `config.Source/Target`, an MCP-only `agents.Adapter`, generic skill installation outside that interface, JSON answer files keyed by `state.Key.ID()`, single `Package.Skill`, and multiple MCP definitions. `cmd/aact` currently imports `internal/packagehelpers`; that dependency is explicitly removed here.

## File responsibilities

| Area | Responsibility after this work |
|---|---|
| `internal/catalog/` | Capability/component declarations, generic normalization, component sets and input schema |
| `internal/config/` | Pack discovery, local catalog imports, configured names, profile discovery and path/policy resolution |
| `internal/forms/` | Shared validation, draft editing and component checklist interaction |
| `internal/state/` | Existing persistence plus minimal logical-profile/legacy-key and component metadata compatibility |
| `internal/render/` | Generic per-skill/plugin staging, commands and templates |
| `internal/install/` | Agent-neutral owned filesystem installation primitives used by adapters |
| `internal/agents/` | Registry, optional feature interfaces, each agent's detection/paths/inspection/install behavior |
| `internal/app/` | Profile resolution, whole-capability coordination, adapter/runtime observation aggregation |
| `internal/mcp/`, `internal/process/` | Generic declared actions and Docker/process operations |
| `internal/tui/`, `internal/cli/`, `internal/viewmodel/` | Profile-based presentation and request/result contracts |
| `packages/`, `cmd/inspector-helper/` | Capability-owned metadata/commands; existing service behavior retained |
| `examples/capability-pack/`, `docs/capability-packs.md` | Maintainer starting point and creation/import/profile documentation |
| `docs/demo/`, `tests/ux/`, `tests/integration/` | Updated reference interaction and real AACT regression/packaged tests |

## Shared contracts

Task authors may add private helpers, but public names below must match across tasks. Introduce compatibility bridges before removing old entry points so each task leaves the branch buildable.

- `catalog.Package.Skills []Skill`, `Plugins []Plugin`, `Sets []ComponentSet`, `ManifestID string`; the latter two are resolved metadata with `toml:"-"`. `ManifestID` preserves definition provenance when the configured catalog ID replaces `Package.ID`. `SkillDefinitions() []Skill`, `HasSkill() bool`, `PluginDefinitions() []Plugin`; existing `MCPDefinitions()`/`HasMCP()` remain. Reject mixed singular/list declarations for the same kind. A plugin-only capability is valid.
- `catalog.Skill{Name, Source string; Files []string; Templates []Template; Generator *Command}`. Legacy package-level templates/generator normalize into the legacy single skill. `Source` resolves against the owning package directory.
- `catalog.Plugin{Name, Format, Source, EnabledInput string}`. Initial format ID: `claude-code`. Paths are definition-relative; native format handling belongs to its adapter.
- `catalog.ComponentSet{Name string; Skills, MCPs []string}`; `catalog.InstallationItem{ID, Label string; Skills, MCPs, Plugins []string}`; `InstallationItems(Package, []ComponentSet) ([]InstallationItem, error)`. Explicit sets must not overlap. Unset independent components become individual items; old single-skill/MCP manifests retain a linked item. Native plugin items are separately selectable and are not implicitly checked by Select all skills.
- `catalog.Input.Regex string` with `toml:"regex"`. `forms.ValidateProvided(defs []catalog.Input, values map[string]any) error` checks supplied values without requiring a complete form; existing `forms.Validate` adds active required/group checks. Retain `Input.Type == "boolean"`: resolved values are `bool`; explicit `false` satisfies requiredness. The TUI uses a keyboard/mouse checkbox with the same fixed/default/reset policy as every other input.
- `config.Pack{ID, Root, ManifestPath, ProfileRoot string; Catalog []catalog.Package; PackageDefaults map[string]map[string]any}`. Preserve identity generation and read compatibility via a temporary `Source` bridge; `pack_id` is the preferred TOML spelling.
- `config.ProfileRef{PackID, CapabilityID, Name string}`; `config.Profile{Ref ProfileRef; Path, Error string; Raw map[string]any; InputPolicy map[string]string}`; `DiscoverProfiles(Pack, capabilityID string) ([]Profile, error)`; `LoadProfile(Pack, capabilityID, name string) (Profile, error)`. Discovery returns a row with `Error` for an individually malformed file, keeping other profiles visible; root enumeration failures return the function error. Explicit loading of an invalid profile fails.
- `state.Store.ResolveProfileKey(packID, capabilityID, profileName string) (Key, error)` resolves a logical profile to an existing unambiguous key or the compatible new key shape. Preserve `Key.ID()` and current persisted formats. For a new logical profile use the existing `Source/Package/Target` slots internally with no environment selection. Do not repurpose persisted `Key.Profile`, which existing code uses for some MCP children.
- `agents.Scope{ID, Home, ConfigPathOverride string; ExplicitHome bool}`; existing process/home overrides are interpreted by the adapter.
- `agents.FeatureSet{Skills, MCPs bool; PluginFormats []string}`; `agents.Adapter` exposes `ID() string`, `Name() string`, `Features() FeatureSet`, `Detect(context.Context, Scope) (Detection, error)`, and `Observe(context.Context, Scope, ObservationRequest) (Observation, error)`.
- `agents.ConfigFile{Path, Scope, Precedence, Evidence string; Exists bool}`; `Detection{State, Evidence, Reason string; Installed, CanCreateConfig bool; ConfigFiles []ConfigFile}` with states `installed`, `not-detected`, `unverified`; `ObservationRequest{Key state.Key; Managed []state.Installation; IncludeInventory bool}`; `ComponentObservation{Kind, Name, Status, Path, RegistrationName, URL, Transport, Error string; Managed bool}`; `Observation{Detection Detection; Components []ComponentObservation; ConfigFiles []ConfigFile; Errors []string; ObservedAt time.Time}`. Component status values are `installed`, `absent`, `modified`, `unavailable`. These types are created before app/UI consumers move.
- Optional `agents.SkillManager` exposes `InstallSkill(context.Context, Scope, SkillRequest) (state.Installation, error)` and `RemoveSkill(context.Context, Scope, state.Installation) error`. `SkillRequest{Key state.Key; Package catalog.Package; Skill catalog.Skill; StagedDir string}`. The adapter returns the achieved effect; the coordinator records it using the existing state store.
- Optional `agents.MCPManager` exposes `Register(context.Context, Scope, MCPRequest) (MCPRegistrationResult, error)` and `Unregister(context.Context, Scope, state.Installation) error`. `MCPRequest{Key state.Key; Registration Registration}` uses the existing registration value type. The selected agent adapter owns native MCP config changes; app flows and detection use the adapter registry and never fall back to a second MCP adapter API.
- Optional `agents.PluginManager` exposes `InstallPlugin(context.Context, Scope, PluginRequest) (state.Installation, error)` and `RemovePlugin(context.Context, Scope, state.Installation) error`. `PluginRequest{Key state.Key; Plugin catalog.Plugin; StagedDir string}` contains an explicitly staged native artifact. Plugin default selection follows its declared true enabled input; without one it requires an explicit item choice.
- `agents.Dependencies{Store *state.Store; Runner process.Executor; Probe DiscoveryProbe}`; `NewRegistry(Dependencies) *Registry`; `Registry.Adapter(id string) (Adapter, error)`; `Registry.Adapters() []Adapter`. Feature assertions agree with implemented optional interfaces.
- `app.ProfileRequest{Ref config.ProfileRef; Inputs map[string]any; ResetInputs []string; ItemIDs, DestinationIDs []string; Interactive, SkillsOnly bool; ExternalURLs map[string]string}`; `PreviewProfile(context.Context, ProfileRequest) (viewmodel.SetupPreview, error)`; `ApplyProfile(context.Context, ProfileRequest) (viewmodel.OperationResult, error)`; `CreateProfile(context.Context, config.ProfileRef) error`. `ItemIDs == nil` means inherited/default selection; an explicitly non-nil empty slice means none. Persist selected intent separately from achieved effects through optional metadata in the existing profile record. Creation records local identity; acceptance/apply owns answer validation.
- `app.ProfileSnapshot(context.Context, capabilityID string) (viewmodel.CapabilityProfileSnapshot, error)` aggregates source and local profiles, adapter observations and runtime observations. `CapabilityProfileSnapshot{Profiles []CapabilityProfile; Errors []string; ObservedAt time.Time}`; `CapabilityProfile{Ref config.ProfileRef; Key state.Key; Origin, ConfigStatus, ConfigError string; Components []ComponentStatus; MCPs []RuntimeStatus}`; `ComponentStatus{AgentID, Kind, Name, Status, Error string}`; `RuntimeStatus{MCPID, Status, Ownership, URL, Error string; ObservedAt time.Time; Stale bool}`. Configuration statuses are `configured`, `needs-input`, `invalid`; component statuses follow adapter observations. MCP definitions are called components/servers, never configuration profiles.
- `viewmodel.SetupRequest` carries `Ref config.ProfileRef`; `SetupPreview` includes `PackRoot, ProfilePath, ProfileTOML, ProfileOrigin string`, `Inputs []SetupInput`, `Items []catalog.InstallationItem`, `SelectedItemIDs []string`, existing destination rows extended with `Features agents.FeatureSet`, and `ValidationIssues []string`. Temporary old-method bridges are removed after all UI/CLI consumers move.

## Subagent execution protocol

At execution start, verify the user branch and existing checkout; that explicitly selected checkout overrides the skill's default worktree creation. Do not run a worktree creator. Resolve this plan's scratch directory with `bash <sdd-skill>/scripts/sdd-workspace <plan-path>`, then read/create its `progress.md` ledger with the plan identity on the first line. No scratch workspace is required merely to write this plan.

Use a fresh `gpt-6-luna` implementer for each task, with `fork_turns="none"`; use the skill's `task-brief` file plus the task's dependencies, exact contracts, allowed files, global constraints, report-file path and a prohibition on child subagents. One implementation worker at a time in this shared checkout. Independent read-only context gathering or review preparation may overlap. Batch small adapter variants within the task rather than creating a worker for each trivial edit.

Luna subagents perform all implementation, repository and terminal work, and local task commits; the primary controller coordinates only. Do not spawn nested subagents. Never stage all files. Include the repository's `Agent-Conversation` provenance trailer. Capture task BASE before dispatch, produce a BASE..HEAD review package covering every task change, then use a fresh Luna reviewer for both spec compliance and quality. Fixes return to a worker and receive a scoped re-review. A local checkpoint is not approval to push or merge.

Preflight records a table for every shared interface/file between tasks and every task's internal consistency. Ledger routine rulings, red/green commands/output, commits, reviews, deferred findings and completion. Resume completed tasks from the ledger after compaction. Do not silently reinterpret architectural requirements.

Repair limit: at most five failed verification/review repair rounds per task. Rounds 1–3 resume its implementer; later rounds need fresh eyes. Model escalation above Luna requires user permission despite the skill's automatic escalation preference. At the approved repair limit, pause the affected task under the Constitution; continue independent authorized work. A final whole-branch review uses Luna unless the user authorizes a stronger reviewer, followed by at most one consolidated fix wave and scoped re-review. No repair merge allowance is granted.

## Dependencies and checkpoints

| Task | Deliverable | Depends on |
|---|---|---|
| 1 | Component manifests and set normalization | — |
| 2 | Shared input validation and Boolean checkboxes | 1 |
| 3 | Pack/catalog import resolution and configured names | 1 |
| 4 | Profile discovery, creation identity and state compatibility | 2, 3 |
| 5 | Several staged skills and agent-neutral file installation | 1, 4 |
| 6 | Full adapter registry/detection/observation contract | 4 |
| 7 | Adapter skill feature and generic All destination | 5, 6 |
| 8 | Adapter-owned MCP inspection/registration/config policy | 6 |
| 9 | Native plugin feature and Claude implementation | 1, 6 |
| 10 | Generic declared command execution and helper delivery | 1, 4 |
| 11 | Coordinated capability apply/remove through adapters | 7, 8, 9, 10 |
| 12 | Actual profile/component status aggregation | 4, 6, 11 |
| 13 | Profile-based CLI and settings | 11, 12 |
| 14 | Home profiles, creation, and terminology | 12, 13 |
| 15 | Component/agent selection and native plugin availability | 11, 14 |
| 16 | Maintainer docs/bootstrap examples and interactive reference | 3, 4, 15 |
| 17 | Integrated/native/packaged verification and final review | 1–16 |

The order is chosen for a single buildable branch; the dependency table enables independent read-only preparation without overlapping implementation edits.

---

### Task 1: Normalize multiple skills, plugins, and catalog component sets

**Files:** Modify `internal/catalog/model.go`, `internal/catalog/manifest.go`; create `internal/catalog/components.go`, `internal/catalog/components_test.go`; extend `internal/catalog/manifest_test.go` and `internal/catalog/testdata/` fixtures. Once this plan is approved for execution, update `AGENTS.md`'s approved-plan pointer to this plan so future workers use the new specification.

**Interfaces:** Produce the catalog component/skill/plugin/item contracts above. Consume existing `Command`, `Template`, `MCPDefinitions`, and manifest validation. Make singular/list compatibility explicit before app consumers change.

- [ ] Write `TestLoadMultipleSkillsWithIndependentRoots`: a neutral capability contains `review-guidance` and `release-notes`, resolves both roots, and retains templates/generator references per skill. Add tests for legacy `[skill]`, mixed singular/list rejection, duplicate component names, plugin-only declarations, invalid plugin source, and distinct component kinds with the same human label.
- [ ] Write `TestInstallationItemsLinkDeclaredComponents`: one explicit set contains `review-guidance` + `repository-api`; `release-notes` is an individual item. Assert `len(items) == 2`, the linked item's `Skills == []string{"review-guidance"}` and `MCPs == []string{"repository-api"}`, and the independent item's `Skills == []string{"release-notes"}`. Initial selection is applied by Task 11, not by item normalization. Reject overlapping/unknown set references, preserve legacy linked items, and do not alter an MCP enable-input value.
- [ ] Run `go test ./internal/catalog -run 'Test(LoadMultipleSkills|InstallationItems|LoadPlainSkill|LoadBundle)' -count=1`; record the intended missing-field/normalization failures.
- [ ] Add the additive fields/helpers and normalization. Preserve package-relative origins; keep catalog set overlay data generic. No package-ID branches.
- [ ] Run the focused tests and `go test ./internal/catalog -count=1`; report red/green evidence and changed files. Controller checkpoint: `feat: declare capability component bundles`.

### Task 2: Validate inputs consistently and render Boolean checkboxes

**Files:** Modify `internal/catalog/manifest.go`, `internal/forms/validate.go`, `internal/forms/resolve.go`, `internal/forms/editor.go`, `internal/forms/editor_ui.go`, `internal/forms/collection.go`; create `internal/forms/regex_test.go`, `internal/forms/boolean_input_test.go`; extend catalog/form editor tests.

**Interfaces:** Consume `Input.Regex`; produce `ValidateProvided`; retain `Validate`, `Resolve`, `ResolvePartial`, editor and collection APIs. Draft preview may carry validation issues; accepted values may not bypass validation.

- [ ] Write table tests using `regex = '^[a-z][a-z0-9-]*$'`: `ValidateProvided(defs, map[string]any{"name": "review-kit"}) == nil`; rejecting `Review Kit` must contain `name`, `Review Kit` and the pattern. Reject malformed `[` at manifest load. Assert explicit anchoring remains the author's choice.
- [ ] Write tests for a collection's second invalid item (report index 2/value), numeric/boolean normalized text, optional empty values, invalid declared defaults, fixed prefills, explicit hidden submissions, inline acceptance, paste acceptance, file/directory picker acceptance, and reset to an invalid inherited value. A rejected picked value remains visible with its validation reason. Preserve visibility-controlled requiredness and exclusive groups.
- [ ] Characterize existing Boolean decoding/toggling, then write missing checkbox regressions: `type = "boolean"`, `default = false` resolves as `bool(false)` and satisfies requiredness; the field displays `[ ]`, Space/Enter yields `bool(true)` and `[x]`, and mouse toggling produces the same value. Editable profile/default values can change; fixed true/false values remain displayed and locked; reset restores the typed inherited value. Unchanged false values do not create a dirty draft. Characterization tests may already pass; record a genuine red presentation/behavior test before changing code.
- [ ] Run `go test ./internal/catalog ./internal/forms -run 'Regex|InvalidPattern|BooleanInput' -count=1`; record the intended failures.
- [ ] Compile declared regexes and validate normalized provided values centrally. Keep invalid editable drafts visible with errors, block acceptance/apply, and preserve secret redaction in diagnostics without changing existing field presentation.
- [ ] Render scalar Boolean inputs as checkboxes using the existing focus/keyboard/mouse infrastructure. Preserve the layer palette and displayed default/fixed/reset hints; do not route a Boolean through a separate text editor.
- [ ] Run the focused tests plus `go test ./internal/forms ./internal/catalog -count=1`; report evidence. Controller checkpoint: `feat: validate typed inputs and render Boolean checkboxes`.

### Task 3: Compose pack catalogs from initialized local repositories

**Files:** Modify `internal/config/source.go`, `internal/config/identity.go`; create `internal/config/pack.go`, `internal/config/catalog_imports.go`, `internal/config/catalog_imports_test.go`; extend `internal/config/source_test.go`, `paths_test.go`.

**Interfaces:** Produce `Pack` and the compatible discovery bridge. TOML adds `pack_id`, `[[imports]] {catalog, include, overrides}` and optional catalog `id`; configured ID defaults to the loaded capability ID and chooses its profile directory. Preserve originating manifest/directory provenance separately from configured identity.

- [ ] Write `TestPackCombinesLocalBundledAndImportedCatalog`: import a temporary vendored manifest, select two entries, rename one to `company-handoff`, resolve its resources relative to the vendored manifest, and retain the active pack's profile root/defaults. External profile directories and environment defaults are not imported.
- [ ] Test duplicate manifest IDs under different configured names, actionable unresolved duplicate errors with both file paths, nested-import cycles, missing vendor paths without automatic command/network execution, invalid include/override references, and `pack_id`/`source_id` compatibility/conflicts.
- [ ] Run `go test ./internal/config -run 'Pack|CatalogImport|ConfiguredName' -count=1`; record intended loader failures.
- [ ] Implement ordered local import expansion/filter/override logic. Support direct local and `bundled:` sources; do not implement URL fetching or automatic bootstrap. Apply catalog set relationships from Task 1.
- [ ] Run focused and existing identity/path tests; report evidence. Controller checkpoint: `feat: compose capability pack catalogs`.

### Task 4: Discover profiles and reuse existing local state identity

**Files:** Create `internal/config/profile.go`, `profile_test.go`, `internal/state/profile_keys.go`, `profile_keys_test.go`; modify `internal/config/target.go` compatibility code, `internal/state/profiles.go`, `profiles_test.go`, and state fixture tests. Do not relocate state files.

**Interfaces:** Produce `ProfileRef`, `Profile`, `DiscoverProfiles`, `LoadProfile`, and `Store.ResolveProfileKey`. Minimal optional profile metadata distinguishes local creation from pack declaration; old key/answer/auth formats remain readable.

- [ ] Write discovery tests for `<ProfileRoot>/company-handoff/ota.toml` and `prod.toml`, empty directories, explicit external profile root, malformed profile diagnostics, safe component names, symlink/path escapes, and relative file values from the profile's declaring directory.
- [ ] Test default/fixed values using existing `forms.ResolvePartial`: saved editable values win; fixed values win over saved/CLI; reset restores inherited data; unknown policies fail with file/input details. Include explicit Boolean false at each layer without treating it as missing. Profile files remain byte-for-byte untouched.
- [ ] Test old hashed keys/answer/auth locations are reused for an unambiguous profile: `resolved.ID() == legacy.ID()` and `store.AuthDir(resolved) == store.AuthDir(legacy)`. Persisted MCP-child `Key.Profile` is not repurposed. Ambiguous old environment/target matches report both records and require explicit repair. New/local profile keys use the compatible existing storage slots.
- [ ] Run `go test ./internal/config ./internal/state -run 'Profile|Fixed|Default|LegacyKey' -count=1`; record intended failures, implement the discovery/key bridge, then verify green with both package suites.
- [ ] Report red/green evidence and all compatibility cases. Controller checkpoint: `feat: discover pack profiles without replacing state storage`.

### Task 5: Stage several skills and decouple filesystem installation from agent types

**Files:** Modify `internal/render/render.go`, `generator.go`, `render_test.go`, `internal/install/skills.go`, `skills_test.go`, `ownership.go`; create `internal/render/skills.go`, `skills_test.go`, `internal/render/plugins.go`, `plugins_test.go`, `internal/install/destination.go`; adapt existing `internal/app/service.go`, `ui_setup.go` installer arguments without adding policy; extend state installation fixtures if named-component metadata is necessary.

**Interfaces:** Produce `render.StageSkill(ctx context.Context, pkg catalog.Package, skill catalog.Skill, inputs map[string]any, profile config.Profile, parent string) (string, error)` and `StagePlugin(ctx context.Context, pkg catalog.Package, plugin catalog.Plugin, parent string) (string, error)` (copy the declared native artifact using existing containment/resource rules). Produce `install.SkillDestination{ID, Home, SkillsDir string}`, `Skills.InstallOne(ctx context.Context, pkg catalog.Package, skill catalog.Skill, destination SkillDestination, key state.Key, staged string) (state.Installation, error)`, `Skills.RemoveOne(ctx context.Context, record state.Installation) error`. Existing callers temporarily convert their resolved destinations into this neutral type. No agent import remains in the file engine. The adapter returns effects and Task 11 records them; temporary legacy wrappers preserve old recording behavior until migrated.

- [ ] Test two templated/generated skills in separate staging subdirectories (`first != second` and both contain their own `SKILL.md`), correct relative generator/template origins, one skill's failure leaving the other source/staging untouched, and the legacy single-skill Stage behavior. A template-only skill receives declared form/profile values directly, including true/false Mustache sections, without a generator. Test native-plugin staging preserves `.claude-plugin/plugin.json` and refuses escape paths.
- [ ] Test two installed skills for one profile/agent remain separate records/destinations; repeated apply is idempotent; removing one preserves another; shared identical skills survive until their last owned reference is removed; foreign or edited content is refused.
- [ ] Run `go test ./internal/render ./internal/install ./internal/state -run 'Multiple|StageSkill|Shared|Ownership|Legacy' -count=1`; record failures.
- [ ] Add per-skill staging and agent-neutral file operations. Generic file primitives may stay in `internal/install`; agent path decisions may not. Preserve existing hash/root/lock mechanics and avoid an `agents` ↔ `install` import cycle.
- [ ] Run affected package suites and report evidence. Controller checkpoint: `refactor: support staged skills through neutral file operations`.

### Task 6: Introduce the full adapter registry and detection/observation contract

**Files:** Modify `internal/agents/adapter.go`, `discovery.go`, `locations.go`; create `registry.go`, `features.go`, `observation.go`, `registry_test.go`, `contract_test.go`; add small per-agent implementation files where useful.

**Interfaces:** Produce `Adapter`, `Scope`, `FeatureSet`, `Detection`, `ObservationRequest`, `Observation`, `Dependencies`, `Registry` and optional feature interfaces exactly as listed above. Transitional MCP adapters retain a bridge until Task 11.

- [ ] Write contract tests that enumerate adapters from the registry and assert feature claims agree with implemented optional interfaces. Cover detection states installed/not-detected/unverified, a known agent with no config, explicit/custom homes, and process-native config overrides.
- [ ] Test native macOS/Linux/Windows probes and existing WSL/MinGW assumptions through injected platform/path evidence. Resolve config/skill/plugin locations through each adapter; discovery itself performs no writes.
- [ ] Run `go test ./internal/agents -run 'Registry|AdapterContract|Detect|Override' -count=1`; record the intended failures.
- [ ] Implement the registry/base wrappers for existing Codex, Claude, OpenCode, Copilot CLI, JetBrains AI/Copilot, Hermes and the upcoming generic skill adapter. Adapter-local code owns agent names/IDs/detection/layout. Do not add unverified plugin support claims.
- [ ] Run `go test ./internal/agents -count=1`; report feature matrix and evidence. Controller checkpoint: `refactor: define complete agent adapter features`.

### Task 7: Put skill operations and the generic All destination behind adapters

**Files:** Create `internal/agents/skills.go`, `skills_test.go`, `generic.go`, `generic_test.go`; modify `global_skills.go`, registry implementations and `internal/app/global_skill_destination.go` temporary delegation bridge.

**Interfaces:** Implement `SkillManager` for supported agents using Task 5 primitives. Generic adapter ID is `generic`; the compatibility/UI alias `all` is labeled `All`, uses `~/.agents/skills`, and advertises only Skills.

- [ ] Test skill install/inspect/remove through Codex, Claude, OpenCode and Hermes adapters against temporary homes, including missing skill directories and native overrides. Confirm application code need not construct an agent-specific skill path.
- [ ] Test Generic has `Features().Skills == true`, `Features().MCPs == false`, `len(Features().PluginFormats) == 0`, resolves exactly `.agents/skills`, preserves the `all` alias and rejects MCP/plugin operations without creating any config file. Several skills install and remove independently through it.
- [ ] Run `go test ./internal/agents ./internal/install -run 'SkillAdapter|Generic|Global' -count=1`; record failures, implement feature methods/delegation, then verify affected suites.
- [ ] Preserve old manual-MCP records as historical readable data; do not advertise `generic`/`all` as an MCP writer. Report evidence. Controller checkpoint: `feat: install skills through agent adapters`.

### Task 8: Move MCP config policy and inventory entirely into adapters

**Files:** Modify `internal/agents/cli.go`, `jsonc.go`, `locations.go`, `observation.go`, per-agent wrappers and tests; remove/delegate agent-specific inventory/path functions from `internal/app/ui_agents.go`, `ui_setup.go`, `ui.go`, `registration_only.go`, `ui_registration.go` as new adapter consumers become available.

**Interfaces:** Implement `MCPManager` and actual MCP observation. Registry supplies all supported agent evidence/paths/creation support; core receives declared registration requests and observations.

- [ ] Test installed agent + missing config creates only the supported config during Register; detection/Observe remains read-only. Test absent agent and unsupported config creation produce a specific disabled reason before mutation.
- [ ] Test JSONC-versus-JSON precedence, malformed config, unrelated entries/comments, explicit config override, add/update/remove, CLI stdout/stderr failures, timeout/cancellation and observation of an externally removed/modified registration.
- [ ] Run `go test ./internal/agents -run 'MCPAdapter|MissingConfig|JSONC|Registration|Observe' -count=1`; record failures.
- [ ] Move remaining agent-ID/path/config knowledge behind the relevant adapter. Keep file/CLI semantics and preserve unrelated config. Generic and Hermes do not gain MCP features merely to satisfy an interface.
- [ ] Run agent suites plus directly affected app tests through compatibility facades; report evidence. Controller checkpoint: `refactor: make adapters own MCP config and inspection`.

### Task 9: Install and observe an explicitly declared native plugin

**Files:** Create `internal/agents/plugins.go`, `claude_plugins.go`, `claude_plugins_test.go`, neutral native plugin fixtures; extend registry/features/observation and catalog plugin validation tests. Do not hand-edit the live Claude plugin registry.

**Interfaces:** Implement `PluginManager` for format `claude-code`. `PluginRequest` contains the staged native artifact; adapters without a verified implementation expose no matching plugin format.

- [ ] Write tests for local `.claude-plugin/plugin.json` validation and a local adapter-owned marketplace descriptor referencing the staged artifact. Assert native CLI add/install/list/uninstall/remove commands use the selected isolated agent context and user scope; no remote catalog fetch is added to AACT.
- [ ] Test unsupported adapter/format is not selectable or invoked, failures retain exact native CLI diagnostics, observation uses actual native inventory, repeat installation is idempotent, and removing one managed plugin preserves unrelated plugins and any still-used marketplace.
- [ ] Run `go test ./internal/agents -run 'Plugin|ClaudePlugin' -count=1`; record failures, implement the optional feature and ownership metadata, then verify green.
- [ ] If the local Claude CLI can run with a temporary config root, verify validate/install/list/remove of a harmless fixture there only. Otherwise report mocked versus native evidence explicitly. Other native formats remain unsupported rather than guessed.
- [ ] Report the inspected vendor contract and feature matrix. Controller checkpoint: `feat: install native plugins through supported adapters`.

### Task 10: Execute capability commands generically and ship their helpers

**Files:** Modify `internal/mcp/actions.go`, `actions_test.go`, `internal/app/ui_save_apply.go`, `cmd/aact/main.go`, `internal/render/generator.go`; update `internal/packagehelpers/protocol.go` compatibility and package manifests for declared credential files; inspect/update `.goreleaser.yaml` and `tests/packages/public_test.go`; create `tests/integration/declared_resources_test.go` for archive/resource checks.

**Interfaces:** Add `Profile config.Profile` to the generic action request with `json:"profile"`; keep protocol version 1 and existing action/input/package/state context fields. Do not populate old `Target` selection in generic workflows; standalone helper compatibility consumes Profile and converts only internally where existing package routines need that shape. Add `MCP.CredentialFiles []string` with `toml:"credential_files"`, paths relative to the existing managed auth directory; observation only states file presence, not authenticated success. Named execution, OS variants, parsing, cancellation and diagnostics use existing process contracts.

- [ ] Test an arbitrary neutral capability command executes exactly its declared argv/cwd/input envelope, handles Windows overrides, and emits choices/runtime results without recognizing any capability ID or input name. A non-interactive command may handle its own saved/file/env credentials.
- [ ] Test a missing executable returns its actual resolved path and OS error; never self-executes AACT. Test timeout, invalid JSON, bounded output and real stderr. Test generic manifest-declared credential-file presence using `session.bin` rather than a domain-specific name.
- [ ] Run `go test ./internal/mcp ./internal/render ./internal/app -run 'GenericAction|DeclaredCommand|CredentialObservation|Action' -count=1`; record failures.
- [ ] Remove helper-name/ID fallback and special authentication guards, remove `__aact_internal_inspector_helper` dispatch, and update helper/manifests for generic profile requests. Preserve the services and standalone command implementations.
- [ ] Assert `go list -deps ./cmd/aact` excludes `internal/packagehelpers`; verify archive/resource tests ship every declared executable for each supported OS/arch. Report evidence. Controller checkpoint: `refactor: execute all capability actions from declarations`.

### Task 11: Apply selected capability components through adapter features

**Files:** Create `internal/app/profile_apply.go`, `profile_apply_test.go`, `component_selection.go`, `component_selection_test.go`; refactor `service.go`, `ui_setup.go`, `registration_only.go`, `ui_registration.go`, `global_skill_destination.go`, `mcp_profiles.go`; extend viewmodel operation/setup contracts.

**Interfaces:** Produce `ProfileRequest`, `PreviewProfile`, `ApplyProfile`, `CreateProfile` and the new viewmodel setup contracts. Consume profile discovery/key mapping, installation items, renderer, registry optional features and generic runtime/actions.

- [ ] Test one Apply installs two skills, registers two enabled MCPs and an explicitly selected supported plugin through injected adapters, with one skill shared by MCPs installed once. Assert omitted independent items are not installed and linked members cannot be selected inconsistently.
- [ ] Test selected-all skill items do not change disabled MCP enable-input values or implicitly enable native plugins; `ItemIDs == nil` uses defaults/saved choice and `ItemIDs = []string{}` deselects everything. Fixed inputs survive overrides; invalid regex means `runnerCalls == 0`, `adapterCalls == 0` and `runtimeCalls == 0`. External endpoint attachment installs its companion skill and registers it without starting Docker.
- [ ] Test failure at generation/auth/start/skill/register/plugin/record reports exact step/diagnostics and actual completed effects; no automatic revert and no false checked destination. Deselection removes only owned selected capability bindings and preserves unrelated agent config/shared resources.
- [ ] Run `go test ./internal/app -run 'ApplyProfile|ComponentSelection|CreateProfile' -count=1`; record failures, implement the whole operation using adapter interfaces, then verify relevant existing Save/Apply/ownership/error tests.
- [ ] Remove core agent-path/agent-ID decisions and direct skill installer calls. Retain generic orchestration and temporary UI/CLI wrappers only until Tasks 13–15 migrate. Report evidence. Controller checkpoint: `feat: apply complete capability profiles through adapters`.

### Task 12: Aggregate actual status for every capability profile

**Files:** Create `internal/app/profile_snapshot.go`, `profile_snapshot_test.go`; refactor `internal/app/ui_profiles.go`, `ui_agents.go`; update `internal/viewmodel/profile.go`, `agent.go`, setup status and relevant existing tests.

**Interfaces:** Produce `CapabilityProfileSnapshot` and `ProfileSnapshot(ctx, capabilityID)`. Each profile includes origin/readiness and named skill/MCP/plugin statuses per agent; runtime observation retains ownership/stale/conflict information.

- [ ] Test profiles appear directly from pack files before any state/runtime records exist. A partial profile reports needs input; a complete profile is configured while its absent skills/MCP registrations remain absent/stopped. Local-created profiles join the same capability list.
- [ ] Test removing an actual managed skill link/registration/native plugin changes observation even when its ledger record remains. Different agents/MCP children/profiles do not fill each other's statuses. Unknown adapter/runtime errors become unavailable with concrete diagnostics.
- [ ] Test legacy key reuse, externally running/other-AACT ownership, external attach, duplicate runtime claims, missing/stopped/running runtime states and stale last observations. Do not infer auth success from presence of a credential file.
- [ ] Run `go test ./internal/app -run 'CapabilityProfileSnapshot|ObservedProfile|LegacyProfileStatus' -count=1`; record failures, implement discovery + adapter + runtime aggregation, then run affected profile/status tests.
- [ ] Report evidence. Controller checkpoint: `feat: report observed capability profile status`.

### Task 13: Use configuration profiles consistently in the CLI and settings

**Files:** Modify `internal/cli/flags.go`, `run.go`, tests; update app settings/config commands and CLI integration fixtures.

**Interfaces:** `--profile` means configuration profile; `--mcp` means an MCP component only. Optional repeatable `--item ITEM_ID` selects installation items explicitly; omission uses defaults/saved selection. CLI maps to `ProfileRequest`; pack discovery remains `--config`. Preferred directory command is `config set-profile-directory`; old storage-root aliases remain compatible.

- [ ] Test install/apply with a named pack profile, sole-profile inference, multiple-profile error listing names, local profile creation, and existing no-prefill `--skills-only` usage. Configuration profile and selected MCP child stay separate for start/stop/log commands.
- [ ] Test old runtime `--environment`/`--target` flags produce a clear profile-based migration message, storage-directory aliases still resolve correctly, `settings` reports pack/catalog/profile terms, and launching from another working directory honors the saved/explicit pack.
- [ ] Test CLI `--set` regex failure includes field/value and exits 2 before effects; operation failure exits 1 with actual stderr; supported JSON output reports component effects and observation separately.
- [ ] Test `--set enabled=false` preserves a typed false value through validation, saved answers, JSON command input and templates. Required Boolean false is accepted; a fixed Boolean wins over supplied values under the same policy as other fixed inputs and is shown as locked in preview.
- [ ] Run `go test ./internal/cli -run 'Profile|Settings|Regex|SkillsOnly|Legacy' -count=1`; record failures, migrate parsing/help/commands and then run the CLI suite.
- [ ] Report evidence. Controller checkpoint: `feat: select capability profiles in the CLI`.

### Task 14: Show profiles immediately and replace the target/environment hierarchy

**Files:** Modify `internal/tui/home.go`, `model.go`, `profiles.go`, `setup.go`, `workspace.go`, `management.go`, `management_details.go`; replace/remove `target_chooser.go` and environment-specific viewmodel/app UI entry points once unused; add `profile_home_test.go`, `profile_creation_test.go`, update interaction tests.

**Interfaces:** Consume profile snapshot/setup contracts and local `CreateProfile`. Home right-pane rows are configuration profiles, not separate MCP children pretending to be profiles.

- [ ] Write keyboard interaction tests: selecting a capability immediately populates all its profile rows/statuses; Enter on the left focuses the right; Enter on a selected profile opens that profile; Tab/arrows preserve the existing pane philosophy and profile scroll position.
- [ ] Test `Create another profile` appears with zero and several profiles, opens a name/input draft, and creates only local state after acceptance. There is no synthetic default row before creation and no Environment/Target chooser. Cancel leaves no saved profile; name collisions are clear.
- [ ] Test headers, menus, settings, help, errors and information panes use Capability Pack/Catalog/Profile terms and profile configuration directory terminology. Structured status sections show actual skill/registration/runtime data for both skill-only and MCP capabilities.
- [ ] Run `go test ./internal/tui -run 'ProfileHome|ProfileCreation|Home|Hierarchy|Settings' -count=1`; record failures, migrate presentation and remove old selector flows, then verify existing navigation/palette/scroll/exit regressions.
- [ ] Report evidence. Controller checkpoint: `feat: browse capability profiles from the home screen`.

### Task 15: Select multiple skills, linked items, agents, and supported plugins

**Files:** Modify `internal/tui/setup.go`, `workspace.go`, `registration_overlay.go`, `internal/forms/editor_ui.go` if a reusable checklist primitive is needed; add `internal/tui/component_selection_test.go`, `adapter_feature_navigation_test.go`; update relevant agent/setup fixtures.

**Interfaces:** Consume `InstallationItem`, adapter feature observations and `ProfileRequest.ItemIDs/DestinationIDs`. One bottom-of-form Save and apply dispatches the whole capability.

- [ ] Test all independent skill rows initially checked; keyboard Select all rechecks them; Space/Enter unchecks one independent skill while others remain; unchecking a linked item removes its skill+MCP members together. No mouse action is required.
- [ ] Test Generic/All is offered for skill-only selection and cannot manage MCP/plugin components. Absent agents are unavailable with reasons; installed/missing-config agents remain selectable when their adapter can create config. Unsupported native plugins/formats are visibly unavailable and cannot be selected through keyboard, mouse or stale UI state.
- [ ] Test selected agents do not cause a partial capability to be presented as completely installed. Native plugin choice reflects compatible destinations. Existing input sections remain manifest-owned; no capability-specific fields/sections appear on unrelated capabilities.
- [ ] Exercise a manifest-declared Boolean checkbox inside the split configuration screen: focus is visible, keyboard and mouse toggle only that field, fixed values cannot change, and returning from its panel retains its typed draft without moving the opposite panel's selection.
- [ ] Run `go test ./internal/tui ./internal/forms -run 'ComponentSelection|AdapterFeature|ManifestUI|Split|Palette' -count=1`; record failures, implement checklist/availability and then verify paste, input acceptance, dirty prompts, Save/Apply, error-result and F10/terminal tests.
- [ ] Report evidence. Controller checkpoint: `feat: choose capability components using adapter support`.

### Task 16: Document pack creation, provide bootstrap examples, and update the reference mock

**Files:** Create `docs/capability-packs.md`, `examples/capability-pack/aact.toml`, `bootstrap.sh`, `bootstrap.ps1`, example profile/capability/template files, and `tests/integration/pack_example_test.go`, `pack_bootstrap_test.go`; update `README.md`, `docs/demo/model.mjs`, `app.mjs`, `README.md`, and `test/demo-model.test.mjs`, `demo-interaction.test.mjs`, `demo-page.test.mjs`, `demo-render.test.mjs` in AACT.

**Interfaces:** Use the final catalog/profile/component schema. Bootstrap is explicitly run by the pack maintainer/user; AACT does not invoke it. The demo is a labeled interactive reference, never a substitute for real TUI evidence.

- [ ] Test a neutral documentation example loads as a pack, imports a locally initialized catalog, discovers profiles, resolves declared inputs/relationships and supports a multi-skill capability. Tests remain in AACT; do not create tests in a consuming pack repo.
- [ ] Test POSIX/PowerShell setup starting points initialize pinned Git submodules, preserve paths containing spaces, repeat safely, and surface a real Git failure. Use local Git fixtures, allowing file protocol only in the fixture environment; never clone live dependencies for tests.
- [ ] Update the interactive mock's profile list, creation labels, status, component Select all/deselection, coupled items, Boolean checkbox interaction and unsupported plugin state. Its keyboard interaction must mirror Tasks 14–15; preserve the existing visual layout/layer palettes.
- [ ] Run `go test ./tests/integration -run 'CapabilityPackExample|PackBootstrap' -count=1` and `node --test test/demo-*.test.mjs`. Record deliberate red tests, then implementation/green results. PowerShell/native checks unavailable locally are reported explicitly and covered by native CI; Node remains a demo-test dependency only.
- [ ] Document creator and consumer directory trees, imported catalog filters/overrides, inputs/default/fixed/regex (including a Boolean checkbox example with `default = false`), multi-skill templates/generators, linked sets, plugin formats, manual bootstrap, state ownership, local-created profiles and non-interactive usage. Update the README installer version line only if required by an existing release task; this plan does not create a release.
- [ ] Report evidence. Controller checkpoint: `docs: explain capability packs and reusable catalogs`.

### Task 17: Verify integrated behavior, packaging, platforms, and the whole branch

**Files:** Create `tests/integration/capability_profiles_test.go`, `capability_components_test.go`, `agent_features_test.go`; extend `tests/packages/public_test.go` and native CI only where existing jobs miss a new test; update `docs/implementation-evidence.md`. Save generated capture evidence in this plan's scratch workspace, not alongside unrelated untracked proposal assets.

**Interfaces:** Exercise packaged CLI/app contracts, real generic child commands, isolated adapter fixtures, and a harmless test-owned Docker runtime. No live install/migration is part of this task.

- [ ] Write end-to-end tests: import neutral third-party catalog → discover two pack profiles → enforce fixed/default/regex → stage two skills → apply linked MCP + skills via adapter → observe statuses → deselect/remove only owned effects. Include source-only and local-created profiles, an unsupported plugin, a native plugin fixture where available, and actual child stderr failure.
- [ ] Run the new focused integration tests, record intended failures, finish only the missing cross-task wiring through the implementer, and verify green. Inspect the first failure before repair; do not broaden/repeat passing suites without a new concern.
- [ ] Run `go test ./... -count=1`, `go vet ./...`, and appropriate existing integration/package tests. Run race checks over the changed coordination/state/adapter/runtime packages when supported. Check the AACT dependency graph excludes package helpers and core app/UI/CLI has no agent-ID-specific behavior.
- [ ] Build the branch binary and all standalone helpers using the scratch-only commands below, then verify archive/resource contents with `go test ./tests/integration -run 'DeclaredResources|ArchiveContracts' -count=1`. Run from the existing AACT checkout; `AACT_PLAN_WORKSPACE` is the absolute directory returned by the execution preflight. The copied GoReleaser config overrides its [documented top-level dist setting](https://goreleaser.com/customization/general/dist/); it retains all six OS/arch targets and the declared helper build/resource lists. Native macOS/Linux/Windows CI remains the platform gate; record local/native/injected/mock evidence and any CI work still awaiting an authorized push.

  ```sh
  mkdir -p "$AACT_PLAN_WORKSPACE/bin"
  go build -o "$AACT_PLAN_WORKSPACE/bin/aact" ./cmd/aact
  cp .goreleaser.yaml "$AACT_PLAN_WORKSPACE/goreleaser.snapshot.yaml"
  printf '\ndist: "%s"\n' "$AACT_PLAN_WORKSPACE/dist" >> "$AACT_PLAN_WORKSPACE/goreleaser.snapshot.yaml"
  goreleaser release --snapshot --config "$AACT_PLAN_WORKSPACE/goreleaser.snapshot.yaml"
  ```

  Do not publish, tag or use `--clean`. On a repeat verification attempt, choose a fresh task-owned dist path instead of cleaning another run's artifacts. If the checked-in config has acquired a `dist` key, replace that key only in the scratch copy rather than adding a duplicate.
- [ ] Exercise the real branch TUI in an existing user-designated tmux pane against isolated pack/state/agent fixtures. Traverse home profiles, creation, components, Boolean/text/file/directory inputs, the existing in-TUI picker, paste/regex, agents, supported/unsupported plugins, progress, successful/failed results, logs, settings/help, resizing/scrolling and exit. Capture actual terminal output/screenshots and compare to the updated reference. Do not create another tmux session or run a live company/home profile.
- [ ] Dispatch the final whole-branch review with the merge-base..HEAD package, spec, plan and ledger; use Luna unless the user approves escalation. Consolidate final findings into one worker fix wave, then one scoped re-review. Report all rulings, open findings, exact evidence and native verification gaps; leave the branch for user review with no push/merge/release.
- [ ] Luna worker checkpoint before independent task and whole-branch reviews: `test: verify capability profiles and adapter features`. All subsequent fixes require their own local checkpoint and scoped review before the final evidence report.

## Requirement coverage and plan self-review

| Discussion requirement | Tasks |
|---|---|
| Pack defines intended environment; remove environment/target selection | 3, 4, 11, 13, 14 |
| Profiles immediately shown with actual configuration/skill/MCP/runtime status | 4, 12, 14, 17 |
| Profile values/policies, relative paths, local override reset and read-only TOMLs | 2, 4, 11, 13 |
| Create another profile retains no-prefill setup | 4, 11, 13, 14 |
| Optional regex accepted consistently, rejected value clearly shown | 2, 11, 13, 15, 17 |
| Typed Boolean inputs with keyboard/mouse checkboxes and default/fixed/reset behavior | 2, 4, 13, 15, 16, 17 |
| No capability-specific behavior or embedded inspector helper in AACT | 1, 10, 11, 17 |
| Local/bundled entries; external catalogs initialized outside AACT/imported locally | 3, 16, 17 |
| Company pack owns profiles; configured names/profile directories resolve collisions | 3, 4, 12, 16 |
| Multiple skills and MCPs; all skills selected, Select all, individual/linked deselection | 1, 5, 11, 15, 17 |
| Agent adapters own detection/config/skills/MCP/plugins; optional unsupported features | 6, 7, 8, 9, 11, 12, 15 |
| Generic adapter is shared `.agents/skills` only | 7, 11, 15 |
| Existing JSON/hashed state storage preserved | 4, 5, 11, 12 |
| Capability Pack/Catalog/Profile terminology in UI/CLI/docs/mock | 13, 14, 16 |
| Creator docs and shell/PowerShell setup starting points | 16 |
| All AACT behavior tests in AACT; TDD; native and real TUI evidence | Every task, 17 |
| User branch; no new worktrees; Luna; controller-only coordination | Global constraints and execution protocol |

Self-review checks before handing off: every requirement has an implementing task; dependencies produce the signatures their consumers use; old state/CLI/helper compatibility is explicit; optional features have tests for unavailable paths; review-focus cases each have a named owning task; no product code or deployment change is hidden in documentation work. Execution preflight still produces the skill's detailed shared-file/interface conflict table in its ledger.
