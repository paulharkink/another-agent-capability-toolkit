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
	Modal                string
	ModalSelected        int
	Focus                Pane
	EnvironmentIndex     int
	TargetIndex          int
	AgentDetailIndex     int
	SettingsDetailOffset int
	SettingsDetailIndex  int
	SettingsOptions      []string // retained for source compatibility with legacy test fixtures; no longer drives UI state
	HelpOrigin           string
	HelpSelected         int
	Hits                 []hitRegion
	ViewerPath           string
	ViewerContent        string
	ViewerOffset         int
	ViewerHorizontal     int
	ViewerReturn         string
	FormOverlay          bool
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
		} else if (m.view == "Agents" || m.view == "Settings") && m.management.Focus == ProfilesPane {
			m.management.Focus = CapabilitiesPane
			m.management.AgentDetailIndex = 0
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
		if m.view == "Agents" {
			if m.management.Focus == CapabilitiesPane {
				m.management.Focus = ProfilesPane
				m.management.AgentDetailIndex = m.agentDetailInitialIndex()
				return nil
			}
			return m.activateAgentDetail()
		}
		if m.view == "Settings" {
			if m.management.Focus == CapabilitiesPane {
				m.management.Focus = ProfilesPane
				m.management.SettingsDetailOffset = 0
				m.management.SettingsDetailIndex = 0
				return nil
			}
			return m.activateSettingsDetail()
		}
		fallthrough
	case "f2":
		if m.view != "Help" {
			if m.view == "Agents" && m.management.Focus == CapabilitiesPane {
				m.management.Focus = ProfilesPane
				m.management.AgentDetailIndex = m.agentDetailInitialIndex()
			} else if m.view == "Settings" && m.management.Focus == CapabilitiesPane {
				m.management.Focus = ProfilesPane
				m.management.SettingsDetailOffset = 0
				m.management.SettingsDetailIndex = 0
			} else if m.view == "Agents" {
				return m.activateAgentDetail()
			} else if m.view == "Settings" {
				return m.activateSettingsDetail()
			} else if m.view == "Environments" {
				m.management.Modal = "actions"
				m.management.ModalSelected = 0
			}
		}
	case "space", " ":
		// Category controls are activated explicitly with Enter / F2.
	case "f5", "R":
		if m.view == "Agents" {
			if _, ok := m.backend.(agentManagementBackend); !ok {
				m.output = "Detection refresh unavailable: agent service exposes IDs, not detection evidence."
				return nil
			}
		}
		return m.load()
	case "tab", "shift+tab", "left", "right":
		if m.view == "Agents" || m.view == "Settings" {
			if stroke == "left" || stroke == "shift+tab" {
				m.management.Focus = CapabilitiesPane
			} else {
				m.management.Focus = ProfilesPane
				if m.view == "Agents" {
					m.management.AgentDetailIndex = m.agentDetailInitialIndex()
				}
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
	if m.view == "Agents" && m.management.Focus == ProfilesPane {
		count := m.agentDetailLineCount()
		m.management.AgentDetailIndex = moveBounded(m.management.AgentDetailIndex, count, stroke, max(1, m.height-11))
		return
	}
	if m.view == "Settings" && m.management.Focus == ProfilesPane {
		_, right := managementPaneWidths(m.width)
		details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
		m.management.SettingsDetailIndex = moveBounded(m.management.SettingsDetailIndex, len(details), stroke, max(1, m.height-12))
		m.keepSettingsDetailVisible(len(details))
		return
	}
	if m.view == "Settings" {
		previous := m.selected
		m.selected = moveBounded(m.selected, len(m.managementSettingsRows()), stroke, max(1, m.height-11))
		if m.selected != previous {
			m.management.SettingsDetailOffset = 0
			m.management.SettingsDetailIndex = 0
		}
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
	if m.view == "Agents" && index == &m.selected {
		m.management.AgentDetailIndex = m.agentDetailInitialIndex()
	}
	if m.view == "Environments" && index == &m.management.EnvironmentIndex {
		m.management.TargetIndex = 0
	}
}

func moveBounded(index, count int, stroke string, page int) int {
	if count == 0 {
		return 0
	}
	switch stroke {
	case "up":
		index--
	case "down":
		index++
	case "pgup":
		index -= page
	case "pgdown":
		index += page
	case "home":
		index = 0
	case "end":
		index = count - 1
	}
	return min(max(index, 0), count-1)
}

func (m *Model) agentDetailControlCount() int {
	_, controls, _ := m.agentManagementDisplayLines()
	return len(controls)
}

func (m *Model) agentManagementDisplayLines() (details, controls []string, firstControl int) {
	row, ok := m.selectedAgentManagement()
	ids := m.namedAgents()
	selectedID := ""
	if m.selected >= 0 && m.selected < len(ids) {
		selectedID = ids[m.selected]
	}
	registrations := m.agentRegistrations(selectedID)
	if ok {
		registrations = row.Registrations
		details = agentManagementDetails(row)
	} else {
		details = []string{"Status: Unverified", "Evidence: Detection unavailable", "Active home: Not reported", "Effective config: Not resolved", "Intended write config: Not supported or not resolved", "Config candidates: None reported", "Observed registrations: None reported"}
	}
	if !ok && len(registrations) > 0 {
		details = append(details, "Observed AACT registrations:")
		details = append(details, registrations...)
	}
	if ok && len(row.Registrations) == 0 {
		for _, registration := range m.agentRegistrations(row.ID) {
			details = append(details, "Observed AACT registration: "+registration)
		}
	}
	_, right := managementPaneWidths(max(80, m.width))
	details = wrapManagementDetails(details, right-1)
	firstControl = len(details)
	if ok {
		for i, file := range row.ConfigFiles {
			if file.Exists {
				controls = append(controls, fmt.Sprintf("View exact configuration candidate %d", i+1))
			}
		}
	}
	if len(controls) == 0 {
		details = append(details, wrapManagementDetails([]string{"Configure location: unavailable; this backend has no supported location editor"}, right-1)...)
	} else {
		details = append(details, controls...)
	}
	return details, controls, firstControl
}

func (m *Model) agentDetailLineCount() int {
	details, _, _ := m.agentManagementDisplayLines()
	return len(details)
}

func (m *Model) agentDetailInitialIndex() int {
	_, controls, first := m.agentManagementDisplayLines()
	if len(controls) > 0 {
		return first
	}
	return 0
}

func (m *Model) activateAgentDetail() tea.Cmd {
	row, ok := m.selectedAgentManagement()
	if !ok {
		return nil
	}
	_, controls, firstControl := m.agentManagementDisplayLines()
	index := m.management.AgentDetailIndex - firstControl
	if index < 0 || index >= len(controls) {
		return nil
	}
	candidateIndex := -1
	for i, file := range row.ConfigFiles {
		if !file.Exists {
			continue
		}
		if index == 0 {
			candidateIndex = i
			break
		}
		index--
	}
	if candidateIndex < 0 {
		return nil
	}
	backend, ok := m.backend.(agentManagementBackend)
	if !ok {
		m.output = "Exact configuration viewer unavailable from service"
		return nil
	}
	path := row.ConfigFiles[candidateIndex].Path
	m.management.ViewerReturn = ""
	return func() tea.Msg {
		content, err := backend.UIAgentConfig(m.ctx, row.ID, path)
		return agentConfigMsg{path: path, content: content, err: err}
	}
}

func (m *Model) settingsActionIndex(details []string) (int, bool) {
	if len(details) == 0 {
		return 0, false
	}
	switch m.selected {
	case 0:
		return len(details) - 1, true
	case 1:
		if _, ok := m.backend.(defaultAgentsBackend); ok {
			return len(details) - 1, true
		}
	}
	return 0, false
}

func (m *Model) activateSettingsDetail() tea.Cmd {
	_, right := managementPaneWidths(max(80, m.width))
	details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
	actionIndex, hasAction := m.settingsActionIndex(details)
	visible := max(1, m.height-12)
	m.keepSettingsDetailVisible(len(details))
	if !hasAction || m.management.SettingsDetailIndex != actionIndex ||
		m.management.SettingsDetailIndex < m.management.SettingsDetailOffset ||
		m.management.SettingsDetailIndex >= m.management.SettingsDetailOffset+visible {
		return nil
	}
	return m.activateSettingsCategory()
}

func (m *Model) keepSettingsDetailVisible(detailCount int) {
	visible := max(1, m.height-12)
	maxStart := max(0, detailCount-visible)
	start := min(max(0, m.management.SettingsDetailOffset), maxStart)
	if m.management.SettingsDetailIndex < start {
		start = m.management.SettingsDetailIndex
	} else if m.management.SettingsDetailIndex >= start+visible {
		start = m.management.SettingsDetailIndex - visible + 1
	}
	m.management.SettingsDetailOffset = min(max(0, start), maxStart)
}

func (m *Model) activateSettingsCategory() tea.Cmd {
	switch m.selected {
	case 0:
		m.editEnvironmentRoot()
	case 1:
		if _, ok := m.backend.(defaultAgentsBackend); ok {
			m.editDefaultAgents()
		} else {
			m.output = "Default-agent preference service unavailable; use the CLI settings command."
		}
	default:
		return nil
	}
	return nil
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
	m.management.FormOverlay = true
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Environment root", Type: "directory", Required: true}}, map[string]any{"root": m.environmentRoot()})
	m.form.SetTitle("Environment source · Edit environment root")
	_, _, width, height, _ := managementFormOverlayBounds(m.width, m.height)
	m.form.Update(tea.WindowSizeMsg{Width: width, Height: height})
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
	m.management.FormOverlay = true
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "agents", Label: "Default named agents", Type: "multichoice", Options: choices}}, map[string]any{"agents": selected})
	m.form.SetTitle("Agent defaults · Edit future MCP destinations")
	_, _, width, height, _ := managementFormOverlayBounds(m.width, m.height)
	m.form.Update(tea.WindowSizeMsg{Width: width, Height: height})
}

