package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

var managementGold = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffe38a"))
var managementSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#081f5b")).Background(lipgloss.Color("#e9f2fb"))

func (m *Model) environmentRoot() string {
	if root := m.settings["environment-root"]; root != "" {
		return root
	}
	return m.settings["environment_root"]
}

type managementState struct {
	Modal            string
	ModalSelected    int
	Focus            Pane
	EnvironmentIndex int
	TargetIndex      int
	HelpOrigin       string
	HelpSelected     int
	Hits             []hitRegion
	ViewerPath       string
	ViewerContent    string
	ViewerOffset     int
	ViewerHorizontal int
	ViewerReturn     string
	SettingsOptions  []string
	SettingsDraft    map[string]bool
	SettingsReason   string
}

type environmentEntry struct {
	Name       string
	Targets    []string
	Paths      []string
	Errors     []string
	TargetDefs []viewmodel.EnvironmentTarget
	NoFile     bool
}

type environmentBrowserBackend interface {
	UIEnvironmentSnapshot(context.Context) (viewmodel.EnvironmentSnapshot, error)
	UIEnvironmentTarget(context.Context, string) (string, error)
}

type defaultAgentsBackend interface {
	UIAgentDefaultOptions(context.Context) ([]string, error)
	UISetDefaultAgents(context.Context, []string) error
}

type environmentTargetMsg struct {
	path, content string
	err           error
}

func isManagementView(view string) bool {
	switch view {
	case "Agents", "Environments", "Settings", "Help":
		return true
	default:
		return false
	}
}

func (m *Model) namedAgents() []string {
	if m.agentManagement != nil {
		rows := make([]string, 0, len(m.agentManagement))
		for _, agent := range m.agentManagement {
			rows = append(rows, agent.ID)
		}
		return rows
	}
	rows := make([]string, 0, len(m.agents))
	for _, id := range m.agents {
		if id != "all" {
			rows = append(rows, id)
		}
	}
	return rows
}

func (m *Model) selectedAgentManagement() (viewmodel.AgentManagementRow, bool) {
	ids := m.namedAgents()
	if len(ids) == 0 {
		return viewmodel.AgentManagementRow{}, false
	}
	id := ids[min(max(0, m.selected), len(ids)-1)]
	for _, row := range m.agentManagement {
		if row.ID == id {
			return row, true
		}
	}
	return viewmodel.AgentManagementRow{}, false
}

func (m *Model) agentRegistrations(id string) []string {
	seen := map[string]bool{}
	if m.profileSnapshot != nil {
		for _, p := range m.profileSnapshot.Profiles {
			for _, registered := range p.RegisteredAgents {
				if registered == id {
					seen[p.Key.Source+" / "+p.Key.Package+" / "+p.Key.Environment+" / "+p.Key.Target] = true
				}
			}
		}
	}
	for _, installed := range m.inventory {
		if installed.Component == "mcp" && installed.AgentID == id {
			seen[installed.Key.Source+" / "+installed.Key.Package+" / "+installed.Key.Environment+" / "+installed.Key.Target] = true
		}
	}
	rows := make([]string, 0, len(seen))
	for row := range seen {
		rows = append(rows, row)
	}
	sort.Strings(rows)
	return rows
}

