package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// These local projections preserve domain identity; labels are presentation only.
// The typed application snapshot can replace their construction without changing navigation.
type CapabilityRow struct {
	ID, Source, Package, Name string
	Skill, MCP                bool
	MCPNames                  []string
	CatalogIndex              int
}
type ProfileRow struct {
	ID                string
	Key               state.Key
	Name, Status, URL string
	Instance          mcp.Instance
	Configuration     *viewmodel.CapabilityProfile
	Profile           *viewmodel.Profile
}
type Pane uint8

const (
	CapabilitiesPane Pane = iota
	ProfilesPane
)

type paneState struct {
	ID            string
	Index, Offset int
}
type modalState struct {
	Kind     string
	Selected int
	Parent   *modalState
	Label    string
	Rows     []string
	Offset   int
	Column   int
	Follow   bool
}
type homeMenuItem struct{ Label, Action, Reason string }
type hitRegion struct {
	X, Y, Width, Height int
	Pane                Pane
	Index               int
	Control             string
}

func (h hitRegion) contains(x, y int) bool {
	return x >= h.X && x < h.X+h.Width && y >= h.Y && y < h.Y+h.Height
}

type homeState struct {
	Capabilities, Profiles, Context paneState
	ContextDisplayOffset            int
	Focus                           Pane
	Modal                           *modalState
	Hits                            []hitRegion
}

type contextRow struct {
	ID, Label, Kind string
	ProfileIndex    int
	Key             state.Key
}

func (m *Model) contextRows() []contextRow {
	c, ok := m.selectedCapability()
	if !ok {
		return nil
	}
	if m.profileMode() && c.CatalogIndex >= 0 {
		rows := []contextRow{}
		for i, p := range m.profiles() {
			rows = append(rows, contextRow{ID: p.ID, Label: p.Name + " · " + p.Status, Kind: "profile", ProfileIndex: i, Key: p.Key})
		}
		rows = append(rows, contextRow{ID: "create-profile", Label: "Create another profile…", Kind: "create-profile"}, contextRow{ID: "details", Label: "View capability details", Kind: "details"})
		return rows
	}
	if c.CatalogIndex < 0 {
		rows := []contextRow{
			{ID: "locate", Label: "Locate Capability Pack…", Kind: "locate-source"},
			{ID: "information", Label: "View saved information", Kind: "saved-information"},
		}
		for i, p := range m.profiles() {
			target := p.Key.Target
			if target == "" {
				target = "default"
			}
			rows = append(rows, contextRow{ID: p.ID, Label: p.Key.Environment + " / " + target + " · " + p.Name + " · " + p.Status, Kind: "profile", ProfileIndex: i, Key: p.Key})
		}
		return rows
	}
	rows := []contextRow{
		{ID: "configure", Label: capabilitySetupLabel(), Kind: "configure"},
		{ID: "details", Label: "View capability details", Kind: "details"},
	}
	for i, p := range m.profiles() {
		label := p.Name + " · " + p.Status
		rows = append(rows, contextRow{ID: p.ID, Label: label, Kind: "profile", ProfileIndex: i, Key: p.Key})
	}
	seen := map[state.Key]bool{}
	for _, row := range rows {
		if row.Kind == "profile" {
			seen[row.Key] = true
		}
	}
	for _, installed := range m.inventory {
		key := installed.Key
		if key.Source != c.Source || key.Package != c.Package || installed.Component == "runtime" || seen[key] {
			continue
		}
		seen[key] = true
		if c.Skill && !c.MCP && key.Environment == "" && (key.Target == "" || key.Target == "default") {
			rows = append(rows, contextRow{ID: key.ID() + "\x00configured", Label: "Saved configuration", Kind: "target", Key: key})
			continue
		}
		environment, target := key.Environment, key.Target
		if environment == "" {
			environment = "No preset"
		}
		if target == "" {
			target = "default"
		}
		rows = append(rows, contextRow{ID: key.ID() + "\x00configured", Label: environment + " / " + target + " · saved target", Kind: "target", Key: key})
	}
	return rows
}

func (m *Model) installationDetails(c CapabilityRow) (string, []string) {
	if m.inventoryError != nil {
		return "Unknown", []string{"AACT records unavailable: " + m.inventoryError.Error()}
	}
	type component struct{ name, kind, mcp string }
	components := []component{}
	if c.Skill {
		components = append(components, component{name: "Skill", kind: "skill"})
	}
	if c.MCP {
		if len(c.MCPNames) == 0 {
			components = append(components, component{name: "MCP registration", kind: "mcp"})
		} else {
			for _, name := range c.MCPNames {
				components = append(components, component{name: name + " MCP registration", kind: "mcp", mcp: name})
			}
		}
	}
	if len(components) == 0 {
		return "Unknown", []string{"No installable components in the catalog"}
	}
	componentsByDestination := map[string]map[string]bool{}
	found := 0
	details := make([]string, 0, len(components))
	for _, component := range components {
		agents := map[string]bool{}
		for _, inst := range m.inventory {
			if inst.Key.Source != c.Source || inst.Key.Package != c.Package || inst.Component != component.kind {
				continue
			}
			if component.kind == "mcp" && component.mcp != "" && inst.Key.MCP != component.mcp {
				continue
			}
			parentKey := inst.Key
			parentKey.MCP = ""
			destination := inst.AgentID + "\x00" + inst.AgentHome
			if destination == "\x00" {
				destination = "unspecified destination"
			}
			group := parentKey.ID() + "\x00" + destination
			if componentsByDestination[group] == nil {
				componentsByDestination[group] = map[string]bool{}
			}
			componentsByDestination[group][component.kind+"\x00"+component.mcp] = true
			agent := inst.AgentID
			if agent == "" {
				agent = "unspecified destination"
			}
			agents[agent] = true
		}
		if len(agents) == 0 {
			details = append(details, component.name+": no AACT record")
			continue
		}
		found++
		ids := make([]string, 0, len(agents))
		for agent := range agents {
			ids = append(ids, agent)
		}
		sort.Strings(ids)
		details = append(details, component.name+": "+strings.Join(ids, ", "))
	}
	if found == 0 {
		return "Not installed", details
	}
	complete := false
	for _, installed := range componentsByDestination {
		all := true
		for _, component := range components {
			if !installed[component.kind+"\x00"+component.mcp] {
				all = false
				break
			}
		}
		if all {
			complete = true
			break
		}
	}
	if !complete {
		return "Partial", details
	}
	return "Installed", details
}

