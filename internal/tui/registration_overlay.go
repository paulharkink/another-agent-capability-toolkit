package tui

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// registrationState is a presentation draft. The adapter receives a request
// only after Apply; moving between rows never changes an agent config file.
type registrationState struct {
	Profile           ProfileRow
	Agents            []string
	Choices           []registrationChoice
	Marked            map[string]bool
	OriginalMarked    map[string]bool
	Remove            bool
	NeedsTransport    bool
	Transport         string
	OriginalTransport string
	keepOnApply       bool
	Row               int // Endpoint URI is row zero; named agents follow.
	Area              int // Left list, right detail, fixed actions.
	Action            int // Cancel or Apply.
	Offset            int
	Check             viewmodel.ConnectionObservation
	Message           string
	X, Y, W           int
	H, LeftW          int
}

type registrationChoice struct {
	AgentID     string
	Destination string
}

func (choice registrationChoice) key() string {
	if choice.Destination == "" {
		return choice.AgentID
	}
	return choice.AgentID + "\x00" + choice.Destination
}

func (r *registrationState) agentStart() int {
	if r.Remove {
		return 0
	}
	if r.NeedsTransport {
		return 2
	}
	return 1
}

func (r *registrationState) lastRow() int {
	if r.Remove {
		return max(0, len(r.Choices)-1)
	}
	return max(0, r.agentStart()+len(r.Agents)-1)
}

func (r *registrationState) selectedAgent() (string, bool) {
	if r.Remove {
		choice, ok := r.selectedChoice()
		return choice.AgentID, ok
	}
	i := r.Row - r.agentStart()
	if i < 0 || i >= len(r.Agents) {
		return "", false
	}
	return r.Agents[i], true
}

func (r *registrationState) selectedChoice() (registrationChoice, bool) {
	if !r.Remove || r.Row < 0 || r.Row >= len(r.Choices) {
		return registrationChoice{}, false
	}
	return r.Choices[r.Row], true
}

func (r *registrationState) onTransport() bool { return r.NeedsTransport && r.Row == 1 }

func (r *registrationState) selectTransport() {
	if r.Transport == "" || r.Transport == "sse" {
		r.Transport = "streamable-http"
	} else {
		r.Transport = "sse"
	}
}

func (r *registrationState) HasUnsavedChanges() bool {
	if r == nil {
		return false
	}
	if r.Transport != r.OriginalTransport {
		return true
	}
	for key, marked := range r.Marked {
		if r.OriginalMarked[key] != marked {
			return true
		}
	}
	for key, marked := range r.OriginalMarked {
		if r.Marked[key] != marked {
			return true
		}
	}
	return false
}

func (r *registrationState) MarkClean() {
	if r == nil {
		return
	}
	r.OriginalMarked = cloneRegistrationMarks(r.Marked)
	r.OriginalTransport = r.Transport
}

