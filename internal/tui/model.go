package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"io"
	"sort"
	"strings"
)

// Backend uses existing domain types so app.Service can implement it without importing tui.
// UISourceLabels maps package directories to source IDs and may map source IDs to display labels.
// UIRun's target argument is the root path for the set-environment-root action.
type Backend interface {
	UICatalog(context.Context) ([]catalog.Package, error)
	UIInventory(context.Context) ([]state.Installation, error)
	UIMCPs(context.Context) ([]mcp.Instance, error)
	UIAgents(context.Context) ([]string, error)
	UISettings(context.Context) (map[string]string, error)
	UISourceLabels(context.Context) (map[string]string, error)
	UIRun(ctx context.Context, action, sourceID, packageID, profile, agentID, environment, target string) (string, error)
}

type agentManagementBackend interface {
	UIAgentManagement(context.Context) ([]viewmodel.AgentManagementRow, error)
	UIAgentConfig(context.Context, string, string) (string, error)
}
type Model struct {
	home                       homeState
	backend                    Backend
	ctx                        context.Context
	view                       string
	selected, width, height    int
	catalog                    []catalog.Package
	inventory                  []state.Installation
	mcps                       []mcp.Instance
	profileSnapshot            *viewmodel.ProfileSnapshot
	profileError               error
	inventoryError             error
	environmentSnapshot        *viewmodel.EnvironmentSnapshot
	environmentError           error
	pendingRegistration        *viewmodel.RegistrationRequest
	pendingRegistrationRemoval bool
	registration               *registrationState
	pendingSetup               *viewmodel.SetupPreview
	pendingSetupField          string
	setupRetry                 *setupRetryDraft
	setupOperationPending      bool
	logSession                 uint64
	logProfile                 state.Key
	logLabel                   string
	pendingDefaultAgents       bool
	agents                     []string
	agentManagement            []viewmodel.AgentManagementRow
	settings, sourceLabels     map[string]string
	busy                       bool
	output, action             string
	result                     *resultState
	form                       *forms.FormModel
	pending                    operation
	management                 managementState
}
type operation struct{ action, source, packageID, profile, agent, environment, target string }
type loadedMsg struct {
	catalog             []catalog.Package
	inventory           []state.Installation
	inventoryError      error
	mcps                []mcp.Instance
	profileSnapshot     *viewmodel.ProfileSnapshot
	profileError        error
	environmentSnapshot *viewmodel.EnvironmentSnapshot
	environmentError    error
	agents              []string
	agentManagement     []viewmodel.AgentManagementRow
	settings, labels    map[string]string
	err                 error
}
type agentConfigMsg struct {
	path, content string
	err           error
}
type operationMsg struct {
	origin, output string
	err            error
	failed         bool
}
type settingsSavedMsg struct{ err error }