func (m *Model) selectedContextRow() (contextRow, bool) {
	rows := m.contextRows()
	if len(rows) == 0 {
		return contextRow{}, false
	}
	return rows[min(max(m.home.Context.Index, 0), len(rows)-1)], true
}

func (m *Model) selectedContextProfile() (ProfileRow, bool) {
	row, ok := m.selectedContextRow()
	profiles := m.profiles()
	if !ok || row.Kind != "profile" || row.ProfileIndex >= len(profiles) {
		return ProfileRow{}, false
	}
	return profiles[row.ProfileIndex], true
}

func (m *Model) capabilities() []CapabilityRow {
	rows := make([]CapabilityRow, 0, len(m.catalog))
	for i, p := range m.catalog {
		source := m.sourceLabels[p.Dir]
		if source == "" {
			source = m.settings["source"]
		}
		name := p.Name
		if name == "" {
			name = p.ID
		}
		mcpNames := []string{}
		for _, definition := range p.MCPDefinitions() {
			if definition.Name != "" {
				mcpNames = append(mcpNames, definition.Name)
			}
		}
		rows = append(rows, CapabilityRow{ID: source + "\x00" + p.ID, Source: source, Package: p.ID, Name: name, Skill: p.HasSkill(), MCP: p.HasMCP(), MCPNames: mcpNames, CatalogIndex: i})
	}
	if m.profileSnapshot != nil {
		seen := map[string]bool{}
		for _, row := range rows {
			seen[row.ID] = true
		}
		for _, p := range m.profileSnapshot.Profiles {
			id := p.Key.Source + "\x00" + p.Key.Package
			if seen[id] {
				continue
			}
			seen[id] = true
			rows = append(rows, CapabilityRow{ID: id, Source: p.Key.Source, Package: p.Key.Package, Name: p.Key.Package + " · " + m.label(p.Key.Source), MCP: true, CatalogIndex: -1})
		}
	}

	return rows
}
func (m *Model) selectedCapability() (CapabilityRow, bool) {
	rows := m.capabilities()
	if len(rows) == 0 {
		return CapabilityRow{}, false
	}
	i := min(max(m.home.Capabilities.Index, 0), len(rows)-1)
	return rows[i], true
}
func (m *Model) profiles() []ProfileRow {
	c, ok := m.selectedCapability()
	if !ok {
		return nil
	}
	if m.profileMode() && c.CatalogIndex >= 0 {
		rows := []ProfileRow{}
		snapshot := m.capabilityProfiles[c.Package]
		for _, profile := range snapshot.Profiles {
			copy := profile
			rows = append(rows, ProfileRow{ID: profile.Key.ID(), Key: profile.Key, Name: profile.Ref.Name, Status: profileStatusSummary(profile), Configuration: &copy})
		}
		return rows
	}
	if !c.MCP {
		return nil
	}
	rows := []ProfileRow{}
	if m.profileSnapshot != nil {
		for _, p := range m.profileSnapshot.Profiles {
			if p.Key.Source == c.Source && p.Key.Package == c.Package {
				profile := p
				rows = append(rows, ProfileRow{ID: p.Key.ID(), Key: p.Key, Name: p.Name, Status: p.RuntimeStatus, URL: p.URL, Profile: &profile, Instance: mcp.Instance{Key: p.Key, Name: p.Name, Status: p.RuntimeStatus, URL: p.URL, Ownership: p.Ownership}})
			}
		}
		return rows
	}
	for _, p := range m.mcps {
		if p.Key.Source == c.Source && p.Key.Package == c.Package {
			rows = append(rows, ProfileRow{ID: p.Key.ID() + "\x00" + p.Name, Key: p.Key, Name: p.Name, Status: p.Status, URL: p.URL, Instance: p})
		}
	}
	return rows
}
func (m *Model) paneHeight() int { return max(1, m.height-12) }
func reconcilePane(p *paneState, ids []string, visible int) bool {
	oldID := p.ID
	found := false
	for i, id := range ids {
		if id == oldID {
			p.Index = i
			found = true
			break
		}
	}
	if len(ids) == 0 {
		*p = paneState{}
		return oldID != ""
	}
	p.Index = min(max(0, p.Index), len(ids)-1)
	p.ID = ids[p.Index]
	p.Offset = min(max(0, p.Offset), max(0, len(ids)-visible))
	if p.Index < p.Offset {
		p.Offset = p.Index
	}
	if p.Index >= p.Offset+visible {
		p.Offset = p.Index - visible + 1
	}
	return oldID != "" && !found
}
func (m *Model) reconcileHome() {
	cs := m.capabilities()
	ids := []string{}
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	removed := reconcilePane(&m.home.Capabilities, ids, m.paneHeight())
	if removed {
		m.home.Profiles = paneState{}
	}
	ps := m.profiles()
	ids = nil
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	removed = reconcilePane(&m.home.Profiles, ids, m.paneHeight()) || removed
	contextIDs := make([]string, 0, len(ps)+3)
	for _, row := range m.contextRows() {
		contextIDs = append(contextIDs, row.ID)
	}
	reconcilePane(&m.home.Context, contextIDs, m.paneHeight())
	if removed {
		m.output = "Selected item was removed; selected the nearest surviving row."
	}
	m.selected = m.home.Capabilities.Index
}
func (m *Model) selectPane(pane Pane, index int) {
	if pane == ProfilesPane {
		rows := m.profiles()
		if len(rows) == 0 {
			m.output = m.emptyProfiles()
			return
		}
		m.home.Focus = pane
		m.home.Profiles.Index = min(max(index, 0), len(rows)-1)
		m.home.Profiles.ID = rows[m.home.Profiles.Index].ID
		m.home.Context.Index = 0
		for i, contextRow := range m.contextRows() {
			if contextRow.Kind == "profile" && contextRow.ProfileIndex == m.home.Profiles.Index {
				m.home.Context.Index = i
				m.home.Context.ID = contextRow.ID
				break
			}
		}
	} else {
		rows := m.capabilities()
		if len(rows) == 0 {
			return
		}
		index = min(max(index, 0), len(rows)-1)
		if m.home.Capabilities.ID != rows[index].ID {
			m.home.Profiles = paneState{}
			m.home.Context = paneState{}
			m.home.ContextDisplayOffset = 0
		}
		m.home.Capabilities.Index = index
		m.home.Capabilities.ID = rows[index].ID
		m.home.Focus = pane
	}
	m.reconcileHome()
}
func (m *Model) focusPane(pane Pane) {
	m.home.Focus = pane
}

