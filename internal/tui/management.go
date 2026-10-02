package tui

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

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
}

type environmentEntry struct {
	Name    string
	Targets []string
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

// Saved profiles are shown separately from environment files because the
// current service does not expose a directory scan or exact TOML contents.
func (m *Model) environmentEntries() []environmentEntry {
	entries := []environmentEntry{{Name: "No environment file"}}
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
		m.navigate("Catalog")
		m.home.Modal = &modalState{Kind: "main"}
	case "enter":
		if m.view == "Settings" && m.selected == 0 {
			m.editEnvironmentRoot()
			return nil
		}
		fallthrough
	case "f2":
		if m.view != "Help" {
			m.management.Modal = "actions"
			m.management.ModalSelected = 0
		}
	case "f5", "R":
		if m.view == "Agents" {
			if _, ok := m.backend.(agentManagementBackend); !ok {
				m.output = "Detection refresh unavailable: agent service exposes IDs, not detection evidence."
				return nil
			}
		}
		return m.load()
	case "tab", "left", "right":
		if m.view == "Environments" {
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

func (m *Model) managementMenuEntries() []string {
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
		return []string{"Use for new setups — disabled: target service unavailable", "View target — disabled: exact TOML unavailable from service", "Environment root", "Close"}
	case "Settings":
		return []string{"Edit environment root", "Default named agents — disabled: service support pending", "Docker backend — disabled: service support pending", "Back"}
	}
	return nil
}

func (m *Model) managementModalKey(stroke string) tea.Cmd {
	if m.management.Modal == "viewer" {
		if stroke == "esc" {
			m.management.Modal = "files"
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
		m.management.ModalSelected = max(0, m.management.ModalSelected-1)
	case "down":
		m.management.ModalSelected = min(len(entries)-1, m.management.ModalSelected+1)
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
		if entries[i] == "Environment root" || entries[i] == "Edit environment root" {
			m.management.Modal = ""
			m.editEnvironmentRoot()
			return nil
		}
		m.management.Modal = ""
	}
	return nil
}

func (m *Model) editEnvironmentRoot() {
	m.pending = operation{action: "set-environment-root"}
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Environment root", Type: "directory", Required: true}}, map[string]any{"root": m.settings["environment_root"]})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}

func (m *Model) managementSettingsRows() []string {
	defaultAgents := m.settings["default_agents"]
	if defaultAgents == "" {
		defaultAgents = "None selected"
	}
	return []string{
		"Environment root: " + m.settings["environment_root"],
		"Default named agents: " + defaultAgents,
		"Docker backend: selection unavailable from service",
		"Source: " + m.settings["source"],
		"Checkout: " + m.settings["checkout"],
		"State: " + m.settings["state-dir"],
		"Platform: " + runtime.GOOS + " / " + runtime.GOARCH,
	}
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
	lines[0] = "╔" + fit(" AACT · "+m.view, width-2) + "╗"
	lines[1] = "║" + fit(" F9/m Main menu · F1 Help · Esc Back", width-2) + "║"
	lines[2] = "╠" + strings.Repeat("═", width-2) + "╣"
	visible := height - 11
	if m.view == "Agents" {
		m.renderAgentManagement(lines, visible)
	} else if m.view == "Environments" {
		m.renderEnvironmentManagement(lines, visible)
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
	v := tea.NewView(strings.Join(lines, "\n"))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) renderManagementList(lines []string, rows []string, visible int) {
	width := m.width
	start := max(0, m.selected-visible+1)
	if m.selected < visible {
		start = 0
	}
	for y := 0; y < visible; y++ {
		i := start + y
		if i >= len(rows) {
			break
		}
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		lines[y+4] = "║" + fit(prefix+rows[i], width-2) + "║"
		m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 4, Width: width - 2, Height: 1, Index: i, Control: "row"})
	}
	lines[3] = "║" + fit(" "+m.view, width-2) + "║"
}

func (m *Model) renderAgentManagement(lines []string, visible int) {
	rows := m.namedAgents()
	left := (m.width - 3) / 2
	right := m.width - 3 - left
	lines[3] = "╠" + fit(" Agents", left) + "╦" + fit(" Evidence and registrations", right) + "╣"
	start := max(0, m.selected-visible+1)
	if m.selected < visible {
		start = 0
	}
	for y := 0; y < visible; y++ {
		i := start + y
		leftText := ""
		if i < len(rows) {
			prefix := "  "
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
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 4, Width: left, Height: 1, Index: i, Control: "row"})
		}
		rightText := ""
		if len(rows) > 0 {
			selected := rows[min(m.selected, len(rows)-1)]
			registrations := m.agentRegistrations(selected)
			details := []string{"Detection unavailable", "Config location unavailable", fmt.Sprintf("AACT registrations: %d", len(registrations))}
			if row, ok := m.selectedAgentManagement(); ok {
				registrations = row.Registrations
				details = []string{"Detection: " + row.Detection, "Evidence: " + row.Evidence, "Note: " + row.Note, fmt.Sprintf("Config candidates: %d", len(row.ConfigFiles))}
				for _, file := range row.ConfigFiles {
					state := "missing"
					if file.Exists {
						state = "exists"
					}
					details = append(details, file.Path+" · "+state+" · "+file.Scope+" · "+file.Precedence)
				}
				details = append(details, fmt.Sprintf("AACT registrations: %d", len(registrations)))
			}
			details = append(details, registrations...)
			if y < len(details) {
				rightText = details[y]
			}
		}
		lines[y+4] = "║" + fit(leftText, left) + "║" + fit(rightText, right) + "║"
	}
}

func (m *Model) agentConfigView() tea.View {
	width, height := m.width, m.height
	lines := make([]string, height)
	lines[0] = "╔" + fit(" Agent configuration · "+m.management.ViewerPath, width-2) + "╗"
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
	v := tea.NewView(strings.Join(lines, "\n"))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) renderEnvironmentManagement(lines []string, visible int) {
	entries := m.environmentEntries()
	m.management.EnvironmentIndex = min(max(0, m.management.EnvironmentIndex), len(entries)-1)
	selected := entries[m.management.EnvironmentIndex]
	left := (m.width - 3) / 2
	right := m.width - 3 - left
	leftTitle, rightTitle := " Environments", " Targets · "+selected.Name
	if m.management.Focus == CapabilitiesPane {
		leftTitle = "►" + leftTitle
	} else {
		rightTitle = "►" + rightTitle
	}
	lines[3] = "╠" + fit(leftTitle, left) + "╦" + fit(rightTitle, right) + "╣"
	leftStart := max(0, m.management.EnvironmentIndex-visible+1)
	rightStart := max(0, m.management.TargetIndex-visible+1)
	for y := 0; y < visible; y++ {
		l, r := "", ""
		i := leftStart + y
		if i < len(entries) {
			prefix := "  "
			if i == m.management.EnvironmentIndex {
				prefix = "> "
			}
			l = prefix + entries[i].Name
			m.management.Hits = append(m.management.Hits, hitRegion{X: 1, Y: y + 4, Width: left, Height: 1, Pane: CapabilitiesPane, Index: i, Control: "row"})
		}
		i = rightStart + y
		if i < len(selected.Targets) {
			prefix := "  "
			if i == m.management.TargetIndex {
				prefix = "> "
			}
			r = prefix + selected.Targets[i]
			m.management.Hits = append(m.management.Hits, hitRegion{X: left + 2, Y: y + 4, Width: right, Height: 1, Pane: ProfilesPane, Index: i, Control: "row"})
		} else if y == 0 && len(selected.Targets) == 0 {
			r = "No configured targets exposed by service"
		}
		lines[y+4] = "║" + fit(l, left) + "║" + fit(r, right) + "║"
	}
	lines[m.height-7] = "║" + fit(" Environment root: "+m.settings["environment_root"]+" · saved profiles do not prove TOML files exist", m.width-2) + "║"
}

func (m *Model) renderManagementModal(lines []string) {
	entries := m.managementMenuEntries()
	w := min(m.width-6, 75)
	x := (m.width - w) / 2
	y := max(4, (m.height-len(entries)-4)/2)
	box := []string{"┌" + fit(" Actions", w-2) + "┐"}
	for i, entry := range entries {
		prefix := "  "
		if i == m.management.ModalSelected {
			prefix = "> "
		}
		box = append(box, "│"+fit(prefix+entry, w-2)+"│")
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
			}
			return nil
		case "help":
			return m.managementKey("f1")
		case "actions":
			return m.managementKey("f2")
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