// Actual target files and saved profiles have separate rows. A saved profile
// never counts as evidence that its target TOML still exists.
func (m *Model) environmentEntries() []environmentEntry {
	entries := []environmentEntry{{Name: "No environment file", NoFile: true}}
	actual := map[string]*environmentEntry{}
	if m.environmentSnapshot != nil {
		for _, name := range m.environmentSnapshot.Environments {
			key := m.environmentSnapshot.SourceID + " / " + name
			actual[key] = &environmentEntry{Name: key + " · TOML files"}
		}
		for _, target := range m.environmentSnapshot.Targets {
			key := target.SourceID + " / " + target.Environment
			entry := actual[key]
			if entry == nil {
				entry = &environmentEntry{Name: key + " · TOML files"}
				actual[key] = entry
			}
			label := target.PackageID + " / " + target.Name
			if target.Error != "" {
				label += " · Invalid TOML"
			}
			entry.Targets = append(entry.Targets, label)
			entry.Paths = append(entry.Paths, target.Path)
			entry.Errors = append(entry.Errors, target.Error)
			entry.TargetDefs = append(entry.TargetDefs, target)
		}
	}
	actualNames := make([]string, 0, len(actual))
	for name := range actual {
		actualNames = append(actualNames, name)
	}
	sort.Strings(actualNames)
	for _, name := range actualNames {
		entries = append(entries, *actual[name])
	}
	byName := map[string]map[string]bool{}
	if m.profileSnapshot != nil {
		for _, p := range m.profileSnapshot.Profiles {
			if p.Key.Environment == "" {
				continue
			}
			key := p.Key.Source + "\x00" + p.Key.Environment
			if byName[key] == nil {
				byName[key] = map[string]bool{}
			}
			byName[key][p.Key.Package+" / "+p.Key.Target+" · Saved profile"] = true
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		targets := make([]string, 0, len(byName[name]))
		for target := range byName[name] {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		entries = append(entries, environmentEntry{Name: strings.Replace(name, "\x00", " / ", 1) + " · Saved profiles", Targets: targets})
	}
	return entries
}

func (m *Model) managementKey(stroke string) tea.Cmd {
	if stroke == "ctrl+c" || stroke == "f10" {
		return tea.Quit
	}
	if m.management.Modal != "" {
		return m.managementModalKey(stroke)
	}
	if stroke == "esc" {
		if m.view == "Help" && m.management.HelpOrigin != "" {
			m.view = m.management.HelpOrigin
			m.selected = m.management.HelpSelected
			m.management.HelpOrigin = ""
		} else {
			m.navigate("Catalog")
		}
		return nil
	}
	switch stroke {
	case "f1", "?":
		if m.view != "Help" {
			m.navigate("Help")
		}
	case "m", "f9":
		m.management.Modal = "main"
		m.management.ModalSelected = 0
	case "enter":
		if m.view == "Settings" {
			if m.selected == 0 {
				m.editEnvironmentRoot()
				return nil
			}
			if cmd, handled := m.activateSettingsRow(m.selected); handled {
				return cmd
			}
		}
		fallthrough
	case "f2":
		if m.view != "Help" {
			m.management.Modal = "actions"
			m.management.ModalSelected = 0
		}
	case "space", " ":
		if m.view == "Settings" {
			m.toggleSettingsCheckbox(m.selected)
		}
	case "f5", "R":
		if m.view == "Agents" {
			if _, ok := m.backend.(agentManagementBackend); !ok {
				m.output = "Detection refresh unavailable: agent service exposes IDs, not detection evidence."
				return nil
			}
		}
		return m.load()
	case "ctrl+s":
		if m.view == "Settings" {
			return m.saveSettingsDraft()
		}
	case "tab", "shift+tab", "left", "right":
		if m.view == "Settings" && (stroke == "tab" || stroke == "shift+tab") {
			save := len(m.managementSettingsRows()) - 2
			if stroke == "shift+tab" {
				if m.selected == save+1 {
					m.selected = save
				} else if m.selected == save {
					m.selected = 0
				} else {
					m.selected = save + 1
				}
			} else if m.selected < save {
				m.selected = save
			} else if m.selected == save {
				m.selected = save + 1
			} else {
				m.selected = 0
			}
		} else if m.view == "Environments" {
			if stroke == "left" {
				m.management.Focus = CapabilitiesPane
			} else if stroke == "right" || m.management.Focus == CapabilitiesPane {
				m.management.Focus = ProfilesPane
			} else {
				m.management.Focus = CapabilitiesPane
			}
		}
	case "up", "down", "pgup", "pgdown", "home", "end":
		m.moveManagementSelection(stroke)
	}
	return nil
}

func (m *Model) moveManagementSelection(stroke string) {
	if m.view == "Settings" {
		m.moveSettingsSelection(stroke)
		return
	}
	count := 0
	index := &m.selected
	switch m.view {
	case "Agents":
		count = len(m.namedAgents())
	case "Environments":
		entries := m.environmentEntries()
		m.management.EnvironmentIndex = min(max(0, m.management.EnvironmentIndex), len(entries)-1)
		if m.management.Focus == CapabilitiesPane {
			count = len(entries)
			index = &m.management.EnvironmentIndex
		} else {
			count = len(entries[m.management.EnvironmentIndex].Targets)
			index = &m.management.TargetIndex
		}
	case "Settings":
		count = len(m.managementSettingsRows())
	case "Help":
		count = len(m.managementHelpRows())
	}
	if count == 0 {
		*index = 0
		return
	}
	switch stroke {
	case "up":
		*index--
	case "down":
		*index++
	case "pgup":
		*index -= max(1, m.height-11)
	case "pgdown":
		*index += max(1, m.height-11)
	case "home":
		*index = 0
	case "end":
		*index = count - 1
	}
	*index = min(max(0, *index), count-1)
	if m.view == "Environments" && index == &m.management.EnvironmentIndex {
		m.management.TargetIndex = 0
	}
}

func (m *Model) settingsSelectable(index int) bool {
	option := index - 2
	if index == 0 || (option >= 0 && option < len(m.management.SettingsOptions)) {
		return true
	}
	return index >= len(m.managementSettingsRows())-2
}

func (m *Model) moveSettingsSelection(stroke string) {
	rows := m.managementSettingsRows()
	choices := make([]int, 0, len(m.management.SettingsOptions)+3)
	for i := range rows {
		if m.settingsSelectable(i) {
			choices = append(choices, i)
		}
	}
	position := 0
	for i, index := range choices {
		if index >= m.selected {
			position = i
			break
		}
		position = i
	}
	switch stroke {
	case "up":
		position--
	case "down":
		position++
	case "pgup":
		position -= max(1, m.height-11)
	case "pgdown":
		position += max(1, m.height-11)
	case "home":
		position = 0
	case "end":
		position = len(choices) - 1
	}
	m.selected = choices[min(max(0, position), len(choices)-1)]
}

func (m *Model) managementMenuEntries() []string {
	if m.management.Modal == "main" {
		return []string{"Agents", "Environments", "Settings", "Help", "Back"}
	}
	if m.management.Modal == "files" {
		row, ok := m.selectedAgentManagement()
		if !ok {
			return []string{"Back"}
		}
		entries := make([]string, 0, len(row.ConfigFiles)+1)
		for _, file := range row.ConfigFiles {
			entry := file.Path + " · " + file.Scope
			if !file.Exists {
				entry += " — disabled: file does not exist"
			}
			entries = append(entries, entry)
		}
		return append(entries, "Back")
	}
	switch m.view {
	case "Agents":
		viewer := "View configuration files — disabled: exact files unavailable from service"
		refresh := "Refresh detection — disabled: detection evidence unavailable from service"
		if row, ok := m.selectedAgentManagement(); ok {
			for _, file := range row.ConfigFiles {
				if file.Exists {
					viewer = "View configuration files"
					break
				}
			}
			refresh = "Refresh detection"
		}
		return []string{viewer, "Configure location — disabled: service support pending", refresh, "Back"}
	case "Environments":
		viewer := "View target — disabled: select an actual TOML target"
		entries := m.environmentEntries()
		selected := entries[min(max(0, m.management.EnvironmentIndex), len(entries)-1)]
		setup := "Configure / install selected target…"
		if selected.NoFile {
			setup = "Configure / install selected capability…"
		}
		if reason := m.environmentSetupReason(selected); reason != "" {
			setup += " — disabled: " + reason
		}
		if m.management.TargetIndex < len(selected.Paths) && selected.Paths[m.management.TargetIndex] != "" {
			if _, ok := m.backend.(environmentBrowserBackend); ok {
				viewer = "View target"
			}
		}
		return []string{setup, viewer, "Environment root", "Close"}
	case "Settings":
		defaults := "Default named agents — disabled: service support pending"
		if _, ok := m.backend.(defaultAgentsBackend); ok {
			defaults = "Default named agents"
		}
		return []string{"Edit environment root", defaults, "Docker backend — disabled: service support pending", "Back"}
	}
	return nil
}

func (m *Model) environmentSetupReason(selected environmentEntry) string {
	if _, ok := m.backend.(setupBackend); !ok {
		return "setup service unavailable"
	}
	if selected.NoFile {
		capability, ok := m.selectedCapability()
		if !ok || capability.CatalogIndex < 0 {
			return "select an available capability on the home screen"
		}
		return ""
	}
	if m.management.TargetIndex >= len(selected.TargetDefs) {
		return "select an actual TOML target"
	}
	target := selected.TargetDefs[m.management.TargetIndex]
	if target.Error != "" || target.Path == "" {
		return "selected TOML target is invalid"
	}
	for _, capability := range m.capabilities() {
		if capability.Source == target.SourceID && capability.Package == target.PackageID && capability.CatalogIndex >= 0 {
			return ""
		}
	}
	return "target capability is not available in this checkout"
}

func (m *Model) managementModalKey(stroke string) tea.Cmd {
	if m.management.Modal == "viewer" {
		if stroke == "esc" {
			m.management.Modal = m.management.ViewerReturn
			return nil
		}
		lines := strings.Split(m.management.ViewerContent, "\n")
		visible := max(1, m.height-7)
		switch stroke {
		case "up":
			m.management.ViewerOffset--
		case "down":
			m.management.ViewerOffset++
		case "pgup":
			m.management.ViewerOffset -= visible
		case "pgdown":
			m.management.ViewerOffset += visible
		case "home":
			m.management.ViewerOffset = 0
		case "end":
			m.management.ViewerOffset = len(lines) - visible
		case "left":
			m.management.ViewerHorizontal = max(0, m.management.ViewerHorizontal-8)
		case "right":
			m.management.ViewerHorizontal += 8
		}
		m.management.ViewerOffset = min(max(0, m.management.ViewerOffset), max(0, len(lines)-visible))
		return nil
	}
	entries := m.managementMenuEntries()
	if stroke == "esc" {
		if m.management.Modal == "files" {
			m.management.Modal = "actions"
		} else {
			m.management.Modal = ""
		}
		return nil
	}
	switch stroke {
	case "up":
		m.management.ModalSelected = (m.management.ModalSelected + len(entries) - 1) % len(entries)
	case "down":
		m.management.ModalSelected = (m.management.ModalSelected + 1) % len(entries)
	case "home":
		m.management.ModalSelected = 0
	case "end":
		m.management.ModalSelected = len(entries) - 1
	case "enter":
		i := m.management.ModalSelected
		if i < 0 || i >= len(entries) {
			return nil
		}
		if strings.Contains(entries[i], "disabled:") {
			m.output = strings.TrimSpace(strings.SplitN(entries[i], "disabled:", 2)[1])
			return nil
		}
		if m.management.Modal == "main" {
			m.management.Modal = ""
			if entries[i] != "Back" && entries[i] != m.view {
				m.navigate(entries[i])
			}
			return nil
		}
		if m.management.Modal == "files" {
			row, ok := m.selectedAgentManagement()
			if !ok || i == len(row.ConfigFiles) {
				m.management.Modal = "actions"
				m.management.ModalSelected = 0
				return nil
			}
			file := row.ConfigFiles[i]
			backend, ok := m.backend.(agentManagementBackend)
			if !ok {
				m.output = "Exact configuration viewer unavailable from service"
				return nil
			}
			m.management.ViewerReturn = "files"
			return func() tea.Msg {
				content, err := backend.UIAgentConfig(m.ctx, row.ID, file.Path)
				return agentConfigMsg{path: file.Path, content: content, err: err}
			}
		}
		if m.view == "Agents" {
			switch i {
			case 0:
				m.management.Modal = "files"
				m.management.ModalSelected = 0
				return nil
			case 2:
				m.management.Modal = ""
				return m.load()
			}
		}
		if m.view == "Environments" && i == 1 {
			selected := m.environmentEntries()[m.management.EnvironmentIndex]
			path := selected.Paths[m.management.TargetIndex]
			backend, ok := m.backend.(environmentBrowserBackend)
			if !ok {
				m.output = "Exact target viewer unavailable from service"
				return nil
			}
			m.management.ViewerReturn = "actions"
			return func() tea.Msg {
				content, err := backend.UIEnvironmentTarget(m.ctx, path)
				return environmentTargetMsg{path: path, content: content, err: err}
			}
		}
		if m.view == "Environments" && i == 0 {
			selected := m.environmentEntries()[m.management.EnvironmentIndex]
			m.management.Modal = ""
			if selected.NoFile {
				capability, _ := m.selectedCapability()
				return m.beginSetup(capability.Source, capability.Package, "", "")
			}
			target := selected.TargetDefs[m.management.TargetIndex]
			return m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: target.SourceID, PackageID: target.PackageID, Environment: target.Environment, Target: target.Name}, "Overview")
		}
		if entries[i] == "Environment root" || entries[i] == "Edit environment root" {
			m.management.Modal = ""
			m.editEnvironmentRoot()
			return nil
		}
		if m.view == "Settings" && i == 1 {
			m.management.Modal = ""
			m.editDefaultAgents()
			return nil
		}
		if m.view == "Environments" && i == 3 {
			m.management.Modal = ""
			m.navigate("Catalog")
			return nil
		}
		m.management.Modal = ""
	}
	return nil
}