func (m *Model) selectContext(index int) {
	rows := m.contextRows()
	if len(rows) == 0 {
		return
	}
	m.home.Focus = ProfilesPane
	m.home.Context.Index = min(max(index, 0), len(rows)-1)
	m.home.Context.ID = rows[m.home.Context.Index].ID
	if row := rows[m.home.Context.Index]; row.Kind == "profile" {
		profiles := m.profiles()
		m.home.Profiles.Index = row.ProfileIndex
		m.home.Profiles.ID = profiles[row.ProfileIndex].ID
	}
	m.reconcileHome()
}
func (m *Model) movePane(key string) {
	p := m.home.Capabilities
	count := len(m.capabilities())
	if m.home.Focus == ProfilesPane {
		p = m.home.Context
		count = len(m.contextRows())
	}
	next := p.Index
	switch key {
	case "up", "k":
		next--
	case "down", "j":
		next++
	case "pgup":
		next -= m.paneHeight()
	case "pgdown":
		next += m.paneHeight()
	case "home":
		next = 0
	case "end":
		next = count - 1
	}
	if m.home.Focus == ProfilesPane {
		m.selectContext(next)
	} else {
		m.selectPane(CapabilitiesPane, next)
	}
}
func (m *Model) emptyProfiles() string {
	c, ok := m.selectedCapability()
	if !ok {
		return "No capability selected"
	}
	if !c.MCP {
		return "No MCP profiles — skill-only capability"
	}
	return "No MCP profiles yet — configure/install to create one"
}
func (m *Model) homeKey(stroke string) tea.Cmd {
	if stroke == "ctrl+c" || stroke == "f10" {
		return tea.Quit
	}
	if m.home.Modal != nil {
		return m.modalKey(stroke)
	}
	switch stroke {
	case "ctrl+c", "f10", "q":
		return tea.Quit
	case "esc":
		if m.home.Focus == ProfilesPane {
			m.home.Focus = CapabilitiesPane
		}
	case "m", "f9":
		m.openHomeMenu("main")
	case "f1", "?":
		m.navigate("Help")
	case "tab":
		m.focusPane(1 - m.home.Focus)
	case "left":
		m.focusPane(CapabilitiesPane)
	case "right":
		m.focusPane(ProfilesPane)
	case "up", "down", "k", "j", "pgup", "pgdown", "home", "end":
		m.movePane(stroke)
	case "enter":
		if m.home.Focus == CapabilitiesPane {
			m.focusPane(ProfilesPane)
		} else {
			return m.openContextRow()
		}
	case "f2":
		if m.home.Focus == CapabilitiesPane {
			m.focusPane(ProfilesPane)
		} else {
			return m.openContextRow()
		}
	case "f3":
		if m.home.Focus == CapabilitiesPane {
			m.selectContext(1)
		} else if row, ok := m.selectedContextRow(); ok && row.Kind == "details" {
			m.home.Modal = &modalState{Kind: "details"}
		} else {
			m.selectContext(1)
		}
	case "f4":
		if m.home.Focus == CapabilitiesPane {
			m.selectContext(0)
		} else if row, ok := m.selectedContextRow(); ok && row.Kind == "configure" {
			return m.homeOperation("parameters")
		} else {
			m.selectContext(0)
		}
	case "f5", "R":
		return m.load()
	case "u":
		m.output = "Use profile Actions for registration changes; package uninstall is unavailable here."
	case "i", "a", "s", "x", "l":
		return m.homeOperation(stroke)
	}
	return nil
}

func (m *Model) openContextRow() tea.Cmd {
	row, ok := m.selectedContextRow()
	if !ok {
		return nil
	}
	switch row.Kind {
	case "create-profile":
		if c, ok := m.selectedCapability(); ok {
			m.openProfileCreation(c)
		}
	case "configure":
		return m.homeOperation("parameters")
	case "locate-source":
		m.locateSelectedSource()
	case "saved-information":
		m.showSavedCapabilityInformation()
	case "remove-local-registration":
		m.removeUnavailableRegistration()
	case "details":
		m.home.Modal = &modalState{Kind: "details"}
	case "profile":
		if c, ok := m.selectedCapability(); ok && c.CatalogIndex < 0 {
			if profile, found := m.selectedContextProfile(); found && profile.Key == row.Key {
				m.openObservedProfileWorkspace(profile)
				return nil
			}
		}
		return m.openTargetWorkspace(m.packProfileRequest(row.Key), "Overview")
	case "target":
		return m.openTargetWorkspace(m.packProfileRequest(row.Key), "Overview")
	}
	return nil
}

func (m *Model) openObservedProfileWorkspace(p ProfileRow) {
	if p.Profile == nil || p.Profile.Key != p.Key {
		m.output = "Observed target facts are unavailable; refresh this profile before opening its workspace."
		return
	}
	profile := *p.Profile
	profile.RegisteredAgents = append([]string(nil), p.Profile.RegisteredAgents...)
	sections := []forms.FormSection{{ID: sectionOverviewID, Title: "Overview"}, {ID: sectionEndpointID, Title: "Endpoint"}, {ID: sectionAgentsID, Title: "Agents"}, {ID: sectionInformationID, Title: "Information"}}
	form := forms.NewForm(m.ctx, nil, nil)
	form.SetTitle("Observed · " + p.Name)
	form.SetBackNavigation(true)
	form.SetReadOnly(true)
	form.SetSections(sections...)
	form.SelectSectionID(sectionOverviewID)
	_, _, width, height, _ := managementFormOverlayBounds(m.width, m.height)
	form.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.workspace = &workspaceState{
		Key: p.Key, Section: "Overview", InvokingView: m.view, InvokingSelection: m.home.Context.Index,
		Active: true, ObservedOnly: true, Existing: true, Profile: &profile, ProfileSnapshot: copyProfileSnapshot(m.profileSnapshot),
	}
	m.form = form
	m.refreshObservedWorkspaceFacts()
	m.pendingSetup = nil
	m.home.Modal = nil
}