func managementFormOverlayBounds(width, height int) (x, y, overlayWidth, overlayHeight int, ok bool) {
	if width < 80 || height < 16 {
		return 0, 0, 0, 0, false
	}
	overlayWidth = min(72, width-8)
	overlayHeight = min(20, height-4)
	if overlayWidth < 48 || overlayHeight < 12 {
		return 0, 0, 0, 0, false
	}
	return (width - overlayWidth) / 2, (height - overlayHeight) / 2, overlayWidth, overlayHeight, true
}

func (m *Model) initSettingsDraft() {
	m.management.SettingsDetailOffset = 0
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

func (m *Model) managementSettingsRows() []string {
	return []string{"Environment source", "Agent defaults", "Runtime backend", "Diagnostics"}
}

func (m *Model) managementSettingsDetails() []string {
	switch m.selected {
	case 0:
		return []string{"Environment source", "Source: " + nonempty(m.settings["source"], "not selected"), "Checkout: " + nonempty(m.settings["checkout"], "not reported"), "Environment root: " + nonempty(m.environmentRoot(), "not configured"), "[ Edit environment root… ]"}
	case 1:
		lines := []string{"Agent defaults", "Default named agents affect future MCP installations only.", "Existing registrations are unchanged.", "Skill-only installations use All — ~/.agents/skills."}
		if _, ok := m.backend.(defaultAgentsBackend); ok {
			return append(lines, "[ Edit default named agents… ]")
		}
		return append(lines, "Default-agent preference service unavailable; use the CLI settings command.")
	case 2:
		return []string{"Runtime backend", "Backend selection and health checks are unavailable from this service.", "Use the CLI runtime commands to inspect or change the backend."}
	default:
		return []string{"Diagnostics", "Source: " + nonempty(m.settings["source"], "not reported"), "Checkout: " + nonempty(m.settings["checkout"], "not reported"), "Environment root: " + nonempty(m.environmentRoot(), "not reported"), "State: " + nonempty(m.settings["state-dir"], "not reported"), "Platform: " + runtime.GOOS + " / " + runtime.GOARCH, "Known checkout listing: unavailable from this service"}
	}
}

func (m *Model) managementHelpRows() []string {
	return []string{
		"Agents / Settings: Left / Right or Tab changes focused pane",
		"Up / Down / PageUp / PageDown / Home / End selects and scrolls the focused pane",
		"Enter / F2: Open / focus the highlighted row or control; Environments opens target actions",
		"Text editors: arrows move the cursor · Backspace edits · Esc leaves the editor",
		"Pickers: arrows choose · Enter accepts · Esc returns to the previous layer",
		"Agents: choose an agent before opening a config · Settings: choose a category before its control",
		"F1 / ?: Help · F5 / R: Refresh · F9 / m: Main menu · F10: Quit",
		"Mouse click selects visible rows or controls · wheel scrolls the focused pane",
		"Scroll titles show More above / More below when content continues",
		"Esc / Back returns one layer at a time and Help restores its origin",
	}
}

func (m *Model) managementView() tea.View {
	m.management.Hits = nil
	width, height := m.width, m.height
	if width < 80 || height < 16 {
		return tea.NewView(navyCanvas("AACT · " + m.view + "\nResize terminal to 80×16 or larger.\nEsc Back"))
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
	menubar := " F9 Main menu: Agents | Environments | Settings | Help   F2 Open / Focus"
	scope := " Checkout: " + shortPath(m.settings["checkout"]) + " · Managing: " + runtime.GOOS + "/" + runtime.GOARCH + " · Source: " + m.settings["source"] + " · Env: " + shortPath(m.environmentRoot())
	lines[0] = "╔" + managementGold.Render(fit(" AACT · Another Agent Capability Toolkit", width-2)) + "╗"
	lines[1] = "║" + fit(menubar, width-2) + "║"
	lines[2] = "║" + fit(scope, width-2) + "║"
	lines[3] = "╠" + strings.Repeat("═", width-2) + "╣"
	actionsStart := strings.Index(menubar, "F2 Open / Focus")
	m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: 1, Width: actionsStart, Height: 1, Control: "main"}, hitRegion{X: actionsStart + 1, Y: 1, Width: len("F2 Open / Focus"), Height: 1, Control: "actions"})
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
	footer := []struct{ label, control string }{{"F1 Help", "help"}, {"F2 Open / Focus", "actions"}, {"F5 Refresh", "refresh"}, {"F9 Main menu", "main"}, {"F10 Quit", "quit"}}
	x, content := 2, " "
	for _, item := range footer {
		content += item.label + "  "
		m.management.Hits = append(m.management.Hits, hitRegion{X: x, Y: height - 3, Width: len(item.label), Height: 1, Control: item.control})
		x += len(item.label) + 2
	}
	lines[height-3] = "║" + fit(content, width-2) + "║"
	lines[height-2] = "║" + fit(" Esc Back · ↑↓ Select focused pane · Enter / F2 Open / Focus", width-2) + "║"
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
		selectable := m.view != "Help"
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
	left, right := managementPaneWidths(m.width)
	start := max(0, m.selected-visible+1)
	leftTitle := " Settings categories"
	if m.management.Focus == CapabilitiesPane {
		leftTitle = "►" + leftTitle
	}
	details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
	m.keepSettingsDetailVisible(len(details))
	detailStart := min(m.management.SettingsDetailOffset, max(0, len(details)-visible))
	detailTitle := " Details · " + rows[min(max(0, m.selected), len(rows)-1)]
	if m.management.Focus == ProfilesPane {
		detailTitle = "►" + detailTitle
	}
	lines[4] = "╠" + managementGold.Render(managementScrollTitle(leftTitle, len(rows), start, visible, left)) + "╦" + managementGold.Render(managementScrollTitle(detailTitle, len(details), detailStart, visible, right)) + "╣"
	for y := 0; y < visible; y++ {
		i := start + y
		leftText := ""
		if i < len(rows) {
			prefix := "› "
			if i == m.selected {
				prefix = "> "
			}
			leftText = prefix + rows[i]
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 5, Width: left, Height: 1, Index: i, Control: "row"})
		}
		rightText := ""
		detailIndex := detailStart + y
		if detailIndex < len(details) {
			rightText = details[detailIndex]
		}
		leftCell := fit(leftText, left)
		if m.management.Focus == CapabilitiesPane && i == m.selected {
			leftCell = managementSelected.Render(leftCell)
		}
		rightCell := fit(rightText, right)
		actionIndex, hasAction := m.settingsActionIndex(details)
		if hasAction && detailIndex == actionIndex {
			label := details[detailIndex]
			m.management.Hits = append(m.management.Hits, hitRegion{X: left + 2, Y: y + 5, Width: right, Height: 1, Index: detailIndex, Control: "settings-action"})
			rightCell = managementGold.Render(fit(label, right))
			if m.management.Focus == ProfilesPane {
				rightCell = managementSelected.Render(fit(label, right))
			}
		} else if detailIndex < len(details) {
			m.management.Hits = append(m.management.Hits, hitRegion{X: left + 2, Y: y + 5, Width: right, Height: 1, Index: detailIndex, Control: "settings-detail"})
			if m.management.Focus == ProfilesPane && detailIndex == m.management.SettingsDetailIndex {
				rightCell = managementSelected.Render(fit(rightText, right))
			}
		}
		lines[y+5] = "║" + leftCell + "║" + rightCell + "║"
	}
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
	leftTitle := managementScrollTitle(" Agents", len(rows), start, visible, left)
	if m.management.Focus == CapabilitiesPane {
		leftTitle = managementScrollTitle("► Agents", len(rows), start, visible, left)
	}
	detailTitle := " " + selectedName + " · Agent details"
	if m.management.Focus == ProfilesPane {
		detailTitle = "► " + selectedName + " · Agent details"
	}
	lines[4] = "╠" + managementGold.Render(leftTitle) + "╦" + managementGold.Render(fit(detailTitle, right)) + "╣"
	details, controls, firstControl := m.agentManagementDisplayLines()
	detailStart := 0
	if m.management.Focus == ProfilesPane {
		detailStart = max(0, m.management.AgentDetailIndex-visible+1)
	}
	detailStart = min(detailStart, max(0, len(details)-visible))
	detailTitle = managementScrollTitle(detailTitle, len(details), detailStart, visible, right)
	lines[4] = "╠" + managementGold.Render(leftTitle) + "╦" + managementGold.Render(fit(detailTitle, right)) + "╣"
	for y := 0; y < visible; y++ {
		i := start + y
		leftText := ""
		if i < len(rows) {
			prefix := "› "
			if i == m.selected {
				prefix = "> "
			}
			leftText = prefix + rows[i] + " · " + m.agentManagementRowStatus(rows[i])
			for _, row := range m.agentManagement {
				if row.ID == rows[i] && row.Name != "" {
					leftText = prefix + row.Name + " (" + row.ID + ") · " + agentManagementStatus(row.Detection)
					break
				}
			}
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 5, Width: left, Height: 1, Index: i, Control: "row"})
		}
		rightText := ""
		detailIndex := detailStart + y
		if len(rows) > 0 && detailIndex < len(details) {
			rightText = details[detailIndex]
		}
		leftCell := fit(leftText, left)
		if i < len(rows) && i == m.selected {
			if m.management.Focus == CapabilitiesPane {
				leftCell = managementSelected.Render(leftCell)
			}
		}
		if detailIndex >= firstControl && detailIndex < firstControl+len(controls) {
			m.management.Hits = append(m.management.Hits, hitRegion{X: left + 2, Y: y + 5, Width: right, Height: 1, Index: detailIndex, Control: "agent-config"})
			if m.management.Focus == ProfilesPane && detailIndex == m.management.AgentDetailIndex {
				rightText = managementSelected.Render(fit(rightText, right))
			}
		} else if len(rows) > 0 && detailIndex < len(details) {
			m.management.Hits = append(m.management.Hits, hitRegion{X: left + 2, Y: y + 5, Width: right, Height: 1, Index: detailIndex, Control: "agent-detail"})
			if m.management.Focus == ProfilesPane && detailIndex == m.management.AgentDetailIndex {
				rightText = managementSelected.Render(fit(rightText, right))
			}
		}
		rightCell := fit(rightText, right)
		if rightText != ansi.Strip(rightText) {
			rightCell = rightText
		}
		lines[y+5] = "║" + leftCell + "║" + rightCell + "║"
	}
}