func (m *Model) editEnvironmentRoot() {
	m.pending = operation{action: "set-environment-root"}
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Environment root", Type: "directory", Required: true}}, map[string]any{"root": m.environmentRoot()})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}

func (m *Model) editDefaultAgents() {
	backend, ok := m.backend.(defaultAgentsBackend)
	if !ok {
		m.output = "Default named agent service unavailable"
		return
	}
	ids, err := backend.UIAgentDefaultOptions(m.ctx)
	if err != nil {
		m.output = m.cleanOutput(err.Error())
		return
	}
	choices := make([]catalog.Choice, 0, len(ids))
	for _, id := range ids {
		choices = append(choices, catalog.Choice{Value: id, Label: id})
	}
	selected := []string{}
	for _, id := range strings.Split(m.settings["default_agents"], ",") {
		for _, choice := range choices {
			if id == choice.Value {
				selected = append(selected, id)
				break
			}
		}
	}
	m.pendingDefaultAgents = true
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "agents", Label: "Default named agents", Type: "multichoice", Options: choices}}, map[string]any{"agents": selected})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}

func (m *Model) initSettingsDraft() {
	m.management.SettingsOptions = nil
	m.management.SettingsDraft = map[string]bool{}
	m.management.SettingsReason = "service support pending"
	if backend, ok := m.backend.(defaultAgentsBackend); ok {
		options, err := backend.UIAgentDefaultOptions(m.ctx)
		if err != nil {
			m.management.SettingsReason = m.cleanOutput(err.Error())
		} else {
			m.management.SettingsOptions = append([]string(nil), options...)
			m.management.SettingsReason = ""
		}
	} else {
		m.management.SettingsOptions = m.namedAgents()
	}
	for _, id := range strings.Split(m.settings["default_agents"], ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			m.management.SettingsDraft[id] = true
		}
	}
}