func (m *Model) locateSelectedSource() {
	m.pending = operation{action: "locate-source", source: m.selectedCapabilitySource()}
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Capability Pack directory", Type: "directory", Required: true}}, map[string]any{})
	m.form.SetTitle("Locate Capability Pack · " + m.selectedCapabilitySource())
	m.form.SetBackNavigation(true)
	m.management.FormOverlay = true
	_, _, width, height, _ := managementFormOverlayBounds(m.width, m.height)
	m.form.Update(tea.WindowSizeMsg{Width: width, Height: height})
}

func (m *Model) showSavedCapabilityInformation() {
	c, ok := m.selectedCapability()
	if !ok {
		return
	}
	info := "Saved capability: " + c.Package + " · Capability Pack: " + c.Source + ". The pack is unavailable. Locate its directory to configure it."
	if m.profileSnapshot != nil {
		for _, p := range m.profileSnapshot.Profiles {
			if p.Key.Source == c.Source && p.Key.Package == c.Package {
				info += " Saved target: " + p.Key.Environment + " / " + p.Key.Target + "."
			}
		}
	}
	m.output = info
}

func (m *Model) removeUnavailableRegistration() {
	profiles := m.profiles()
	if len(profiles) == 0 {
		m.output = "No local registration is recorded for this saved target."
		return
	}
	m.removeRegistrationForm(profiles[0])
}

func (m *Model) selectedCapabilitySource() string {
	c, _ := m.selectedCapability()
	return c.Source
}
func (m *Model) homeMenuItems() []homeMenuItem {
	if m.home.Modal == nil {
		return nil
	}
	if m.home.Modal.Kind == "main" {
		return []homeMenuItem{{"Agents", "Agents", ""}, {"Settings", "Settings", ""}, {"Help", "Help", ""}, {"Back", "back", ""}}
	}
	if m.profileMode() {
		return []homeMenuItem{{"Open selected profile", "parameters", ""}, {"Create another profile…", "create-profile", ""}, {"View capability details", "details", ""}, {"Back", "back", ""}}
	}
	if m.home.Focus == ProfilesPane {
		p, isProfile := m.selectedContextProfile()
		if isProfile {
			item := func(label, action string) homeMenuItem {
				return homeMenuItem{label, action, m.profileActionReason(p, action)}
			}
			start := "Start…"
			if strings.EqualFold(p.Status, "running") {
				start = "Restart…"
			}
			bindingReason := ""
			if _, available := m.backend.(setupBackend); !available {
				bindingReason = "setup service unavailable"
			} else if capability, exists := m.selectedCapability(); !exists || capability.CatalogIndex < 0 {
				bindingReason = "package is absent from the local catalog"
			}
			return []homeMenuItem{item(start, "s"), item("Stop…", "x"), item("Edit parameters…", "parameters"), {"Manage complete agent binding…", "agents", bindingReason}, item("Check connection", "check-connection"), {"Refresh observation", "refresh", ""}, item("View logs", "l"), {"View details", "details", ""}, {"Back", "back", ""}}
		}
	}
	c, ok := m.selectedCapability()
	if !ok {
		return []homeMenuItem{{"Back", "back", ""}}
	}
	installReason := ""
	if c.CatalogIndex < 0 {
		installReason = "absent from local catalog"
	}
	if c.CatalogIndex < 0 {
		return []homeMenuItem{{"Locate Capability Pack…", "locate-source", ""}, {"View saved information", "saved-information", ""}, {"Back", "back", ""}}
	}
	items := []homeMenuItem{{capabilitySetupLabel(), "parameters", installReason}}
	items = append(items, homeMenuItem{"View capability details", "details", ""}, homeMenuItem{"Back", "back", ""})
	return items
}

