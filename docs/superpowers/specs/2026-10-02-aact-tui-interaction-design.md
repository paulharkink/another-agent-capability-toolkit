# AACT terminal interaction design

Date: 2026-10-02  
Status: conversational interaction design approved contingent on a matching TUI; written specification awaiting review

## Intent and scope

Replace the current single-view TUI with the Norton Commander style interaction reviewed in the browser mockup. The deliverable is a real Go terminal UI, backed by AACT's application services. The browser mockup is an interaction prototype with illustrative data; it is not a runtime dependency or evidence that its simulated operations work.

The user owns the architecture. This specification records the interaction they approved and distinguishes still-open server and state semantics. Approval of this TUI layout does not silently approve the unresolved Docker and profile-lifecycle proposals listed below.

The acceptance target is the mockup's **layout, navigation, visibility, form behavior, and truthful state presentation** within terminal constraints. Terminal fonts, glyph widths, color depth, and function-key delivery vary; exact browser pixels are not a feasible cross-terminal contract. Every action must remain reachable through visible menus and ordinary keys when a terminal intercepts a function key.

## Home screen

```text
╔═ AACT ════════════════════════════════════════════════════════════════════╗
║ Main menu [F9]: Agents | Environments | Settings | Help                    ║
║ Checkout: company-tools                 Managing: WSL — Ubuntu            ║
╠═ Capabilities ══════════════════════╦═ MCP profiles · Cluster Inspector ╣
║►[ ] Cluster Inspector  skill + MCP   ║ Company / production  Running     ║
║ [ ] Grafana Inspector  skill + MCP   ║ Company / staging     Stopped     ║
║ [ ] Git repo mapper    skill         ║ Windows attachment   Running     ║
║                                   1/3║                                1/3 ║
╠══════════════════════════════════════╩════════════════════════════════════╣
║ Cluster Inspector · skill + MCP · 3 related MCP profiles                  ║
╠═══════════════════════════════════════════════════════════════════════════╣
║ Tab/←→ Panes  F1 Help  F2 Actions  F3 Details  F4 Parameters  F5 Refresh  ║
║ F9 Main menu                                                F10 Quit       ║
╚═══════════════════════════════════════════════════════════════════════════╝
```

The left pane lists capabilities from the current checkout/catalog. The right pane lists **only MCP profiles belonging to the highlighted capability**. Changing the left selection refreshes that related list and selects its first row without disturbing the left selection or scroll position. Profile identity includes source, capability, environment, target, and local profile name where applicable; labels alone must not merge unrelated profiles.

A skill-only capability shows `No MCP profiles — skill-only capability`. A capability with an MCP component but no configured profile shows `No MCP profiles yet — configure/install to create one`. The right pane cannot receive focus while empty. Only Capabilities supports batch marks in this version. The selected profile action never implicitly acts on every visible profile.

Each pane owns a stable selected item ID and scroll offset. Resizing, refreshing, applying an operation, or returning from a modal keeps the originating item selected where it still exists. If it has disappeared, choose the nearest surviving row and explain why. Long lists scroll within their pane and show position/count. The detail strip shows the full selected identity when columns truncate it.

Profile rows represent configured and saved profiles, including never-started ones. The displayed runtime status, authentication status, connection observation, and pending saved changes remain separate facts. A failed Docker refresh displays an error and the timestamp of the last observation; it must not invent `Stopped` or erase configured profiles.

## Navigation and menu contract

| Context | Keys and visible controls | Result |
| --- | --- | --- |
| Home | `Tab`, `←`, `→` | Switch between the two panes; attempting an empty MCP pane leaves focus on Capabilities with an explanation. |
| Focused list | `↑`, `↓`, `PgUp`, `PgDn`, `Home`, `End` | Move its selection and scroll its own viewport. |
| Capability list | `Space` | Toggle that capability's pending batch mark. |
| Any selected row | `Enter`, `F2`, visible Actions control | Open that row's item-specific Actions menu. |
| Home | `F9`, visible Main menu control | Open Agents, Environments, Settings, Help. This does not switch home panes. |
| Home | `F3`, `F4`, `F5` | Open Details, open Parameters/Install, or refresh the focused item/context. |
| Menus | `↑`, `↓`, `Enter`, `Esc` | Move through actions, activate enabled action, or return to the exact origin. |
| Dialogs | `Esc` | Close, or ask whether to discard unsaved edits when a form is dirty. |
| Home | `F10`, visible Quit control | Exit. Input forms do not have a global letter-key quit shortcut. |

Menus show unavailable actions in place with concise reasons. They do not silently omit a command because the selected profile is stopped, attached, unauthenticated, or read-only. The action menu is a centered overlay; its first enabled item receives focus. Closing it restores the selected row and pane. All popup controls have visible focus, and modal focus remains within the popup until it closes.

The Main menu has exactly the management destinations `Agents`, `Environments`, `Settings`, and `Help` for this design. They are management screens, not additional Tab destinations. `F1` opens contextual Help; Main menu → Help opens the help index. Function-key labels remain visible, and every command has a clickable/keyboard menu path.

## Capability install and parameter forms

Installing a capability opens one scrollable form containing every declared input and the destination controls. The bottom action row remains visible while fields scroll. Environment values marked fixed are visible and non-editable with their source file; defaults are editable with their provenance. Fields outside the chosen conditional branch remain visible but disabled with a reason. For Cluster Inspector, `Token` and `Source kubeconfig` are mutually exclusive; the latter is clearly described as an import source, not a live runtime path.