func agentDisplayName(id string) string {
	switch strings.ToLower(id) {
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	case "claude", "claude-code":
		return "Claude Code"
	default:
		return id
	}
}

func (m *Model) settingsCheckboxIndex(index int) (string, bool) {
	option := index - 2
	if option < 0 || option >= len(m.management.SettingsOptions) {
		return "", false
	}
	return m.management.SettingsOptions[option], true
}

func (m *Model) toggleSettingsCheckbox(index int) bool {
	id, ok := m.settingsCheckboxIndex(index)
	if !ok {
		return false
	}
	if m.management.SettingsReason != "" {
		m.output = m.management.SettingsReason
		return true
	}
	m.management.SettingsDraft[id] = !m.management.SettingsDraft[id]
	return true
}

func (m *Model) saveSettingsDraft() tea.Cmd {
	backend, ok := m.backend.(defaultAgentsBackend)
	if !ok || m.management.SettingsReason != "" {
		m.output = m.management.SettingsReason
		return nil
	}
	selected := make([]string, 0, len(m.management.SettingsOptions))
	for _, id := range m.management.SettingsOptions {
		if m.management.SettingsDraft[id] {
			selected = append(selected, id)
		}
	}
	m.busy = true
	return func() tea.Msg { return settingsSavedMsg{err: backend.UISetDefaultAgents(m.ctx, selected)} }
}