func capabilitySetupLabel() string {
	return "Configure now…"
}
func (m *Model) menuEntries() []string {
	items := m.homeMenuItems()
	entries := make([]string, 0, len(items))
	for _, item := range items {
		label := item.Label
		if item.Reason != "" {
			label += " — disabled: " + item.Reason
		}
		entries = append(entries, label)
	}
	return entries
}
func (m *Model) openHomeMenu(kind string) {
	m.home.Modal = &modalState{Kind: kind}
	for i, item := range m.homeMenuItems() {
		if item.Reason == "" {
			m.home.Modal.Selected = i
			break
		}
	}
}
func (m *Model) modalKey(stroke string) tea.Cmd {
	if m.home.Modal.Kind == "logs" {
		return m.logKey(stroke)
	}
	if stroke == "esc" {
		m.home.Modal = m.home.Modal.Parent
		return nil
	}
	if m.home.Modal.Kind == "details" {
		if stroke == "enter" {
			m.home.Modal = m.home.Modal.Parent
		}
		return nil
	}
	items := m.homeMenuItems()
	switch stroke {
	case "up":
		for step := 0; step < len(items); step++ {
			m.home.Modal.Selected = (m.home.Modal.Selected + len(items) - 1) % len(items)
			if items[m.home.Modal.Selected].Reason == "" {
				break
			}
		}
	case "down":
		for step := 0; step < len(items); step++ {
			m.home.Modal.Selected = (m.home.Modal.Selected + 1) % len(items)
			if items[m.home.Modal.Selected].Reason == "" {
				break
			}
		}
	case "home":
		for i, item := range items {
			if item.Reason == "" {
				m.home.Modal.Selected = i
				break
			}
		}
	case "end":
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Reason == "" {
				m.home.Modal.Selected = i
				break
			}
		}
	case "enter":
		index := m.home.Modal.Selected
		if index < 0 || index >= len(items) {
			return nil
		}
		item := items[index]
		if item.Reason != "" {
			m.output = item.Reason
			return nil
		}
		if m.home.Modal.Kind == "actions" && item.Action == "locate-source" {
			m.home.Modal = nil
			m.locateSelectedSource()
			return nil
		}
		if m.home.Modal.Kind == "actions" && item.Action == "saved-information" {
			m.home.Modal = nil
			m.showSavedCapabilityInformation()
			return nil
		}
		if m.home.Modal.Kind == "actions" && item.Action == "remove-local-registration" {
			m.home.Modal = nil
			m.removeUnavailableRegistration()
			return nil
		}
		if m.home.Modal.Kind == "main" {
			m.home.Modal = nil
			if item.Action != "back" {
				m.navigate(item.Action)
			}
			return nil
		}
		switch item.Action {
		case "back":
			m.home.Modal = nil
		case "refresh":
			m.home.Modal = nil
			return m.load()
		case "details":
			m.home.Modal = &modalState{Kind: "details", Parent: m.home.Modal}
		default:
			if item.Action != "registrations" && item.Action != "remove-registrations" {
				m.home.Modal = nil
			}
			return m.homeOperation(item.Action)
		}
	}
	return nil
}
func (m *Model) homeOperation(action string) tea.Cmd {
	if m.profileMode() {
		if action == "create-profile" {
			if c, ok := m.selectedCapability(); ok {
				m.openProfileCreation(c)
			}
			return nil
		}
		if action == "details" {
			m.home.Modal = &modalState{Kind: "details"}
			return nil
		}
		if m.home.Focus == CapabilitiesPane {
			m.focusPane(ProfilesPane)
			return nil
		}
		if row, ok := m.selectedContextRow(); ok {
			if row.Kind == "create-profile" {
				if c, ok := m.selectedCapability(); ok {
					m.openProfileCreation(c)
				}
				return nil
			}
			if row.Kind == "profile" {
				section := "Overview"
				if action == "agents" {
					section = "Agents"
				}
				if action == "s" || action == "x" || action == "l" {
					section = "Runtime"
				}
				return m.openTargetWorkspace(m.packProfileRequest(row.Key), section)
			}
		}
		return nil
	}

	c, ok := m.selectedCapability()
	if !ok {
		return nil
	}
	if m.home.Focus == ProfilesPane {
		p, isProfile := m.selectedContextProfile()
		if isProfile {
			capability, capabilityOK := m.selectedCapability()
			packageUnavailable := capabilityOK && capability.CatalogIndex < 0
			if action == "remove-registrations" {
				if packageUnavailable {
					m.openObservedProfileWorkspace(p)
					return nil
				}
				return m.openTargetWorkspace(m.packProfileRequest(p.Key), "Agents")
			}
			if action == "check-connection" {
				return m.checkProfileConnection(p)
			}
			if action == "registrations" || action == "agents" {
				if packageUnavailable {
					m.openObservedProfileWorkspace(p)
					return nil
				}
				return m.openTargetWorkspace(m.packProfileRequest(p.Key), "Agents")
			}
			if action == "l" {
				return m.openProfileLogs(p)
			}
			if action == "parameters" {
				if packageUnavailable {
					m.output = "Capability Pack configuration is unavailable. Enter on this observed target opens its read-only workspace; use Locate Capability Pack… to restore package settings."
					return nil
				}
				if reason := m.profileActionReason(p, action); reason != "" {
					m.output = reason
					return nil
				}
				return m.openTargetWorkspace(m.packProfileRequest(p.Key), "Overview")
			}

			actions := map[string]string{"s": "start", "x": "stop", "l": "logs"}
			kind := actions[action]
			if kind == "" {
				return nil
			}
			if reason := m.profileActionReason(p, action); reason != "" {
				m.output = reason
				return nil
			}
			m.home.Modal = nil
			op := operation{action: kind, source: p.Key.Source, packageID: p.Key.Package, environment: p.Key.Environment, target: p.Key.Target, mcp: p.Key.MCP}
			return m.run(op)
		}
	}
	if c.CatalogIndex < 0 {
		m.output = "Capability is absent from the active catalog; Enter a saved target to open its observed workspace, or choose Locate Capability Pack… to restore package settings."
		return nil
	}
	m.selected = c.CatalogIndex
	if action == "parameters" || action == "i" {
		if _, ok := m.backend.(setupBackend); ok {
			m.openProfileCreation(c)
			return nil
		}
	}
	if action != "details" {
		m.output = "Choose a Capability Pack profile before starting or changing installed components."
	}
	return nil
}
func (m *Model) homeMouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	m.homeView() // Rebuild geometry after resize/refresh, not stale rendered coordinates.
	if m.home.Modal != nil {
		if m.home.Modal.Kind == "logs" {
			return m.logMouse(msg)
		}
		if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft {
			for _, h := range m.home.Hits {
				if h.contains(mouse.X, mouse.Y) && h.Control == "menu" {
					m.home.Modal.Selected = h.Index
					return m.modalKey("enter")
				}
			}
		}
		return nil
	}
	for _, h := range m.home.Hits {
		if !h.contains(mouse.X, mouse.Y) {
			continue
		}
		if _, ok := msg.(tea.MouseWheelMsg); ok && h.Control == "pane" {
			old := m.home.Focus
			m.focusPane(h.Pane)
			key := "down"
			if mouse.Button == tea.MouseWheelUp {
				key = "up"
			}
			m.movePane(key)
			if h.Pane != old {
				m.home.Focus = old
			}
			return nil
		}
		if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft {
			continue
		}
		switch h.Control {
		case "row":
			if h.Pane == ProfilesPane {
				m.selectContext(h.Index)
			} else {
				m.selectPane(h.Pane, h.Index)
			}
			return nil
		case "panes":
			return m.homeKey("tab")
		case "main":
			return m.homeKey("m")
		case "actions":
			return m.homeKey("f2")
		case "details":
			return m.homeKey("f3")
		case "parameters":
			return m.homeKey("f4")
		case "refresh":
			return m.homeKey("f5")
		case "help":
			return m.homeKey("f1")
		case "quit":
			return tea.Quit
		case "pane":
			m.focusPane(h.Pane)
			return nil
		}
	}
	return nil
}
func fit(s string, width int) string {
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	s = ansi.Truncate(s, max(0, width), "…")
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}
func (m *Model) selectedDetail() string {
	c, ok := m.selectedCapability()
	if !ok {
		return "No capabilities in this catalog"
	}
	if c.CatalogIndex < 0 {
		return "Capability Pack unavailable · capability " + c.Package + " · use Locate Capability Pack to recover configuration access"
	}
	if m.home.Focus == ProfilesPane {
		if p, ok := m.selectedContextProfile(); ok {
			if p.Configuration != nil {
				return c.Name + " · Profile " + p.Name + " · " + profileStatusSummary(*p.Configuration)
			}
			return fmt.Sprintf("%s / %s / %s / %s · %s · %s", p.Key.Source, p.Key.Package, p.Key.Environment, p.Key.Target, p.Name, p.URL) + " · owner: " + p.Instance.Ownership
		}
		if row, ok := m.selectedContextRow(); ok {
			return c.Name + " · " + row.Label
		}
	}
	components := []string{}
	if c.Skill {
		components = append(components, "skill")
	}
	if c.MCP {
		if len(c.MCPNames) == 0 {
			components = append(components, "MCP")
		} else {
			for _, name := range c.MCPNames {
				components = append(components, "MCP "+name)
			}
		}
	}
	detail := fmt.Sprintf("Capability Pack · %s · %s", c.Name, strings.Join(components, " + "))
	if c.MCP {
		if m.profileMode() {
			detail += fmt.Sprintf(" · %d configuration profiles", len(m.profiles()))
		} else {
			detail += fmt.Sprintf(" · %d related MCP profiles", len(m.profiles()))
		}
	}
	status, _ := m.installationDetails(c)
	detail += " · AACT records: " + status
	return detail
}
func (m *Model) homeView() tea.View {
	m.home.Hits = nil
	if m.width < 80 || m.height < 16 {
		lines := []string{
			"┌" + strings.Repeat("─", max(0, m.width-2)) + "┐",
			"│" + fit("AACT needs at least 80×16; current "+fmt.Sprintf("%d×%d", m.width, m.height), max(0, m.width-2)) + "│",
			"│" + fit("Resize to continue. Your current draft is retained.", max(0, m.width-2)) + "│",
			"│" + fit("F10 / q Quit", max(0, m.width-2)) + "│",
			"└" + strings.Repeat("─", max(0, m.width-2)) + "┘",
		}
		for len(lines) < m.height {
			lines = append(lines, strings.Repeat(" ", m.width))
		}
		v := tea.NewView(navyCanvas(strings.Join(lines[:min(m.height, len(lines))], "\n")))
		return v
	}
	m.reconcileHome()
	width := m.width
	left := (width - 3) / 2
	right := width - 3 - left
	visible := m.paneHeight()
	cs := m.capabilities()
	ps := m.profiles()
	c, _ := m.selectedCapability()
	type displayRow struct {
		text         string
		contextIndex int
		kind         string
	}
	contextDisplay := []displayRow{{text: "Capability", contextIndex: -1, kind: "heading"}}
	contextRows := m.contextRows()
	if m.profileMode() && c.CatalogIndex >= 0 {
		contextDisplay = []displayRow{{text: "Configuration profiles", contextIndex: -1, kind: "heading"}}
		for i, row := range contextRows {
			contextDisplay = append(contextDisplay, displayRow{text: row.Label, contextIndex: i, kind: "action"})
			if row.Kind == "profile" {
				for _, detail := range profileConfigurationDetails(ps[row.ProfileIndex]) {
					contextDisplay = append(contextDisplay, displayRow{text: detail, contextIndex: -1, kind: "empty"})
				}
			}
		}
		for _, message := range m.capabilityProfiles[c.Package].Errors {
			contextDisplay = append(contextDisplay, displayRow{text: "Observation: " + message, contextIndex: -1, kind: "empty"})
		}
	} else {
		if c.CatalogIndex < 0 {
			contextDisplay = append(contextDisplay, displayRow{text: "Capability Pack unavailable · " + c.Package, contextIndex: -1, kind: "heading"})
			for i := 0; i < min(3, len(contextRows)); i++ {
				contextDisplay = append(contextDisplay, displayRow{text: contextRows[i].Label, contextIndex: i, kind: "action"})
			}
			if len(contextRows) > 3 {
				heading := "Saved targets"
				if c.Skill && !c.MCP {
					heading = "Saved configuration"
				}
				contextDisplay = append(contextDisplay, displayRow{text: heading, contextIndex: -1, kind: "heading"})
				for i := 3; i < len(contextRows); i++ {
					contextDisplay = append(contextDisplay, displayRow{text: contextRows[i].Label, contextIndex: i, kind: "action"})
				}
			}
		} else {
			for i := 0; i < min(2, len(contextRows)); i++ {
				contextDisplay = append(contextDisplay, displayRow{text: contextRows[i].Label, contextIndex: i, kind: "action"})
			}
		}
		if c.MCP && c.CatalogIndex >= 0 {
			contextDisplay = append(contextDisplay, displayRow{text: "Related MCP profiles", contextIndex: -1, kind: "heading"})
			if len(ps) == 0 {
				contextDisplay = append(contextDisplay, displayRow{text: "No profiles yet — configure/install to create one", contextIndex: -1, kind: "empty"})
			} else {
				for i, p := range ps {
					environment, target := p.Key.Environment, p.Key.Target
					if environment == "" {
						environment = "No preset"
					}
					if target == "" {
						target = "default"
					}
					contextDisplay = append(contextDisplay, displayRow{text: environment + " / " + target + " · MCP · " + p.Name + " · " + p.Status, contextIndex: i + 2, kind: "action"})
				}
			}
		}
		if len(contextRows) > 2+len(m.profiles()) {
			heading := "Saved targets"
			if c.Skill && !c.MCP {
				heading = "Saved configuration"
			}
			contextDisplay = append(contextDisplay, displayRow{text: heading, contextIndex: -1, kind: "heading"})
			for i := 2 + len(m.profiles()); i < len(contextRows); i++ {
				contextDisplay = append(contextDisplay, displayRow{text: contextRows[i].Label, contextIndex: i, kind: "action"})
			}
		}
		installStatus, installDetails := m.installationDetails(c)
		contextDisplay = append(contextDisplay, displayRow{text: "Installation · AACT records: " + installStatus, contextIndex: -1, kind: "heading"})
		for _, detail := range installDetails {
			contextDisplay = append(contextDisplay, displayRow{text: detail, contextIndex: -1, kind: "empty"})
		}
	}
	selectedDisplay := 0
	for i, row := range contextDisplay {
		if row.contextIndex == m.home.Context.Index {
			selectedDisplay = i
			break
		}
	}
	if selectedDisplay < m.home.ContextDisplayOffset {
		m.home.ContextDisplayOffset = selectedDisplay
	}
	if selectedDisplay >= m.home.ContextDisplayOffset+visible {
		m.home.ContextDisplayOffset = selectedDisplay - visible + 1
	}
	m.home.ContextDisplayOffset = min(max(0, m.home.ContextDisplayOffset), max(0, len(contextDisplay)-visible))
	shortPath := func(path string) string {
		if path == "" {
			return "none"
		}
		return filepath.Base(path)
	}
	scope := " Capability Pack: " + m.settings["source"] + " · Profiles: " + shortPath(m.environmentRoot()) + " · Managing: " + runtime.GOOS + "/" + runtime.GOARCH
	menubar := " F9 Main menu: Agents | Settings | Help   F2 Open / Focus"
	lines := []string{"╔" + fit(" AACT · Another Agent Capability Toolkit", width-2) + "╗", "║" + fit(menubar, width-2) + "║", "║" + fit(scope, width-2) + "║"}
	ltitle := "Capabilities"
	rtitle := "Selected capability · " + c.Name
	if m.home.Focus == CapabilitiesPane {
		ltitle = "► " + ltitle
	} else {
		rtitle = "► " + rtitle
	}
	if m.home.ContextDisplayOffset > 0 {
		rtitle = "↑ More above · " + rtitle
	}
	gold := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffe38a"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#a8bddb"))
	selected := lipgloss.NewStyle().Foreground(lipgloss.Color("#081f5b")).Background(lipgloss.Color("#e9f2fb"))
	focusedTitle := lipgloss.NewStyle().Bold(true).Reverse(true)
	leftTitle := gold.Render(fit(ltitle, left))
	rightTitle := gold.Render(fit(rtitle, right))
	if m.home.Focus == CapabilitiesPane {
		leftTitle = focusedTitle.Render(fit(ltitle, left))
	} else {
		rightTitle = focusedTitle.Render(fit(rtitle, right))
	}
	lines = append(lines, "╠"+leftTitle+"╦"+rightTitle+"╣")
	for row := 0; row < visible; row++ {
		l, r := "", ""
		i := m.home.Capabilities.Offset + row
		if i < len(cs) {
			installationStatus, _ := m.installationDetails(cs[i])
			cursor := "›"
			if i == m.home.Capabilities.Index {
				cursor = ">"
			}
			kind := "skill"
			if cs[i].MCP {
				kind = "MCP"
				if len(cs[i].MCPNames) > 0 {
					kind = "MCP: " + strings.Join(cs[i].MCPNames, ", ")
				}
				if cs[i].Skill {
					kind = "skill + MCP"
				}
			}
			l = cursor + " " + installationStatus + " · " + cs[i].Name + "  " + kind
			m.home.Hits = append(m.home.Hits, hitRegion{X: 1, Y: 4 + row, Width: left, Height: 1, Pane: CapabilitiesPane, Index: i, Control: "row"})
		}
		i = m.home.ContextDisplayOffset + row
		if i < len(contextDisplay) {
			item := contextDisplay[i]
			if item.contextIndex >= 0 {
				cursor := "›"
				if item.contextIndex == m.home.Context.Index {
					cursor = ">"
				}
				r = cursor + " " + item.text
				m.home.Hits = append(m.home.Hits, hitRegion{X: left + 2, Y: 4 + row, Width: right, Height: 1, Pane: ProfilesPane, Index: item.contextIndex, Control: "row"})
			} else if item.kind == "heading" {
				r = "── " + item.text
			} else {
				r = "· " + item.text
			}
		}
		leftBody, rightBody := left, right
		leftBar, rightBar := len(cs) > visible, len(contextDisplay) > visible
		if leftBar {
			leftBody = max(1, left-1)
		}
		if rightBar {
			rightBody = max(1, right-1)
		}
		l = fit(l, leftBody)
		r = fit(r, rightBody)
		if leftBar {
			l += scrollbarGlyph(m.home.Capabilities.Offset, len(cs), visible, row)
		}
		if rightBar {
			r += scrollbarGlyph(m.home.ContextDisplayOffset, len(contextDisplay), visible, row)
		}
		if i := m.home.ContextDisplayOffset + row; i < len(contextDisplay) {
			switch contextDisplay[i].kind {
			case "heading":
				r = gold.Render(r)
			case "empty":
				r = muted.Render(r)
			}
		}
		if i := m.home.Capabilities.Offset + row; i < len(cs) && i == m.home.Capabilities.Index && m.home.Focus == CapabilitiesPane {
			l = selected.Render(l)
		}
		if i := m.home.ContextDisplayOffset + row; i < len(contextDisplay) && contextDisplay[i].contextIndex == m.home.Context.Index && m.home.Focus == ProfilesPane {
			r = selected.Render(r)
		}
		lines = append(lines, "║"+l+"║"+r+"║")
	}
	pos := func(p paneState, n int) string {
		if n == 0 {
			return "0/0"
		}
		return fmt.Sprintf("%d/%d", p.Index+1, n)
	}
	status := m.output
	if m.busy {
		status = "Running " + m.action + "…"
	}
	if m.profileSnapshot != nil && m.profileSnapshot.ObservationStale {
		observed := "never"
		if !m.profileSnapshot.ObservedAt.IsZero() {
			observed = m.profileSnapshot.ObservedAt.Format("2006-01-02 15:04:05 MST")
		}
		status = "Docker: " + m.profileSnapshot.DockerError + "; last observed " + observed + " · " + status
	}
	contextPosition := pos(m.home.Context, len(contextRows))
	if m.home.ContextDisplayOffset+visible < len(contextDisplay) {
		contextPosition += "  ↓ More below"
	}
	lines = append(lines, "║"+fit(pos(m.home.Capabilities, len(cs)), left)+"║"+fit(contextPosition, right)+"║", "╠"+strings.Repeat("═", width-2)+"╣", "║"+fit(m.selectedDetail(), width-2)+"║", "║"+fit(status, width-2)+"║", "╠"+strings.Repeat("═", width-2)+"╣")
	footer := []struct{ text, control string }{{"F1 Help", "help"}, {"F2 Open / Focus", "actions"}, {"F3 Details", "details"}, {"F4 Parameters", "parameters"}, {"F5 Refresh", "refresh"}}
	x := 2
	f := " "
	fy := len(lines)
	for _, b := range footer {
		f += b.text + "  "
		m.home.Hits = append(m.home.Hits, hitRegion{X: x, Y: fy, Width: len(b.text), Height: 1, Control: b.control})
		x += len(b.text) + 2
	}
	lines = append(lines, "║"+fit(f, width-2)+"║")
	second := []struct{ text, control string }{{"Tab/←→ Panes", "panes"}, {"Enter Open / Focus", "actions"}, {"m Main menu", "main"}, {"F10/q Quit", "quit"}}
	f = " "
	x = 2
	fy = len(lines)
	for i, b := range second {
		if i > 0 {
			f += " · "
			x += 3
		}
		f += b.text
		m.home.Hits = append(m.home.Hits, hitRegion{X: x, Y: fy, Width: lipgloss.Width(b.text), Height: 1, Control: b.control})
		x += lipgloss.Width(b.text)
	}
	lines = append(lines, "║"+fit(f, width-2)+"║", "╚"+strings.Repeat("═", width-2)+"╝")
	actionsStart := strings.Index(menubar, "F2 Open / Focus")
	m.home.Hits = append(m.home.Hits, hitRegion{X: 1, Y: 1, Width: actionsStart, Height: 1, Control: "main"}, hitRegion{X: actionsStart + 1, Y: 1, Width: len("F2 Open / Focus"), Height: 1, Control: "actions"}, hitRegion{X: 1, Y: 3, Width: left, Height: visible + 2, Pane: CapabilitiesPane, Control: "pane"}, hitRegion{X: left + 2, Y: 3, Width: right, Height: visible + 2, Pane: ProfilesPane, Control: "pane"})
	if m.home.Modal != nil && m.registration == nil {
		lines = m.overlay(lines)
	}
	if m.registration != nil {
		lines = m.registrationOverlay(lines)
	}
	v := tea.NewView(navyCanvas(strings.Join(lines, "\n")))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