func New(backend Backend) tea.Model { return NewContext(context.Background(), backend) }
func NewContext(ctx context.Context, backend Backend) *Model {
	return &Model{backend: backend, ctx: ctx, view: "Catalog", width: 80, height: 24, settings: map[string]string{}, sourceLabels: map[string]string{}}
}
func (m *Model) Init() tea.Cmd { return m.load() }
func (m *Model) load() tea.Cmd {
	return func() tea.Msg {
		var msg loadedMsg
		errs := []error{}
		var e error
		msg.catalog, e = m.backend.UICatalog(m.ctx)
		if e != nil {
			errs = append(errs, e)
		}
		msg.inventory, e = m.backend.UIInventory(m.ctx)
		msg.inventoryError = e
		if e != nil {
			errs = append(errs, e)
		}
		if backend, ok := m.backend.(profileSnapshotBackend); ok {
			snapshot, err := backend.UIProfileSnapshot(m.ctx)
			e = err
			msg.profileError = err
			if err == nil {
				msg.profileSnapshot = &snapshot
			}
		} else {
			msg.mcps, e = m.backend.UIMCPs(m.ctx)
		}
		if e != nil {
			errs = append(errs, e)
		}
		msg.agents, e = m.backend.UIAgents(m.ctx)
		if e != nil {
			errs = append(errs, e)
		}
		if backend, ok := m.backend.(agentManagementBackend); ok {
			msg.agentManagement, e = backend.UIAgentManagement(m.ctx)
			if e != nil {
				errs = append(errs, e)
			}
		}
		if backend, ok := m.backend.(environmentBrowserBackend); ok {
			snapshot, err := backend.UIEnvironmentSnapshot(m.ctx)
			msg.environmentError = err
			if err == nil {
				msg.environmentSnapshot = &snapshot
			} else {
				errs = append(errs, err)
			}
		}
		msg.settings, e = m.backend.UISettings(m.ctx)
		if e != nil {
			errs = append(errs, e)
		}
		msg.labels, e = m.backend.UISourceLabels(m.ctx)
		if e != nil {
			errs = append(errs, e)
		}
		msg.err = errors.Join(errs...)
		return msg
	}
}
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		m.reconcileHome()
	}
	if m.registration != nil {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			return m, m.registrationKey(event.String())
		case registrationCheckMsg:
			m.registration.Check = event.observation
			return m, nil
		case tea.MouseMsg:
			return m, m.registrationMouse(event)
		}
	}
	if m.form != nil {
		if x, y, width, height, ok := m.setupOverlayBounds(); ok {
			switch event := msg.(type) {
			case tea.WindowSizeMsg:
				msg = tea.WindowSizeMsg{Width: width, Height: height}
			case tea.MouseClickMsg:
				if event.X < x || event.X >= x+width || event.Y < y || event.Y >= y+height {
					return m, nil
				}
				event.X -= x
				event.Y -= y
				msg = event
			case tea.MouseWheelMsg:
				if event.X < x || event.X >= x+width || event.Y < y || event.Y >= y+height {
					return m, nil
				}
				event.X -= x
				event.Y -= y
				msg = event
			}
		}
		next, cmd := m.form.Update(msg)
		m.form = next.(*forms.FormModel)
		values, e := m.form.Result()
		if errors.Is(e, forms.ErrNotSubmitted) {
			return m, cmd
		}
		m.form = nil
		if errors.Is(e, picker.ErrCancelled) {
			m.pendingRegistration = nil
			m.pendingRegistrationRemoval = false
			m.pendingSetup = nil
			m.pendingSetupField = ""
			m.pendingDefaultAgents = false
			m.output = "Cancelled"
			return m, nil
		}
		if e != nil {
			m.output = e.Error()
			return m, nil
		}
		if m.pendingRegistration != nil {
			request := *m.pendingRegistration
			m.pendingRegistration = nil
			selected, _ := values["agent"].([]string)
			removing := m.pendingRegistrationRemoval
			m.pendingRegistrationRemoval = false
			if removing {
				if len(selected) == 0 {
					m.output = "No registrations selected for removal"
					m.home.Modal = nil
					return m, nil
				}
				removed := make(map[string]bool, len(selected))
				for _, agent := range selected {
					removed[agent] = true
				}
				remaining := make([]string, 0, len(request.AgentIDs))
				for _, agent := range request.AgentIDs {
					if !removed[agent] {
						remaining = append(remaining, agent)
					}
				}
				request.AgentIDs = remaining
			} else {
				request.AgentIDs = selected
			}
			if request.Transport == "" {
				request.Transport, _ = values["transport"].(string)
			}
			return m, m.configureRegistrations(request, removing)
		}
		if m.pendingSetup != nil {
			return m, m.applySetup(values)
		}
		if m.pendingDefaultAgents {
			m.pendingDefaultAgents = false
			selected, _ := values["agents"].([]string)
			backend := m.backend.(defaultAgentsBackend)
			m.busy = true
			return m, func() tea.Msg { return settingsSavedMsg{err: backend.UISetDefaultAgents(m.ctx, selected)} }
		}
		op := m.pending
		if op.action == "set-environment-root" {
			op.target, _ = values["root"].(string)
		} else {
			if agents, ok := values["agent"].([]string); ok {
				op.agent = strings.Join(agents, ",")
			}
			op.environment, _ = values["environment"].(string)
			op.target, _ = values["target"].(string)
		}
		return m, m.run(op)
	}
	switch msg := msg.(type) {
	case logsMsg:
		return m, m.showLogs(msg)
	case logPollMsg:
		if msg.session == m.logSession && m.home.Modal != nil && m.home.Modal.Kind == "logs" && m.home.Modal.Follow {
			return m, m.fetchLogs(msg.session)
		}
	case setupPreviewMsg:
		m.busy = false
		if msg.err != nil {
			m.output = m.cleanOutput(msg.err.Error())
			return m, nil
		}
		m.openSetupForm(msg.preview)
	case loadedMsg:
		m.catalog = msg.catalog
		m.inventory = msg.inventory
		m.inventoryError = msg.inventoryError
		m.mcps = msg.mcps
		m.profileError = msg.profileError
		m.environmentError = msg.environmentError
		m.environmentSnapshot = msg.environmentSnapshot
		if msg.profileSnapshot != nil {
			m.profileSnapshot = msg.profileSnapshot
		}
		m.agents = msg.agents
		m.agentManagement = msg.agentManagement
		m.settings = msg.settings
		m.sourceLabels = msg.labels
		if msg.err != nil {
			m.output = m.cleanOutput(msg.err.Error())
		}
		m.reconcileHome()
	case agentConfigMsg:
		if msg.err != nil {
			m.output = m.cleanOutput(msg.err.Error())
			return m, nil
		}
		m.management.ViewerPath = msg.path
		m.management.ViewerContent = msg.content
		m.management.ViewerOffset = 0
		m.management.ViewerHorizontal = 0
		m.management.Modal = "viewer"
	case environmentTargetMsg:
		if msg.err != nil {
			m.output = m.cleanOutput(msg.err.Error())
			return m, nil
		}
		m.management.ViewerPath = msg.path
		m.management.ViewerContent = msg.content
		m.management.ViewerOffset = 0
		m.management.ViewerHorizontal = 0
		m.management.Modal = "viewer"
	case operationMsg:
		m.busy = false
		m.view = msg.origin
		if !m.setupOperationPending || (msg.err == nil && !msg.failed) {
			m.setupRetry = nil
		}
		m.setupOperationPending = false
		m.showOperationResult(msg)
		return m, m.load()
	case settingsSavedMsg:
		m.busy = false
		if msg.err != nil {
			m.output = m.cleanOutput(msg.err.Error())
			return m, nil
		}
		m.navigate("Catalog")
		m.output = "Default named agents saved for future MCP installs"
		return m, m.load()
	case tea.MouseMsg:
		if m.result != nil {
			return m, m.resultMouse(msg)
		}
		if m.view == "Catalog" && !m.busy {
			return m, m.homeMouse(msg)
		}
		if isManagementView(m.view) && !m.busy {
			return m, m.managementMouse(msg)
		}
	case tea.KeyPressMsg:
		stroke := msg.String()
		if m.result != nil {
			return m, m.resultKey(stroke)
		}
		if m.busy {
			if stroke == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.view == "Catalog" {
			return m, m.homeKey(stroke)
		}
		if isManagementView(m.view) {
			return m, m.managementKey(stroke)
		}
		switch stroke {
		case "esc":
			m.navigate("Catalog")
		case "f10", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = min(max(0, len(m.rows())-1), m.selected+1)
		case "f9", "m":
			m.navigate("Catalog")
			m.home.Modal = &modalState{Kind: "main"}
		case "f5", "R":
			return m, m.load()
		default:
			return m, m.handleAction(stroke)
		}
	}
	return m, nil
}
func (m *Model) navigate(view string) {
	if view == "Help" && m.view != "Help" {
		m.management.HelpOrigin = m.view
		m.management.HelpSelected = m.selected
		m.view = view
		m.selected = 0
		m.output = ""
		m.management.Modal = ""
		return
	}
	m.view = view
	m.selected = 0
	m.output = ""
	m.management.Modal = ""
	m.management.Focus = CapabilitiesPane
	m.management.EnvironmentIndex = 0
	m.management.TargetIndex = 0
	if view == "Settings" {
		m.initSettingsDraft()
	}
	if view == "Catalog" {
		m.reconcileHome()
	}
}
func (m *Model) label(source string) string {
	if label := m.sourceLabels[source]; label != "" {
		return label
	}
	if source == "" {
		return "current catalog"
	}
	return source
}
func (m *Model) rows() []string {
	rows := []string{}
	switch m.view {
	case "Catalog":
		for _, p := range m.catalog {
			name := p.Name
			if name == "" {
				name = p.ID
			}
			components := []string{}
			if p.Skill != nil {
				components = append(components, "skill")
			}
			if p.HasMCP() {
				components = append(components, "MCP")
			}
			rows = append(rows, name+" ["+strings.Join(components, " + ")+"] · "+m.label(m.sourceLabels[p.Dir]))
		}
		for _, inst := range m.inventory {
			if inst.Component == "skill" {
				rows = append(rows, "Installed "+inst.Key.Package+" · "+inst.AgentID+" · "+m.label(inst.Key.Source)+" · "+inst.Key.Environment+"/"+inst.Key.Target)
			}
		}
	case "MCPs":
		for _, inst := range m.mcps {
			rows = append(rows, inst.Name+" · "+inst.Status+" · "+m.label(inst.Key.Source)+" · "+inst.Key.Environment+"/"+inst.Key.Target+"\n    "+inst.URL)
		}
	case "Agents":
		for _, agent := range m.agents {
			rows = append(rows, agent)
		}
	case "Environments":
		rows = append(rows, "Environment root: "+m.environmentRoot(), "Target browsing awaits the environment service.")
	case "Help":
		rows = append(rows, "Tab / Left / Right: focus home panes", "Up / Down / PageUp / PageDown / Home / End: select", "Enter / F2: Actions; Actions includes Details and Parameters", "m / F9: Main menu", "Mouse click/wheel: select/scroll", "Esc: Back; F10: Quit")
	case "Settings":
		rows = append(rows, "Environment root: "+m.environmentRoot())
		keys := []string{}
		for key := range m.settings {
			if key != "environment_root" && key != "environment-root" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, key+": "+m.settings[key])
		}
	}
	return rows
}
func (m *Model) handleAction(stroke string) tea.Cmd {
	switch m.view {
	case "Catalog":
		if m.selected < len(m.catalog) {
			if stroke == "a" || stroke == "s" {
				p := m.catalog[m.selected]
				if !p.HasMCP() {
					m.output = "This package has no MCP server to authenticate or start."
					return nil
				}
				action := "authenticate"
				if stroke == "s" {
					action = "start"
				}
				m.pending = operation{action: action, source: m.sourceLabels[p.Dir], packageID: p.ID}
				m.contextForm()
				return nil
			}
			if stroke == "enter" || stroke == "i" {
				p := m.catalog[m.selected]
				m.pending = operation{action: "install", source: m.sourceLabels[p.Dir], packageID: p.ID}
				m.contextForm()
				return nil
			}
		} else {
			installs := []state.Installation{}
			for _, inst := range m.inventory {
				if inst.Component == "skill" {
					installs = append(installs, inst)
				}
			}
			index := m.selected - len(m.catalog)
			if index >= 0 && index < len(installs) && (stroke == "u" || stroke == "delete") {
				inst := installs[index]
				return m.run(operation{action: "uninstall", source: inst.Key.Source, packageID: inst.Key.Package, agent: inst.AgentID, environment: inst.Key.Environment, target: inst.Key.Target})
			}
		}
	case "MCPs":
		if m.selected < len(m.mcps) {
			inst := m.mcps[m.selected]
			actions := map[string]string{"enter": "status", "s": "start", "x": "stop", "a": "authenticate", "l": "logs"}
			if action := actions[stroke]; action != "" {
				if inst.Status == "external" && action != "status" {
					m.output = "External registrations support status; use profile Actions to configure registrations."
					return nil
				}
				op := operation{action: action, source: inst.Key.Source, packageID: inst.Key.Package, profile: inst.Key.Profile, environment: inst.Key.Environment, target: inst.Key.Target}
				return m.run(op)
			}
		}
	case "Agents":
		if m.selected < len(m.agents) && stroke == "enter" {
			return m.run(operation{action: "agent-info", agent: m.agents[m.selected]})
		}
	case "Settings":
		if m.selected == 0 && stroke == "enter" {
			m.pending = operation{action: "set-environment-root"}
			m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Environment root", Type: "directory", Required: true}}, map[string]any{"root": m.environmentRoot()})
			m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		}
	}
	return nil
}
func (m *Model) contextForm() {
	allowAll := false
	for _, p := range m.catalog {
		source := m.sourceLabels[p.Dir]
		if source == "" {
			source = m.settings["source"]
		}
		if p.ID == m.pending.packageID && source == m.pending.source && p.Skill != nil && !p.HasMCP() {
			allowAll = true
			break
		}
	}
	choices := []catalog.Choice{}
	if allowAll {
		choices = append(choices, catalog.Choice{Value: "all", Label: "All — ~/.agents/skills"})
	}
	for _, agent := range m.agents {
		if strings.EqualFold(agent, "all") {
			continue
		}
		choices = append(choices, catalog.Choice{Value: agent, Label: agent})
	}
	selected := []string{}
	if allowAll {
		selected = []string{"all"}
	} else {
		for _, configured := range strings.Split(m.settings["default_agents"], ",") {
			candidate := strings.TrimSpace(configured)
			for _, choice := range choices {
				if candidate == choice.Value {
					present := false
					for _, old := range selected {
						if old == choice.Value {
							present = true
						}
					}
					if !present {
						selected = append(selected, choice.Value)
					}
					break
				}
			}
		}
	}
	if len(selected) == 0 && len(choices) > 0 {
		selected = append(selected, choices[0].Value)
	}

	target := m.pending.target
	if m.pending.environment == "" && target == "default" {
		target = ""
	}
	defs := []catalog.Input{{Name: "environment", Label: "Environment (optional)", Type: "string"}, {Name: "target", Label: "Target (optional)", Type: "string"}}
	prefill := map[string]any{"environment": m.pending.environment, "target": target}
	if m.pending.action == "install" || m.pending.action == "uninstall" {
		defs = append([]catalog.Input{{Name: "agent", Label: "Agents", Type: "multichoice", Options: choices, Required: true}}, defs...)
		prefill["agent"] = selected
	}
	m.form = forms.NewForm(m.ctx, defs, prefill)
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}
func (m *Model) run(op operation) tea.Cmd {
	m.pending = op
	m.busy = true
	m.action = op.action
	origin := m.view
	runner := &operationExec{ctx: m.ctx, backend: m.backend, op: op}
	return tea.Exec(runner, func(e error) tea.Msg { return operationMsg{origin: origin, output: runner.output, err: e} })
}