func (m *Model) activateSettingsRow(index int) (tea.Cmd, bool) {
	if m.toggleSettingsCheckbox(index) {
		return nil, true
	}
	rows := m.managementSettingsRows()
	if index >= 0 && index < len(rows) && strings.Contains(rows[index], "disabled:") {
		m.output = strings.TrimSpace(strings.SplitN(rows[index], "disabled:", 2)[1])
		return nil, true
	}
	if index == len(rows)-2 {
		return m.saveSettingsDraft(), true
	}
	if index == len(rows)-1 {
		m.navigate("Catalog")
		return nil, true
	}
	return nil, false
}

func (m *Model) managementSettingsRows() []string {
	rows := []string{
		"Environment root: " + m.environmentRoot(),
		"Default named agents for new MCP installations",
	}
	for _, id := range m.management.SettingsOptions {
		mark := "[ ]"
		if m.management.SettingsDraft[id] {
			mark = "[x]"
		}
		row := "  " + mark + " " + agentDisplayName(id)
		if m.management.SettingsReason != "" {
			row += " — disabled: " + m.management.SettingsReason
		}
		rows = append(rows, row)
	}
	rows = append(rows,
		"Skill-only installations default to All — ~/.agents/skills.",
		"These named-agent defaults do not change existing registrations.",
		"Docker backend: [ Select backend — disabled: backend preference service unavailable ]",
		"Resolved backend: unavailable from service",
		"[ Check backend — disabled: backend check service unavailable ]",
		"Source: "+m.settings["source"],
		"Checkout: "+m.settings["checkout"],
		"Known checkouts: unavailable from service",
		"[ View known checkouts… — disabled: checkout listing service unavailable ]",
		"State: "+m.settings["state-dir"],
		"Platform: "+runtime.GOOS+" / "+runtime.GOARCH,
	)
	save := "[ Save ]"
	if m.management.SettingsReason != "" {
		save += " — disabled: " + m.management.SettingsReason
	}
	return append(rows, save, "[ Cancel ]")
}

func (m *Model) managementHelpRows() []string {
	return []string{
		"Tab / Left / Right: switch two-pane focus",
		"Up / Down / PageUp / PageDown / Home / End: select and scroll",
		"Enter / F2: selected row Actions; Up / Down also work in menus",
		"F1: Help · F5: Refresh · F9 / m: Main menu · F10: Quit",
		"Mouse click: select row or visible command · wheel: scroll",
		"Running / Stopped: observed Docker state; a stale observation has a timestamp",
		"Other installation: running elsewhere; local registration may still be configured",
		"Disabled action: its reason is shown in the menu",
		"default: suggested editable value; environment values may prefill it",
		"source: the package or target file that supplied a value",
		"Esc / Back: return to the previous screen",
	}
}