func (m *Model) overlay(lines []string) []string {
	if m.home.Modal.Kind == "logs" {
		return m.logsOverlay(lines)
	}
	title := "Actions"
	if m.home.Modal.Kind == "main" {
		title = "Main menu"
	} else if m.home.Modal.Kind == "actions" {
		if m.home.Focus == ProfilesPane {
			if profiles := m.profiles(); len(profiles) > 0 {
				profile := profiles[m.home.Profiles.Index]
				if capability, ok := m.selectedCapability(); ok {
					title = capability.Name + " · " + profile.Name
				}
			}
		} else if capability, ok := m.selectedCapability(); ok {
			title = capability.Name + " · Actions"
		}
	}
	entries := []string{}
	menuItems := []homeMenuItem{}
	if m.home.Modal.Kind == "details" {
		title = "Details"
		entries = m.profileDetails()
	} else {
		menuItems = m.homeMenuItems()
		entries = m.menuEntries()
	}
	w := min(m.width-6, 64)
	x := (m.width - w) / 2
	y := max(3, (len(lines)-len(entries)-4)/2)
	gold := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffe38a"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#a8bddb"))
	selected := lipgloss.NewStyle().Foreground(lipgloss.Color("#081f5b")).Background(lipgloss.Color("#e9f2fb"))
	box := []string{"┌" + gold.Render(fit(" "+title, w-2)) + "┐"}
	for i, e := range entries {
		prefix := "› "
		disabled := i < len(menuItems) && menuItems[i].Reason != ""
		if m.home.Modal.Kind == "details" {
			prefix = "· "
		} else if disabled {
			prefix = "× "
		}
		if i == m.home.Modal.Selected {
			if m.home.Modal.Kind != "details" {
				prefix = "> "
			}
		}
		item := fit(prefix+e, w-2)
		if i == m.home.Modal.Selected && m.home.Modal.Kind != "details" && !disabled {
			item = selected.Render(item)
		} else if disabled {
			item = muted.Render(item)
		}
		box = append(box, "│"+item+"│")
		if m.home.Modal.Kind != "details" {
			m.home.Hits = append(m.home.Hits, hitRegion{X: x + 1, Y: y + i + 1, Width: w - 2, Height: 1, Index: i, Control: "menu"})
		}
	}
	box = append(box, "│"+fit(" ↑↓ Select · Enter Open · Esc Back", w-2)+"│", "└"+strings.Repeat("─", w-2)+"┘")
	for i, line := range box {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = ansi.Cut(lines[y+i], 0, x) + line + ansi.Cut(lines[y+i], x+w, m.width)
	}
	return lines
}

func scrollbarGlyph(offset, total, visible, row int) string {
	if total <= visible || visible < 1 {
		return " "
	}
	thumb := max(1, visible*visible/total)
	start := offset * (visible - thumb) / max(1, total-visible)
	if row >= start && row < start+thumb {
		return "█"
	}
	return "│"
}