Skill-only installation initially selects `All — ~/.agents/skills`, and may also offer named agent destinations. When the selected installation requires an MCP registration, `All` is absent and at least one named agent is required. Destination paths and the planned effect appear before applying. Each directory picker adds one path; a multiple-value field provides its own Add/Edit/Remove controls.

Keyboard behavior in forms:

- `↑`/`↓` moves focus among fields, checkboxes, selectors, and action buttons. `Tab`/`Shift+Tab` also traverses them.
- `←`/`→` changes a discrete selector choice when it has focus. Inside a text field, those keys move the cursor instead. An opened picker/list uses its own documented navigation.
- `Space` toggles a checkbox; `Enter` activates the focused button or selected menu entry; `Esc` closes or initiates dirty-form discard handling.
- Form errors stay in the same form, focus the first invalid input, and preserve the draft and scroll position. Loading dynamic choices updates the same form instead of reopening it.

Parameters for an existing managed profile use the same field model. The browser mockup depicts separate `Save` and `Apply and restart…` actions and a `Changes pending` state. Those **runtime semantics remain subject to the profile-lifecycle decision**; the TUI must not fake an apply action before its service contract exists. An attached profile displays owner-controlled server fields read-only and permits only applicable local connection/registration fields.

## MCP profile actions and dialogs

The profile Actions menu includes Start/Restart, Stop, Authenticate, Edit parameters, Configure registrations, Remove registrations, Check connection, View logs, View details, and Back. Labels and enabled states depend on the selected profile. With saved but unapplied changes, Restart must distinguish current configuration from applying the saved configuration if that lifecycle model is approved.

Registration dialogs show the endpoint and **named agents only**. Configure presents desired final registration state; Remove opens with no removals selected. Agent detection failures and foreign-name conflicts are visible. Applying reports each agent's result individually rather than declaring a partial batch successful. The exact edit and rollback work belongs to compiled Go agent adapters, outside the TUI model.

Details is a scrollable read-only account of source, environment, target, value origins, desired/applied revision, binding, runtime observations, and local registrations. Check connection is explicit and reports its observation and time. Logs has follow/pause/scroll controls and closing it does not stop the server. Exact agent config viewing shows the entire file **without masking**, as the user specified.

Mutating operations show a foreground progress dialog with steps, output, and per-target results. The UI continues handling resize and input while work runs. Cancellation requests recovery and reports completed, restored, failed, and untouched steps rather than claiming every effect vanished.

## Management screens

**Agents:** A list of native-environment agent adapters and their detection state, with evidence/config location and current AACT registrations for the selected agent. Actions provide exact config-file viewing, location configuration, refresh detection, and Back. Config presence is not used as a proxy for installation. Adapter behavior and format preservation are specified separately in `2026-10-02-aact-agent-adapters-design.md`.

**Environments:** A two-pane browser: environment list left, configured targets right. `No environment file` is a first-class row; it exposes all public inputs during setup. Actions are Use for new setups, View target, Environment root, and Close. Selecting an environment filters the target list without retargeting existing profiles. View target shows exact TOML and resolved values/paths. Invalid TOML appears as an error row, leaving other targets accessible. Repository TOML authoring and environment-root precedence remain open product decisions.

**Settings:** Default *named agents* for future MCP installations; Docker backend selection and resolved backend; catalog/known checkout/state/version/platform diagnostics. The skill-only `All` destination remains distinct. Saving preferences does not rewrite existing registrations. Moving state data is not presented as a casual folder picker.

**Help:** Key map and the meaning of status labels, disabled actions, and source/fixed/default values. It has a visible Back control and restores the originating screen.

## TUI implementation boundary

Bubble Tea v2 owns events and asynchronous commands; Lip Gloss v2 renders the terminal layout. Replace the current global `view`/`selected` state with typed home-pane states, management-screen state, a modal stack, and operation state. The TUI receives typed profile/capability view models and typed operation requests from application services. It must not interpret Docker labels, edit agent JSON/JSONC, or overload a string argument with unrelated meanings. Service results carry action availability and disabled reasons.

The UI runs with native Windows, macOS, and Linux builds; WSL is a Linux runtime scope. No mouse is required. A minimum terminal size may be specified after layout review; below it, show the required/current dimensions and allow exit. At and above the minimum, resizing must preserve focused controls and scroll positions.

Implementation follows the user's TDD requirement. Model tests cover pane filtering, keyboard paths, modal focus restoration, long-list scrolling, small terminals, conditional forms, fixed/default values, disabled reasons, refresh failure, and partial operation results. Adapter tests remain separate from TUI tests. A manual terminal review compares the implemented flows against this mockup before claiming a match; tests alone cannot establish the visual result.

## Public interactive demonstration

Before public release, include a standalone static version of the approved interaction mockup in the public repository and link it from README. Publish it via GitHub Pages through the repository's chosen Pages source. The public demonstration uses illustrative data and no credentials, local session key, real agent config, Docker calls, or backend mutation. Keep its navigation aligned with the actual TUI as the implementation changes.

## Decisions outside this interaction approval

The current browser prototype illustrates several choices that still require the user's architecture decision before their real operations are implemented: whether Save is separate from Apply/restart; whether Stop retains a container; whether environment TOML is edited inside AACT; environment-root precedence; Windows/WSL shared-engine attachment and runtime ownership; and whether a new attachment requires a successful connection check. The internal proposal records options and consequences. These are not inferred merely from the user approving the mockup's menu interaction.