func (m *Model) agentManagementRowStatus(id string) string {
	for _, row := range m.agentManagement {
		if row.ID == id {
			return agentManagementStatus(row.Detection)
		}
	}
	return "Unverified"
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
	visibleEnd := min(len(content), m.management.ViewerOffset+visible)
	position := fmt.Sprintf("Lines %d–%d of %d", m.management.ViewerOffset+1, visibleEnd, len(content))
	if m.management.ViewerOffset > 0 {
		position = "↑ More above · " + position
	}
	if visibleEnd < len(content) {
		position += " · ↓ More below"
	}
	if m.management.ViewerHorizontal > 0 {
		position += " · ← More left"
	}
	position += " · Esc Back"
	lines[height-2] = "║" + fit(position, width-2) + "║"
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
		if _, ok := msg.(tea.MouseWheelMsg); ok && (hit.Control == "row" || hit.Control == "agent-detail" || hit.Control == "settings-detail") {
			key := "down"
			if mouse.Button == tea.MouseWheelUp {
				key = "up"
			}
			if m.view == "Environments" {
				m.management.Focus = hit.Pane
			} else if hit.Control == "agent-detail" || hit.Control == "settings-detail" {
				m.management.Focus = ProfilesPane
			} else if m.view == "Agents" || m.view == "Settings" {
				m.management.Focus = CapabilitiesPane
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
				if m.view == "Agents" || m.view == "Settings" {
					m.management.Focus = CapabilitiesPane
					if m.view == "Settings" {
						m.management.SettingsDetailOffset = 0
						m.management.SettingsDetailIndex = 0
					}
				}
			}
			return nil
		case "agent-config":
			m.management.Focus = ProfilesPane
			m.management.AgentDetailIndex = hit.Index
			return m.activateAgentDetail()
		case "agent-detail", "settings-detail":
			m.management.Focus = ProfilesPane
			if hit.Control == "agent-detail" {
				m.management.AgentDetailIndex = hit.Index
			}
			if hit.Control == "settings-detail" {
				m.management.SettingsDetailIndex = hit.Index
				_, right := managementPaneWidths(max(80, m.width))
				details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
				m.keepSettingsDetailVisible(len(details))
			}
			return nil
		case "settings-action":
			m.management.SettingsDetailIndex = hit.Index
			m.management.Focus = ProfilesPane
			_, right := managementPaneWidths(max(80, m.width))
			details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
			m.keepSettingsDetailVisible(len(details))
			return m.activateSettingsDetail()
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