func cloneRegistrationMarks(values map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

type registrationCheckMsg struct {
	observation viewmodel.ConnectionObservation
}

func (m *Model) registrationAgentName(id string) string {
	for _, agent := range m.agentManagement {
		if agent.ID == id && agent.Name != "" {
			return agent.Name
		}
	}
	return agents.DisplayName(id)
}

func (m *Model) openRegistrationOverlay(p ProfileRow, removing bool) {
	rows := make([]string, 0, len(m.agents))
	seen := map[string]bool{}
	add := func(id string) {
		kind, _, _ := strings.Cut(id, ":")
		if id == "" || strings.EqualFold(id, "all") || strings.EqualFold(kind, "generic") || strings.EqualFold(kind, "generic-mcp") || seen[id] {
			return
		}
		rows = append(rows, id)
		seen[id] = true
	}
	if removing {
		choices := []registrationChoice{}
		seenChoices := map[string]bool{}
		recordedAgents := map[string]bool{}
		for _, row := range m.inventory {
			if row.Key != p.Profile.Key || row.Component != "mcp" || row.AgentID == "" || row.Destination == "" {
				continue
			}
			choice := registrationChoice{AgentID: row.AgentID, Destination: row.Destination}
			if seenChoices[choice.key()] {
				continue
			}
			choices = append(choices, choice)
			seenChoices[choice.key()] = true
			recordedAgents[row.AgentID] = true
		}
		for _, id := range p.Profile.RegisteredAgents {
			if strings.TrimSpace(id) == "" || recordedAgents[id] {
				continue
			}
			choice := registrationChoice{AgentID: id}
			if seenChoices[choice.key()] {
				continue
			}
			choices = append(choices, choice)
			seenChoices[choice.key()] = true
		}
		m.registration = &registrationState{Profile: p, Choices: choices, Marked: map[string]bool{}, OriginalMarked: map[string]bool{}, Remove: true, Transport: p.Profile.Transport, OriginalTransport: p.Profile.Transport}
		return
	} else {
		for _, id := range m.agents {
			add(id)
		}
		for _, id := range p.Profile.RegisteredAgents {
			add(id)
		}
	}
	marked := map[string]bool{}
	if !removing {
		for _, id := range p.Profile.RegisteredAgents {
			marked[id] = true
		}
	}
	message := "Not checked in this session"
	if removing {
		message = ""
	}
	m.registration = &registrationState{Profile: p, Agents: rows, Marked: marked, OriginalMarked: cloneRegistrationMarks(marked), Remove: removing, NeedsTransport: p.Profile.Transport == "", Transport: p.Profile.Transport, OriginalTransport: p.Profile.Transport, Message: message}
}

func (m *Model) registrationKey(stroke string) tea.Cmd {
	r := m.registration
	if r == nil {
		return nil
	}
	switch stroke {
	case "ctrl+s":
		return m.applyRegistrationOverlay()
	case "esc":
		if r.Area != 0 {
			r.Area = 0
		} else if r.HasUnsavedChanges() {
			m.openRegistrationExitPrompt(r, false)
		} else {
			m.registration = nil
		}
	case "ctrl+c":
		return tea.Quit
	case "f10":
		if r.HasUnsavedChanges() {
			m.openRegistrationExitPrompt(r, true)
		} else if m.form != nil && m.form.HasUnsavedChanges() {
			m.unsavedExit = &unsavedExitState{reason: "quit", selected: 2}
		} else {
			return tea.Quit
		}
	case "tab":
		r.Area = (r.Area + 1) % 3
	case "shift+tab":
		r.Area = (r.Area + 2) % 3
	case "left":
		if r.Area == 2 {
			if r.Action > 0 {
				r.Action--
			} else {
				r.Area = 1
			}
		} else if r.Area == 1 {
			r.Area = 0
		}
	case "right":
		if r.Area == 0 {
			r.Area = 1
		} else if r.Area == 1 {
			r.Area = 2
		} else {
			r.Action = min(1, r.Action+1)
		}
	case "up", "k":
		if r.Area == 2 {
			r.Area = 1
		} else if r.Area == 0 {
			r.Row = max(0, r.Row-1)
		}
	case "down", "j":
		if r.Area == 0 {
			r.Row = min(r.lastRow(), r.Row+1)
		} else if r.Area == 1 {
			r.Area = 2
		}
	case "home":
		if r.Area == 0 {
			r.Row = 0
		}
	case "end":
		if r.Area == 0 {
			r.Row = r.lastRow()
		}
	case "space", " ":
		if r.Area == 1 {
			if r.onTransport() {
				r.selectTransport()
			} else if choice, ok := r.selectedChoice(); ok {
				r.Marked[choice.key()] = !r.Marked[choice.key()]
			} else if id, ok := r.selectedAgent(); ok {
				r.Marked[id] = !r.Marked[id]
			}
		}
	case "enter":
		if r.Area == 0 {
			r.Area = 1
		} else if r.Area == 1 {
			if r.Remove {
				if choice, ok := r.selectedChoice(); ok {
					r.Marked[choice.key()] = !r.Marked[choice.key()]
				}
			} else if r.Row == 0 {
				return m.checkRegistrationEndpoint()
			} else if r.onTransport() {
				r.selectTransport()
			} else if id, ok := r.selectedAgent(); ok {
				r.Marked[id] = !r.Marked[id]
			}
		} else if r.Action == 0 {
			if r.HasUnsavedChanges() {
				m.openRegistrationExitPrompt(r, false)
			} else {
				m.registration = nil
			}
		} else {
			return m.applyRegistrationOverlay()
		}
	}
	return nil
}

func (m *Model) checkRegistrationEndpoint() tea.Cmd {
	r := m.registration
	backend, ok := m.backend.(connectionBackend)
	if !ok {
		r.Message = "Connection check unavailable: backend has no connection adapter"
		return nil
	}
	url, transport := r.Profile.URL, r.Transport
	r.Message = "Checking connection…"
	return func() tea.Msg {
		return registrationCheckMsg{observation: backend.CheckConnection(m.ctx, url, transport)}
	}
}

func (m *Model) applyRegistrationOverlay() tea.Cmd {
	r := m.registration
	if r == nil {
		return nil
	}
	if !r.Remove && r.Transport == "" {
		r.Message = "Select an MCP transport before applying registrations."
		return nil
	}
	request := viewmodel.RegistrationRequest{Key: r.Profile.Key, URL: r.Profile.URL, Transport: r.Transport}
	if r.Remove {
		exact := []viewmodel.RegistrationIdentity{}
		legacy := []string{}
		for _, choice := range r.Choices {
			if !r.Marked[choice.key()] {
				continue
			}
			if choice.Destination == "" {
				legacy = append(legacy, choice.AgentID)
			} else {
				exact = append(exact, viewmodel.RegistrationIdentity{AgentID: choice.AgentID, Destination: choice.Destination})
			}
		}
		if len(exact) > 0 && len(legacy) > 0 {
			r.Message = "Choose exact-path registrations or entries with unavailable paths in separate removals."
			return nil
		}
		request.RemoveRegistrations = exact
		request.RemoveAgentIDs = legacy
		if len(exact)+len(legacy) == 0 {
			r.Message = "No registrations selected for removal"
			return nil
		}
	} else {
		selected := []string{}
		for _, id := range r.Agents {
			if r.Marked[id] {
				selected = append(selected, id)
			}
		}
		request.AgentIDs = selected
	}
	if !r.keepOnApply {
		m.registration = nil
	}
	return m.configureRegistrations(request, r.Remove)
}

func (m *Model) openRegistrationExitPrompt(r *registrationState, quit bool) {
	state := &unsavedExitState{reason: "registration", selected: 2}
	if quit {
		state.reason = "quit"
	}
	state.apply = func() tea.Cmd {
		r.keepOnApply = true
		cmd := m.applyRegistrationOverlay()
		if cmd == nil {
			r.keepOnApply = false
			m.unsavedExitIntent = nil
		}
		return cmd
	}
	state.discard = func() tea.Cmd {
		m.registration = nil
		m.unsavedExitIntent = nil
		if quit {
			return m.continueRegistrationQuit()
		}
		return nil
	}
	state.onResult = func(result operationMsg) tea.Cmd {
		m.unsavedExitIntent = nil
		succeeded := result.err == nil && !result.failed && (result.result == nil || len(result.result.Errors) == 0)
		if succeeded {
			r.MarkClean()
			m.registration = nil
			m.output = strings.TrimSpace(result.output)
			if quit {
				return m.continueRegistrationQuit()
			}
			return nil
		}
		message := strings.TrimSpace(result.output)
		if result.err != nil {
			message = result.err.Error()
		} else if result.result != nil && len(result.result.Errors) > 0 {
			message = strings.Join(result.result.Errors, "\n")
		}
		if message == "" {
			message = "Registration apply failed"
		}
		r.Message = message
		r.keepOnApply = false
		m.registration = r
		m.output = message
		m.unsavedExitFailure = true
		m.showOperationResult(result)
		return nil
	}
	m.unsavedExit = state
}

func (m *Model) continueRegistrationQuit() tea.Cmd {
	if m.form != nil && m.form.HasUnsavedChanges() {
		m.unsavedExit = &unsavedExitState{reason: "quit", selected: 2}
		return nil
	}
	return tea.Quit
}

func (m *Model) registrationOverlay(lines []string) []string {
	r := m.registration
	if r == nil {
		return lines
	}
	w := min(m.width-8, 100)
	h := min(m.height-6, 18)
	if r.Remove {
		w = min(m.width-8, 78)
		h = min(max(8, m.height-2), max(14, len(r.Choices)+12))
	}
	x, y := (m.width-w)/2, max(4, (len(lines)-h)/2)
	if y+h > len(lines) {
		y = len(lines) - h
	}
	leftW, rightW := max(24, w/3), 0
	leftW = min(leftW, w-32)
	rightW = w - leftW - 3
	r.X, r.Y, r.W, r.H, r.LeftW = x, y, w, h, leftW
	visible := max(1, h-8)
	if r.Row < r.Offset {
		r.Offset = r.Row
	}
	if r.Row >= r.Offset+visible {
		r.Offset = r.Row - visible + 1
	}
	rows := []string{}
	if !r.Remove {
		rows = append(rows, "Endpoint URI")
	}
	if r.NeedsTransport && !r.Remove {
		choice := "Select MCP transport"
		if r.Transport != "" {
			choice = r.Transport
		}
		rows = append(rows, "Transport · "+choice)
	}
	if r.Remove {
		for _, choice := range r.Choices {
			mark := "[ ]"
			if r.Marked[choice.key()] {
				mark = "[x]"
			}
			label := m.registrationAgentName(choice.AgentID)
			if choice.Destination != "" {
				label += " · " + filepath.Base(choice.Destination)
			} else {
				label += " · path unavailable"
			}
			rows = append(rows, mark+" "+label)
		}
	} else {
		for _, id := range r.Agents {
			mark := "[ ]"
			if r.Marked[id] {
				mark = "[x]"
			}
			rows = append(rows, mark+" "+m.registrationAgentName(id))
		}
	}
	leftTitle := "Named agents"
	if !r.Remove {
		leftTitle = "Endpoint and agents"
	}
	if r.Remove {
		leftTitle = "Registrations to remove"
	}
	if r.Area == 0 {
		leftTitle = "► " + leftTitle
	}
	rightTitle, details := "Endpoint", []string{"Endpoint URI", r.Profile.URL, "Check connection", "Last check: " + r.Message}
	if r.Remove {
		rightTitle = "Removal effect"
		details = []string{"Only selected local agent files and ledger entries are removed", "The MCP endpoint is left running"}
		if r.Message != "" {
			details = append([]string{r.Message}, details...)
		}
	}
	if !r.Remove && !r.Check.CheckedAt.IsZero() {
		result := "unreachable"
		if r.Check.Reachable {
			result = "reachable"
		}
		details[3] = "Last check: " + result + " · " + r.Check.CheckedAt.Format(time.RFC3339)
		if r.Check.Error != "" {
			details = append(details, r.Check.Error)
		}
	}
	if !r.Remove && r.onTransport() {
		rightTitle = "Transport"
		first, second := "[ ]", "[ ]"
		if r.Transport == "streamable-http" {
			first = "[x]"
		} else if r.Transport == "sse" {
			second = "[x]"
		}
		details = []string{"MCP transport", first + " Streamable HTTP", second + " SSE", "Enter or Space changes selection"}
	} else if id, ok := r.selectedAgent(); ok && r.Remove {
		choice, _ := r.selectedChoice()
		rightTitle = m.registrationAgentName(id)
		mark := "[ ]"
		if r.Marked[choice.key()] {
			mark = "[x]"
		}
		details = []string{"Selected removal: " + mark + " " + m.registrationAgentName(id), "Agent ID: " + id}
		if choice.Destination == "" {
			details = append(details, "Recorded config path is unavailable.", "Removal will proceed only if this ID resolves to one recorded path.")
		} else {
			details = append(details, "Config file to remove:")
			details = append(details, wrapRegistrationPath(choice.Destination, rightW-3)...)
		}
		details = append(details, "Only this local registration is removed", "The MCP endpoint is left running")
	} else if id, ok := r.selectedAgent(); ok {
		rightTitle = m.registrationAgentName(id)
		config, detection, note := "Adapter lookup", "Not detected", ""
		effective, home := "", ""
		for _, agent := range m.agentManagement {
			if agent.ID == id {
				detection = agent.Detection
				note = agent.Note
				config = agent.WriteConfigPath
				effective, home = agent.EffectiveConfigPath, agent.Home
				if config == "" && len(agent.ConfigFiles) > 0 {
					config = agent.ConfigFiles[0].Path
				}
				break
			}
		}
		mark := "[ ]"
		markKey := id
		if r.Remove {
			choice, _ := r.selectedChoice()
			markKey = choice.key()
		}
		if r.Marked[markKey] {
			mark = "[x]"
		}
		verb := "Register this endpoint for "
		if r.Remove {
			verb = "Remove this registration from "
		}
		details = []string{"Agent detection: " + detection, "Config file to change: " + config, "Planned effect: local MCP registration", "Desired registration: " + mark + " " + verb + m.registrationAgentName(id)}
		if effective != "" && effective != config {
			details = append(details, "Effective config: "+effective)
		}
		if home != "" {
			details = append(details, "Agent home: "+home)
		}
		registered := false
		for _, registeredID := range r.Profile.Profile.RegisteredAgents {
			registered = registered || registeredID == id
		}
		achieved := "not registered"
		if registered {
			achieved = "registered"
		}
		details = append(details, "Achieved registration: "+achieved)
		if note != "" {
			details = append(details, wrapRegistrationNote(note, rightW-3)...)
		}
	}
	if r.Area == 1 {
		rightTitle = "► " + rightTitle
	}
	title := "Configure agent registrations"
	if r.Remove {
		title = "Remove local registrations"
	}
	box := []string{"┌" + strings.Repeat("─", w-2) + "┐", "│" + fit(" "+title, w-2) + "│", "├" + strings.Repeat("─", leftW) + "┬" + strings.Repeat("─", rightW) + "┤"}
	box = append(box, "│"+fit(" "+leftTitle, leftW)+"│"+fit(" "+rightTitle, rightW)+"│")
	for i := 0; i < visible; i++ {
		left, right := "", ""
		if n := r.Offset + i; n < len(rows) {
			prefix := "› "
			if n == r.Row {
				prefix = "> "
			}
			left = prefix + rows[n]
		}
		if i < len(details) {
			right = "  " + details[i]
		}
		box = append(box, "│"+fit(left, leftW)+"│"+fit(right, rightW)+"│")
	}
	cue := fmt.Sprintf(" %d/%d", r.Row+1, len(rows))
	if r.Offset+visible < len(rows) {
		cue += " · ↓ More below"
	}
	if r.Offset > 0 {
		cue += " · ↑ More above"
	}
	shortcut := "Ctrl-S Save"
	if r.Remove {
		shortcut = "Ctrl-S Remove selected registrations"
	}
	footer := cue + " · Tab areas · " + shortcut + " · Esc Back"
	if r.Remove {
		footer = cue + " · Endpoint stays running · Ctrl-S Remove · Esc Back"
	}
	box = append(box, "├"+strings.Repeat("─", leftW)+"┴"+strings.Repeat("─", rightW)+"┤", "│"+fit(footer, w-2)+"│")
	actions := " Cancel    Save "
	if r.Remove {
		actions = " Cancel    Remove selected registrations "
	}
	if r.Area == 2 {
		if r.Action == 0 {
			actions = " [Cancel]   " + strings.TrimSpace(strings.TrimPrefix(actions, " Cancel   "))
		} else {
			actions = " Cancel   [" + strings.TrimSpace(strings.TrimPrefix(actions, " Cancel   ")) + "]"
		}
	}
	box = append(box, "│"+fit(actions, w-2)+"│", "└"+strings.Repeat("─", w-2)+"┘")
	for i, line := range box {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = ansi.Cut(lines[y+i], 0, x) + line + ansi.Cut(lines[y+i], x+w, m.width)
	}
	return lines
}

func wrapRegistrationNote(note string, width int) []string {
	width = max(1, width)
	lines := []string{}
	line := ""
	for _, word := range strings.Fields(note) {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if line != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, line)
			line = word
		} else {
			line = candidate
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func wrapRegistrationPath(path string, width int) []string {
	width = max(1, width)
	lines := []string{}
	var line strings.Builder
	lineWidth := 0
	for _, char := range path {
		charWidth := lipgloss.Width(string(char))
		if lineWidth > 0 && lineWidth+charWidth > width {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		line.WriteRune(char)
		lineWidth += charWidth
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	if len(lines) == 0 {
		return []string{"(empty)"}
	}
	return lines
}

func (m *Model) registrationMouse(msg tea.MouseMsg) tea.Cmd {
	r := m.registration
	if r == nil {
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		if wheel.X > r.X && wheel.X < r.X+r.LeftW+1 && wheel.Y >= r.Y+4 && wheel.Y < r.Y+r.H-4 {
			visible := max(1, r.H-8)
			switch wheel.Button {
			case tea.MouseWheelDown:
				r.Offset = min(max(0, r.lastRow()+1-visible), r.Offset+3)
				r.Row = max(r.Row, r.Offset)
			case tea.MouseWheelUp:
				r.Offset = max(0, r.Offset-3)
				r.Row = min(r.Row, r.Offset+visible-1)
			}
		}
		return nil
	}
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseLeft {
		return nil
	}
	x, y := click.X, click.Y
	if x <= r.X || x >= r.X+r.W || y <= r.Y || y >= r.Y+r.H {
		return nil
	}
	if y >= r.Y+4 && y < r.Y+r.H-4 {
		if x < r.X+r.LeftW+1 {
			r.Row = min(r.lastRow(), r.Offset+y-(r.Y+4))
			r.Area = 0
			return nil
		}
		r.Area = 1
		controlX := r.X + r.LeftW + 4
		hitControl := func(label string) bool {
			return x >= controlX && x < min(r.X+r.W-1, controlX+lipgloss.Width(label))
		}
		if r.Row == 0 && y == r.Y+6 && hitControl("Check connection") {
			return m.checkRegistrationEndpoint()
		}
		if r.onTransport() && (y == r.Y+5 || y == r.Y+6) {
			if y == r.Y+5 && hitControl("[ ] Streamable HTTP") {
				r.Transport = "streamable-http"
			} else if y == r.Y+6 && hitControl("[ ] SSE") {
				r.Transport = "sse"
			}
		}
		if r.Remove {
			if choice, ok := r.selectedChoice(); ok && y == r.Y+4 {
				mark := "[ ]"
				if r.Marked[choice.key()] {
					mark = "[x]"
				}
				label := "Selected removal: " + mark + " " + m.registrationAgentName(choice.AgentID)
				if hitControl(label) {
					r.Marked[choice.key()] = !r.Marked[choice.key()]
				}
			}
		} else if id, ok := r.selectedAgent(); ok && y == r.Y+7 {
			label := "[ ] Register this endpoint for " + m.registrationAgentName(id)
			if hitControl(label) {
				r.Marked[id] = !r.Marked[id]
			}
		}
		return nil
	}
	if y == r.Y+r.H-2 {
		if x < r.X+r.W/2 {
			if r.HasUnsavedChanges() {
				m.openRegistrationExitPrompt(r, false)
			} else {
				m.registration = nil
			}
			return nil
		}
		return m.applyRegistrationOverlay()
	}
	return nil
}