type operationExec struct {
	ctx     context.Context
	backend Backend
	op      operation
	output  string
}

func (e *operationExec) Run() error {
	output, err := e.backend.UIRun(e.ctx, e.op.action, e.op.source, e.op.packageID, e.op.profile, e.op.agent, e.op.environment, e.op.target)
	e.output = output
	return err
}
func (e *operationExec) SetStdin(io.Reader)  {}
func (e *operationExec) SetStdout(io.Writer) {}
func (e *operationExec) SetStderr(io.Writer) {}
func (m *Model) cleanOutput(output string) string {
	for _, p := range m.catalog {
		for _, input := range p.Inputs {
			if input.Type == "secret" {
				if value, ok := input.Default.(string); ok && value != "" {
					output = strings.ReplaceAll(output, value, "[redacted]")
				}
			}
		}
	}
	return output
}

func (m *Model) setupOverlayBounds() (x, y, width, height int, ok bool) {
	if m.pendingSetup == nil || m.view != "Catalog" || m.width < 80 || m.height < 16 {
		return 0, 0, 0, 0, false
	}
	return 4, 4, m.width - 8, m.height - 6, true
}

func (m *Model) setupOverlayView() tea.View {
	x, y, width, height, _ := m.setupOverlayBounds()
	base := strings.Split(m.homeView().Content, "\n")
	overlay := strings.Split(m.form.View().Content, "\n")
	for row := 0; row < height && row < len(overlay) && y+row < len(base); row++ {
		line := base[y+row]
		base[y+row] = ansi.Cut(line, 0, x) + fit(overlay[row], width) + ansi.Cut(line, x+width, m.width)
	}
	v := tea.NewView(navyCanvas(strings.Join(base, "\n")))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) View() tea.View {
	if m.result != nil {
		return m.resultView()
	}
	if m.form == nil && (m.view == "Catalog" || m.width < 80 || m.height < 16) {
		return m.homeView()
	}
	if m.form != nil {
		if _, _, _, _, ok := m.setupOverlayBounds(); ok {
			return m.setupOverlayView()
		}
		return m.form.View()
	}
	if isManagementView(m.view) {
		return m.managementView()
	}
	title := lipgloss.NewStyle().Bold(true).Render("AACT · " + m.view)
	nav := "Esc Back · m Main menu"
	rows := m.rows()
	if len(rows) == 0 {
		rows = []string{"No entries"}
	}
	maxRows := m.height - 10
	if maxRows < 1 {
		maxRows = 1
	}
	start := 0
	if m.selected >= maxRows {
		start = m.selected - maxRows + 1
	}
	end := start + maxRows
	if end > len(rows) {
		end = len(rows)
	}
	shown := []string{}
	for i := start; i < end; i++ {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		shown = append(shown, prefix+rows[i])
	}
	footer := "↑↓ Select · Esc Back · F5 Refresh · F10 Quit"
	switch m.view {
	case "Catalog":
		footer += "\nEnter install · a authenticate MCP · s start MCP · u uninstall installed skill"
	case "MCPs":
		if m.selected < len(m.mcps) && m.mcps[m.selected].Status == "external" {
			footer += "\nEnter status · registration changes available from home Actions"
		} else {
			footer += "\nEnter status · s start · x stop · a authenticate · l logs"
		}
	case "Agents":
		footer += "\nEnter agent details"
	case "Settings":
		footer += "\nEnter edit environment root"
	}
	status := m.output
	if m.busy {
		status = "Running " + m.action + "…"
	}
	content := fmt.Sprintf("%s\n%s\n\n%s\n\n%s\n\n%s", title, nav, strings.Join(shown, "\n"), status, footer)
	width := m.width
	if width < 20 {
		width = 20
	}
	return tea.NewView(lipgloss.NewStyle().MaxWidth(width).Render(content))
}