func (m *Model) managementView() tea.View {
	m.management.Hits = nil
	width, height := m.width, m.height
	if width < 80 || height < 16 {
		return m.homeView()
	}
	if m.management.Modal == "viewer" {
		return m.agentConfigView()
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = "║" + fit("", width-2) + "║"
	}
	shortPath := func(path string) string {
		if path == "" {
			return "none"
		}
		return filepath.Base(path)
	}
	menubar := " F9 Main menu: Agents | Environments | Settings | Help   F2 Actions"
	scope := " Checkout: " + shortPath(m.settings["checkout"]) + " · Managing: " + runtime.GOOS + "/" + runtime.GOARCH + " · Source: " + m.settings["source"] + " · Env: " + shortPath(m.environmentRoot())
	lines[0] = "╔" + managementGold.Render(fit(" AACT · Another Agent Capability Toolkit", width-2)) + "╗"
	lines[1] = "║" + fit(menubar, width-2) + "║"
	lines[2] = "║" + fit(scope, width-2) + "║"
	lines[3] = "╠" + strings.Repeat("═", width-2) + "╣"
	actionsStart := strings.Index(menubar, "F2 Actions")
	m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: 1, Width: actionsStart, Height: 1, Control: "main"}, hitRegion{X: actionsStart + 1, Y: 1, Width: len("F2 Actions"), Height: 1, Control: "actions"})
	visible := height - 12
	if m.view == "Agents" {
		m.renderAgentManagement(lines, visible)
	} else if m.view == "Environments" {
		m.renderEnvironmentManagement(lines, visible-1)
	} else if m.view == "Settings" {
		m.renderSettingsManagement(lines, visible)
	} else {
		rows := m.managementSettingsRows()
		if m.view == "Help" {
			rows = m.managementHelpRows()
		}
		m.renderManagementList(lines, rows, visible)
	}
	lines[height-6] = "╠" + strings.Repeat("═", width-2) + "╣"
	lines[height-5] = "║" + fit(" "+m.output, width-2) + "║"
	lines[height-4] = "╠" + strings.Repeat("═", width-2) + "╣"
	footer := []struct{ label, control string }{{"F1 Help", "help"}, {"F2 Actions", "actions"}, {"F5 Refresh", "refresh"}, {"F9 Main menu", "main"}, {"F10 Quit", "quit"}}
	x, content := 2, " "
	for _, item := range footer {
		content += item.label + "  "
		m.management.Hits = append(m.management.Hits, hitRegion{X: x, Y: height - 3, Width: len(item.label), Height: 1, Control: item.control})
		x += len(item.label) + 2
	}
	lines[height-3] = "║" + fit(content, width-2) + "║"
	lines[height-2] = "║" + fit(" Esc Back · ↑↓ Select · Enter Actions", width-2) + "║"
	m.management.Hits = append(m.management.Hits, hitRegion{X: 2, Y: height - 2, Width: len("Esc Back"), Height: 1, Control: "back"})
	lines[height-1] = "╚" + strings.Repeat("═", width-2) + "╝"
	if m.management.Modal != "" {
		m.renderManagementModal(lines)
	}
	v := tea.NewView(navyCanvas(strings.Join(lines, "\n")))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) renderManagementList(lines []string, rows []string, visible int) {
	width := m.width
	start := max(0, m.selected-visible+1)
	if m.selected < visible {
		start = 0
	}
	start = min(start, max(0, len(rows)-visible))
	lines[4] = "║" + managementGold.Render(managementScrollTitle(" "+m.view, len(rows), start, visible, width-2)) + "║"
	for y := 0; y < visible; y++ {
		i := start + y
		if i >= len(rows) {
			break
		}
		selectable := m.view != "Help" && (m.view != "Settings" || m.settingsSelectable(i))
		prefix := "· "
		if selectable {
			prefix = "› "
			if i == m.selected {
				prefix = "> "
			}
		} else if m.view == "Settings" && i == 1 {
			prefix = "── "
		}
		row := fit(prefix+rows[i], width-2)
		if selectable && i == m.selected {
			row = managementSelected.Render(row)
		} else if prefix == "── " {
			row = managementGold.Render(row)
		}
		lines[y+5] = "║" + row + "║"
		if selectable {
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 5, Width: width - 2, Height: 1, Index: i, Control: "row"})
		}
	}
}

func managementScrollCue(count, start, visible int) string {
	cue := ""
	if start > 0 {
		cue += " ↑ More above"
	}
	if count > start+visible {
		cue += " ↓ More below"
	}
	return cue
}

func managementScrollTitle(title string, count, start, visible, width int) string {
	cue := managementScrollCue(count, start, visible)
	return fit(title, max(0, width-ansi.StringWidth(cue))) + cue
}

func (m *Model) renderSettingsManagement(lines []string, visible int) {
	rows := m.managementSettingsRows()
	save := len(rows) - 2
	m.renderManagementList(lines, rows[:save], visible)
	buttons := []struct {
		label string
		index int
	}{{"[ Save ]", save}, {"[ Cancel ]", save + 1}}
	content := " "
	for _, button := range buttons {
		if len(content) > 1 {
			content += "  "
		}
		x := 1 + ansi.StringWidth(content)
		styled := managementGold.Render(button.label)
		if m.selected == button.index {
			styled = managementSelected.Render(button.label)
		}
		content += styled
		m.management.Hits = append(m.management.Hits, hitRegion{X: x, Y: m.height - 7, Width: len(button.label), Height: 1, Index: button.index, Control: "row"})
	}
	lines[m.height-7] = "║" + fit(content, m.width-2) + "║"
}

func managementPaneWidths(width int) (int, int) {
	left := (width - 3) * 4 / 9
	return left, width - 3 - left
}

