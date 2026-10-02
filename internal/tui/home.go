package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"runtime"
	"strings"
)

// These local projections preserve domain identity; labels are presentation only.
// The typed application snapshot can replace their construction without changing navigation.
type CapabilityRow struct {
	ID, Source, Package, Name string
	Skill, MCP                bool
	CatalogIndex              int
}
type ProfileRow struct {
	ID                string
	Key               state.Key
	Name, Status, URL string
	Instance          mcp.Instance
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
	Label    string
	Rows     []string
	Offset   int
	Column   int
	Follow   bool
}
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
	Capabilities, Profiles paneState
	Focus                  Pane
	Marks                  map[string]bool
	Modal                  *modalState
	Hits                   []hitRegion
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
		rows = append(rows, CapabilityRow{ID: source + "\x00" + p.ID, Source: source, Package: p.ID, Name: name, Skill: p.Skill != nil, MCP: p.MCP != nil, CatalogIndex: i})
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
	if !ok || !c.MCP {
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
	if len(ps) == 0 {
		m.home.Focus = CapabilitiesPane
	}
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
	} else {
		rows := m.capabilities()
		if len(rows) == 0 {
			return
		}
		index = min(max(index, 0), len(rows)-1)
		if m.home.Capabilities.ID != rows[index].ID {
			m.home.Profiles = paneState{}
		}
		m.home.Capabilities.Index = index
		m.home.Capabilities.ID = rows[index].ID
		m.home.Focus = pane
	}
	m.reconcileHome()
}
func (m *Model) focusPane(pane Pane) {
	if pane == ProfilesPane && len(m.profiles()) == 0 {
		m.output = m.emptyProfiles()
		return
	}
	m.home.Focus = pane
}
func (m *Model) movePane(key string) {
	p := m.home.Capabilities
	count := len(m.capabilities())
	if m.home.Focus == ProfilesPane {
		p = m.home.Profiles
		count = len(m.profiles())
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
	m.selectPane(m.home.Focus, next)
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
	case "m", "f9":
		m.home.Modal = &modalState{Kind: "main"}
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
	case "space", " ":
		if m.home.Focus == CapabilitiesPane {
			if c, ok := m.selectedCapability(); ok {
				if m.home.Marks == nil {
					m.home.Marks = map[string]bool{}
				}
				m.home.Marks[c.ID] = !m.home.Marks[c.ID]
			}
		}
	case "enter", "f2":
		if _, ok := m.selectedCapability(); ok {
			m.home.Modal = &modalState{Kind: "actions"}
		}
	case "f3":
		m.home.Modal = &modalState{Kind: "details"}
	case "f4":
		return m.homeOperation("parameters")
	case "f5", "R":
		return m.load()
	case "u":
		m.output = "Use profile Actions for registration changes; package uninstall is unavailable here."
	case "i", "a", "s", "x", "l":
		return m.homeOperation(stroke)
	}
	return nil
}
func (m *Model) menuEntries() []string {
	if m.home.Modal.Kind == "main" {
		return []string{"Agents", "Environments", "Settings", "Help"}
	}
	if m.home.Focus == ProfilesPane {
		rows := m.profiles()
		if len(rows) == 0 {
			return []string{"Back"}
		}
		p := rows[m.home.Profiles.Index]
		label := func(name, action string) string {
			if reason := m.profileActionReason(p, action); reason != "" {
				return name + " — disabled: " + reason
			}
			return name
		}
		return []string{"Details", label("Start", "s"), label("Stop", "x"), label("Authenticate", "a"), label("Parameters", "parameters"), label("Configure registrations", "registrations"), label("Remove registrations", "remove-registrations"), label("View logs", "l"), label("Check connection", "check-connection"), "Back"}
	}
	install := "Install / Parameters"
	if c, ok := m.selectedCapability(); ok && c.CatalogIndex < 0 {
		install += " — disabled: absent from local catalog"
	}
	return []string{install, "Details", "Refresh", "Back"}
}
func (m *Model) modalKey(stroke string) tea.Cmd {
	if m.home.Modal.Kind == "logs" {
		return m.logKey(stroke)
	}
	if stroke == "esc" {
		m.home.Modal = nil
		return nil
	}
	if m.home.Modal.Kind == "details" {
		if stroke == "enter" {
			m.home.Modal = nil
		}
		return nil
	}
	entries := m.menuEntries()
	switch stroke {
	case "up":
		m.home.Modal.Selected = max(0, m.home.Modal.Selected-1)
	case "down":
		m.home.Modal.Selected = min(len(entries)-1, m.home.Modal.Selected+1)
	case "home":
		m.home.Modal.Selected = 0
	case "end":
		m.home.Modal.Selected = len(entries) - 1
	case "enter":
		index := m.home.Modal.Selected
		kind := m.home.Modal.Kind
		if kind == "main" {
			m.home.Modal = nil
			m.navigate(entries[index])
			return nil
		}
		if m.home.Focus == ProfilesPane {
			if index == 0 {
				m.home.Modal = &modalState{Kind: "details"}
				return nil
			}
			if index == 9 {
				m.home.Modal = nil
				return nil
			}
			actions := map[int]string{1: "s", 2: "x", 3: "a", 4: "parameters", 5: "registrations", 6: "remove-registrations", 7: "l", 8: "check-connection"}
			return m.homeOperation(actions[index])
		}
		m.home.Modal = nil
		switch index {
		case 0:
			return m.homeOperation("parameters")
		case 1:
			m.home.Modal = &modalState{Kind: "details"}
		case 2:
			return m.load()
		}
	}
	return nil
}
func (m *Model) homeOperation(action string) tea.Cmd {
	c, ok := m.selectedCapability()
	if !ok {
		return nil
	}
	if m.home.Focus == ProfilesPane {
		rows := m.profiles()
		if len(rows) == 0 {
			return nil
		}
		p := rows[m.home.Profiles.Index]
		if action == "remove-registrations" {
			m.removeRegistrationForm(p)
			return nil
		}
		if action == "check-connection" {
			return m.checkProfileConnection(p)
		}
		if action == "registrations" {
			m.registrationForm(p)
			return nil
		}
		if action == "l" {
			return m.openProfileLogs(p)
		}
		if action == "parameters" {
			if reason := m.profileActionReason(p, action); reason != "" {
				m.output = reason
				return nil
			}
			return m.beginSetup(p.Key.Source, p.Key.Package, p.Key.Environment, p.Key.Target)
		}
		actions := map[string]string{"s": "start", "x": "stop", "a": "authenticate", "l": "logs"}
		kind := actions[action]
		if kind == "" {
			return nil
		}
		if reason := m.profileActionReason(p, action); reason != "" {
			m.output = reason
			return nil
		}
		m.home.Modal = nil
		op := operation{action: kind, source: p.Key.Source, packageID: p.Key.Package, environment: p.Key.Environment, target: p.Key.Target}
		return m.run(op)
	}
	if c.CatalogIndex < 0 {
		m.output = "Package is absent from the local catalog; use profile Actions for registrations."
		return nil
	}
	m.selected = c.CatalogIndex
	if action == "parameters" || action == "i" {
		if _, ok := m.backend.(setupBackend); ok {
			return m.beginSetup(c.Source, c.Package, "", "")
		}
	}
	if action == "parameters" {
		action = "i"
	}
	return m.handleAction(action)
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
			m.selectPane(h.Pane, h.Index)
			if h.Pane == CapabilitiesPane && mouse.X >= 3 && mouse.X <= 5 {
				return m.homeKey("space")
			}
			return nil
		case "panes":
			return m.homeKey("tab")
		case "mark":
			return m.homeKey("space")
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
	if m.home.Focus == ProfilesPane {
		ps := m.profiles()
		if len(ps) > 0 {
			p := ps[m.home.Profiles.Index]
			return fmt.Sprintf("%s / %s / %s / %s · %s · %s", p.Key.Source, p.Key.Package, p.Key.Environment, p.Key.Target, p.Name, p.URL) + " · owner: " + p.Instance.Ownership
		}
	}
	components := []string{}
	if c.Skill {
		components = append(components, "skill")
	}
	if c.MCP {
		components = append(components, "MCP")
	}
	return fmt.Sprintf("%s · %s · %s · %d related MCP profiles", m.label(c.Source), c.Name, strings.Join(components, " + "), len(m.profiles()))
}
func (m *Model) homeView() tea.View {
	m.home.Hits = nil
	if m.width < 80 || m.height < 16 {
		v := tea.NewView(fmt.Sprintf("AACT needs at least 80×16; current %d×%d.\nF10 / q Quit", m.width, m.height))
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
	lines := []string{"╔" + fit(" AACT ", width-2) + "╗", "║" + fit(" Main menu [F9 / m]", width-2) + "║", "║" + fit(" Checkout: "+m.settings["checkout"]+"   Managing: "+runtime.GOOS+" / "+runtime.GOARCH, width-2) + "║"}
	ltitle := "Capabilities"
	rtitle := "MCP profiles · " + c.Name
	if m.home.Focus == CapabilitiesPane {
		ltitle = "► " + ltitle
	} else {
		rtitle = "► " + rtitle
	}
	lines = append(lines, "╠"+fit(ltitle, left)+"╦"+fit(rtitle, right)+"╣")
	for row := 0; row < visible; row++ {
		l, r := "", ""
		i := m.home.Capabilities.Offset + row
		if i < len(cs) {
			mark := "[ ]"
			if m.home.Marks[cs[i].ID] {
				mark = "[x]"
			}
			cursor := " "
			if i == m.home.Capabilities.Index {
				cursor = ">"
			}
			kind := "skill"
			if cs[i].MCP {
				kind = "MCP"
				if cs[i].Skill {
					kind = "skill + MCP"
				}
			}
			l = cursor + " " + mark + " " + cs[i].Name + "  " + kind
			m.home.Hits = append(m.home.Hits, hitRegion{X: 1, Y: 4 + row, Width: left, Height: 1, Pane: CapabilitiesPane, Index: i, Control: "row"})
		}
		i = m.home.Profiles.Offset + row
		if i < len(ps) {
			cursor := " "
			if i == m.home.Profiles.Index {
				cursor = ">"
			}
			r = cursor + " " + ps[i].Key.Environment + " / " + ps[i].Key.Target + "  " + ps[i].Status
			if ps[i].Instance.Ownership != "" {
				r += " · " + ps[i].Instance.Ownership
			}
			m.home.Hits = append(m.home.Hits, hitRegion{X: left + 2, Y: 4 + row, Width: right, Height: 1, Pane: ProfilesPane, Index: i, Control: "row"})
		} else if row == 0 && len(ps) == 0 {
			r = m.emptyProfiles()
		}
		lines = append(lines, "║"+fit(l, left)+"║"+fit(r, right)+"║")
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
	lines = append(lines, "║"+fit(pos(m.home.Capabilities, len(cs)), left)+"║"+fit(pos(m.home.Profiles, len(ps)), right)+"║", "╠"+strings.Repeat("═", width-2)+"╣", "║"+fit(m.selectedDetail(), width-2)+"║", "║"+fit(status, width-2)+"║", "╠"+strings.Repeat("═", width-2)+"╣")
	footer := []struct{ text, control string }{{"F1 Help", "help"}, {"F2 Actions", "actions"}, {"F3 Details", "details"}, {"F4 Parameters", "parameters"}, {"F5 Refresh", "refresh"}}
	x := 2
	f := " "
	fy := len(lines)
	for _, b := range footer {
		f += b.text + "  "
		m.home.Hits = append(m.home.Hits, hitRegion{X: x, Y: fy, Width: len(b.text), Height: 1, Control: b.control})
		x += len(b.text) + 2
	}
	lines = append(lines, "║"+fit(f, width-2)+"║")
	second := []struct{ text, control string }{{"Tab/←→ Panes", "panes"}, {"Enter Actions", "actions"}, {"Space Mark", "mark"}, {"m Main menu", "main"}, {"F10/q Quit", "quit"}}
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
	m.home.Hits = append(m.home.Hits, hitRegion{X: 1, Y: 1, Width: 24, Height: 1, Control: "main"}, hitRegion{X: 1, Y: 3, Width: left, Height: visible + 2, Pane: CapabilitiesPane, Control: "pane"}, hitRegion{X: left + 2, Y: 3, Width: right, Height: visible + 2, Pane: ProfilesPane, Control: "pane"})
	if m.home.Modal != nil {
		lines = m.overlay(lines)
	}
	v := tea.NewView(strings.Join(lines, "\n"))
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
	}
	entries := []string{}
	if m.home.Modal.Kind == "details" {
		title = "Details"
		entries = m.profileDetails()
	} else {
		entries = m.menuEntries()
	}
	w := min(m.width-6, 64)
	x := (m.width - w) / 2
	y := max(3, (len(lines)-len(entries)-4)/2)
	box := []string{"┌" + fit(" "+title, w-2) + "┐"}
	for i, e := range entries {
		prefix := "  "
		if i == m.home.Modal.Selected {
			prefix = "> "
		}
		box = append(box, "│"+fit(prefix+e, w-2)+"│")
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