func (m *Model) renderAgentManagement(lines []string, visible int) {
	rows := m.namedAgents()
	left, right := managementPaneWidths(m.width)
	selectedName := "Agent details"
	selectedID := ""
	if len(rows) > 0 {
		selectedID = rows[min(max(0, m.selected), len(rows)-1)]
		selectedName = selectedID
		if row, ok := m.selectedAgentManagement(); ok && row.Name != "" {
			selectedName = row.Name
		}
	}
	start := max(0, m.selected-visible+1)
	if m.selected < visible {
		start = 0
	}
	start = min(start, max(0, len(rows)-visible))
	leftTitle := managementScrollTitle(" Detected agents", len(rows), start, visible, left)
	lines[4] = "╠" + managementGold.Render(leftTitle) + "╦" + managementGold.Render(fit(" "+selectedName, right)) + "╣"
	registrations := m.agentRegistrations(selectedID)
	details := []string{"Status: Detection unavailable", "Config: location unavailable", fmt.Sprintf("AACT MCPs: %d", len(registrations))}
	if row, ok := m.selectedAgentManagement(); ok {
		registrations = row.Registrations
		status := row.Detection
		if status == "" {
			status = "Detection unavailable"
		}
		config := "location unavailable"
		if len(row.ConfigFiles) > 0 {
			file := row.ConfigFiles[0]
			config = file.Path
			if !file.Exists {
				config += " (missing)"
			}
		}
		details = []string{"Status: " + status, "Config: " + config, fmt.Sprintf("AACT MCPs: %d", len(registrations)), ""}
		if row.Evidence != "" {
			details = append(details, "Evidence: "+row.Evidence)
		}
		if row.Note != "" {
			details = append(details, "Note: "+row.Note)
		}
		details = append(details, fmt.Sprintf("Config candidates: %d", len(row.ConfigFiles)))
		for _, file := range row.ConfigFiles {
			state := "missing"
			if file.Exists {
				state = "exists"
			}
			details = append(details, file.Path+" · "+state+" · "+file.Scope+" · "+file.Precedence)
		}
	}
	if len(registrations) > 0 {
		details = append(details, "Registrations:")
		details = append(details, registrations...)
	}
	for y := 0; y < visible; y++ {
		i := start + y
		leftText := ""
		if i < len(rows) {
			prefix := "› "
			if i == m.selected {
				prefix = "> "
			}
			leftText = prefix + rows[i]
			for _, row := range m.agentManagement {
				if row.ID == rows[i] && row.Name != "" {
					leftText = prefix + row.Name + " (" + row.ID + ")"
					break
				}
			}
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 5, Width: left, Height: 1, Index: i, Control: "row"})
		}
		rightText := ""
		if len(rows) > 0 && y < len(details) {
			rightText = details[y]
		}
		leftCell := fit(leftText, left)
		if i < len(rows) && i == m.selected {
			leftCell = managementSelected.Render(leftCell)
		}
		lines[y+5] = "║" + leftCell + "║" + fit(rightText, right) + "║"
	}
}

func (m *Model) agentConfigView() tea.View {
	width, height := m.width, m.height
	lines := make([]string, height)
	title := "Agent configuration"
	if m.view == "Environments" {
		title = "Environment target"
	}
	lines[0] = "╔" + fit(" "+title+" · "+m.management.ViewerPath, width-2) + "╗"
	lines[1] = "║" + fit(" Exact file contents · ←→ Horizontal · ↑↓/PgUp/PgDn Scroll · Esc Back", width-2) + "║"
	lines[2] = "╠" + strings.Repeat("═", width-2) + "╣"
	content := strings.Split(m.management.ViewerContent, "\n")
	visible := height - 5
	for y := 0; y < visible; y++ {
		text := ""
		i := m.management.ViewerOffset + y
		if i < len(content) {
			line := []rune(content[i])
			if m.management.ViewerHorizontal < len(line) {
				text = string(line[m.management.ViewerHorizontal:])
			}
		}
		lines[y+3] = "║" + fit(text, width-2) + "║"
	}
	lines[height-2] = "║" + fit(fmt.Sprintf(" Line %d/%d · Esc Back", m.management.ViewerOffset+1, len(content)), width-2) + "║"
	lines[height-1] = "╚" + strings.Repeat("═", width-2) + "╝"
	v := tea.NewView(navyCanvas(strings.Join(lines, "\n")))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) renderEnvironmentManagement(lines []string, visible int) {
	entries := m.environmentEntries()
	m.management.EnvironmentIndex = min(max(0, m.management.EnvironmentIndex), len(entries)-1)
	selected := entries[m.management.EnvironmentIndex]
	left, right := managementPaneWidths(m.width)
	leftTitle, rightTitle := " Environment", " Configured targets · "+selected.Name
	if m.management.Focus == CapabilitiesPane {
		leftTitle = "►" + leftTitle
	} else {
		rightTitle = "►" + rightTitle
	}
	leftStart := max(0, m.management.EnvironmentIndex-visible+1)
	rightStart := max(0, m.management.TargetIndex-visible+1)
	leftTitle = managementScrollTitle(leftTitle, len(entries), leftStart, visible, left)
	rightTitle = managementScrollTitle(rightTitle, len(selected.Targets), rightStart, visible, right)
	lines[4] = "╠" + managementGold.Render(leftTitle) + "╦" + managementGold.Render(rightTitle) + "╣"
	for y := 0; y < visible; y++ {
		l, r := "", ""
		i := leftStart + y
		if i < len(entries) {
			prefix := "› "
			if i == m.management.EnvironmentIndex {
				prefix = "> "
			}
			l = prefix + entries[i].Name
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 5, Width: left, Height: 1, Pane: CapabilitiesPane, Index: i, Control: "row"})
		}
		i = rightStart + y
		if i < len(selected.Targets) {
			prefix := "› "
			if i == m.management.TargetIndex {
				prefix = "> "
			}
			r = prefix + selected.Targets[i]
			m.management.Hits = append(m.management.Hits, hitRegion{X: left + 2, Y: y + 5, Width: right, Height: 1, Pane: ProfilesPane, Index: i, Control: "row"})
		} else if y == 0 && len(selected.Targets) == 0 {
			r = "No targets in this selection"
		}
		leftCell, rightCell := fit(l, left), fit(r, right)
		if m.management.Focus == CapabilitiesPane && leftStart+y == m.management.EnvironmentIndex {
			leftCell = managementSelected.Render(leftCell)
		}
		if m.management.Focus == ProfilesPane && rightStart+y == m.management.TargetIndex && rightStart+y < len(selected.Targets) {
			rightCell = managementSelected.Render(rightCell)
		}
		lines[y+5] = "║" + leftCell + "║" + rightCell + "║"
	}
	root := " Environment root: " + m.environmentRoot() + " · saved profiles are separate from TOML files"
	if m.management.TargetIndex < len(selected.Errors) && selected.Errors[m.management.TargetIndex] != "" {
		root = " Invalid TOML: " + selected.Errors[m.management.TargetIndex]
	}
	lines[m.height-8] = "║" + fit(root, m.width-2) + "║"
	labels := []string{"Configure / install", "View target", "Environment root", "Close"}
	inline := " "
	for i, label := range labels {
		if i > 0 {
			inline += "  "
		}
		x := len(inline) + 1
		button := "[ " + label + " ]"
		inline += button
		m.management.Hits = append(m.management.Hits, hitRegion{X: x, Y: m.height - 7, Width: len(button), Height: 1, Index: i, Control: "env-action"})
	}
	lines[m.height-7] = "║" + managementGold.Render(fit(inline, m.width-2)) + "║"
}

func (m *Model) renderManagementModal(lines []string) {
	entries := m.managementMenuEntries()
	w := min(m.width-6, 75)
	x := (m.width - w) / 2
	y := max(4, (m.height-len(entries)-4)/2)
	title := " Actions"
	if m.management.Modal == "main" {
		title = " Main menu"
	}
	box := []string{"┌" + managementGold.Render(fit(title, w-2)) + "┐"}
	for i, entry := range entries {
		prefix := "› "
		if i == m.management.ModalSelected {
			prefix = "> "
		}
		row := fit(prefix+entry, w-2)
		if i == m.management.ModalSelected {
			row = managementSelected.Render(row)
		}
		box = append(box, "│"+row+"│")
		m.management.Hits = append(m.management.Hits, hitRegion{X: x + 1, Y: y + i + 1, Width: w - 2, Height: 1, Index: i, Control: "menu"})
	}
	box = append(box, "│"+fit(" ↑↓ Select · Enter Open · Esc Back", w-2)+"│", "└"+strings.Repeat("─", w-2)+"┘")
	for i, line := range box {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = ansi.Cut(lines[y+i], 0, x) + line + ansi.Cut(lines[y+i], x+w, m.width)
	}
}

func (m *Model) managementMouse(msg tea.MouseMsg) tea.Cmd {
	if m.management.Modal == "viewer" {
		mouse := msg.Mouse()
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			if mouse.Button == tea.MouseWheelUp {
				return m.managementModalKey("up")
			}
			return m.managementModalKey("down")
		}
		if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft && mouse.Y == m.height-2 && mouse.X >= 2 && mouse.X <= 10 {
			return m.managementModalKey("esc")
		}
		return nil
	}
	m.managementView()
	mouse := msg.Mouse()
	for _, hit := range m.management.Hits {
		if !hit.contains(mouse.X, mouse.Y) {
			continue
		}
		if m.management.Modal != "" && hit.Control != "menu" {
			continue
		}
		if _, ok := msg.(tea.MouseWheelMsg); ok && hit.Control == "row" {
			key := "down"
			if mouse.Button == tea.MouseWheelUp {
				key = "up"
			}
			if m.view == "Environments" {
				m.management.Focus = hit.Pane
			}
			m.moveManagementSelection(key)
			return nil
		}
		if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft {
			continue
		}
		switch hit.Control {
		case "menu":
			m.management.ModalSelected = hit.Index
			return m.managementModalKey("enter")
		case "row":
			if m.view == "Environments" {
				m.management.Focus = hit.Pane
				if hit.Pane == CapabilitiesPane {
					m.management.EnvironmentIndex = hit.Index
					m.management.TargetIndex = 0
				} else {
					m.management.TargetIndex = hit.Index
				}
			} else {
				m.selected = hit.Index
				if m.view == "Settings" {
					cmd, _ := m.activateSettingsRow(hit.Index)
					return cmd
				}
			}
			return nil
		case "help":
			return m.managementKey("f1")
		case "actions":
			return m.managementKey("f2")
		case "env-action":
			entries := m.managementMenuEntries()
			if hit.Index >= len(entries) {
				return nil
			}
			if strings.Contains(entries[hit.Index], "disabled:") {
				m.output = strings.TrimSpace(strings.SplitN(entries[hit.Index], "disabled:", 2)[1])
				return nil
			}
			m.management.Modal = "actions"
			m.management.ModalSelected = hit.Index
			return m.managementModalKey("enter")
		case "refresh":
			return m.managementKey("f5")
		case "main":
			return m.managementKey("f9")
		case "quit":
			return tea.Quit
		case "back":
			return m.managementKey("esc")
		}
	}
	return nil
}
