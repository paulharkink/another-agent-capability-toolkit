package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
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
	UIRun(ctx context.Context, action, sourceID, packageID, agentID, environment, target string) (string, error)
}
type Model struct {
	backend                 Backend
	ctx                     context.Context
	view                    string
	selected, width, height int
	catalog                 []catalog.Package
	inventory               []state.Installation
	mcps                    []mcp.Instance
	agents                  []string
	settings, sourceLabels  map[string]string
	busy                    bool
	output, action          string
	form                    *forms.FormModel
	pending                 operation
}
type operation struct{ action, source, packageID, agent, environment, target string }
type loadedMsg struct {
	catalog          []catalog.Package
	inventory        []state.Installation
	mcps             []mcp.Instance
	agents           []string
	settings, labels map[string]string
	err              error
}
type operationMsg struct {
	origin, output string
	err            error
}

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
		if e != nil {
			errs = append(errs, e)
		}
		msg.mcps, e = m.backend.UIMCPs(m.ctx)
		if e != nil {
			errs = append(errs, e)
		}
		msg.agents, e = m.backend.UIAgents(m.ctx)
		if e != nil {
			errs = append(errs, e)
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
	}
	if m.form != nil {
		next, cmd := m.form.Update(msg)
		m.form = next.(*forms.FormModel)
		values, e := m.form.Result()
		if errors.Is(e, forms.ErrNotSubmitted) {
			return m, cmd
		}
		m.form = nil
		if errors.Is(e, picker.ErrCancelled) {
			m.output = "Cancelled"
			return m, nil
		}
		if e != nil {
			m.output = e.Error()
			return m, nil
		}
		op := m.pending
		if op.action == "set-environment-root" {
			op.target, _ = values["root"].(string)
		} else {
			op.agent, _ = values["agent"].(string)
			op.environment, _ = values["environment"].(string)
			op.target, _ = values["target"].(string)
		}
		return m, m.run(op)
	}
	switch msg := msg.(type) {
	case loadedMsg:
		m.catalog = msg.catalog
		m.inventory = msg.inventory
		m.mcps = msg.mcps
		m.agents = msg.agents
		m.settings = msg.settings
		m.sourceLabels = msg.labels
		if msg.err != nil {
			m.output = m.cleanOutput(msg.err.Error())
		}
		if m.selected >= len(m.rows()) {
			m.selected = 0
		}
	case operationMsg:
		m.busy = false
		m.view = msg.origin
		m.output = m.cleanOutput(msg.output)
		if msg.err != nil {
			if m.output != "" {
				m.output += "\n"
			}
			m.output += m.cleanOutput(msg.err.Error())
		}
		return m, m.load()
	case tea.KeyPressMsg:
		stroke := msg.String()
		if stroke == "q" || stroke == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		switch stroke {
		case "1":
			m.navigate("Catalog")
		case "2":
			m.navigate("MCPs")
		case "3":
			m.navigate("Agents")
		case "4":
			m.navigate("Settings")
		case "tab":
			views := []string{"Catalog", "MCPs", "Agents", "Settings"}
			for i, view := range views {
				if m.view == view {
					m.navigate(views[(i+1)%len(views)])
					break
				}
			}
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected+1 < len(m.rows()) {
				m.selected++
			}
		case "R":
			return m, m.load()
		default:
			return m, m.handleAction(stroke)
		}
	}
	return m, nil
}
func (m *Model) navigate(view string) { m.view = view; m.selected = 0; m.output = "" }
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
			if p.MCP != nil {
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
	case "Settings":
		rows = append(rows, "Environment root: "+m.settings["environment_root"])
		keys := []string{}
		for key := range m.settings {
			if key != "environment_root" {
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
			actions := map[string]string{"enter": "status", "s": "start", "x": "stop", "a": "authenticate", "l": "logs", "u": "uninstall"}
			if action := actions[stroke]; action != "" {
				return m.run(operation{action: action, source: inst.Key.Source, packageID: inst.Key.Package, environment: inst.Key.Environment, target: inst.Key.Target})
			}
		}
	case "Agents":
		if m.selected < len(m.agents) && stroke == "enter" {
			return m.run(operation{action: "agent-info", agent: m.agents[m.selected]})
		}
	case "Settings":
		if m.selected == 0 && stroke == "enter" {
			m.pending = operation{action: "set-environment-root"}
			m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Environment root", Type: "directory", Required: true}}, map[string]any{"root": m.settings["environment_root"]})
			m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		}
	}
	return nil
}
func (m *Model) contextForm() {
	choices := []catalog.Choice{}
	for _, agent := range m.agents {
		choices = append(choices, catalog.Choice{Value: agent, Label: agent})
	}
	agent := ""
	if len(m.agents) > 0 {
		agent = m.agents[0]
	}
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "agent", Label: "Agent", Type: "choice", Options: choices, Required: true}, {Name: "environment", Label: "Environment (optional)", Type: "string"}, {Name: "target", Label: "Target (optional)", Type: "string"}}, map[string]any{"agent": agent, "environment": "", "target": ""})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}
func (m *Model) run(op operation) tea.Cmd {
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
	output, err := e.backend.UIRun(e.ctx, e.op.action, e.op.source, e.op.packageID, e.op.agent, e.op.environment, e.op.target)
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
func (m *Model) View() tea.View {
	if m.form != nil {
		return m.form.View()
	}
	title := lipgloss.NewStyle().Bold(true).Render("AACT · " + m.view)
	nav := "1 Catalog · 2 MCPs · 3 Agents · 4 Settings"
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
	footer := "↑↓ select · Tab switch · R refresh · q quit"
	switch m.view {
	case "Catalog":
		footer += "\nEnter install · u uninstall installed skill"
	case "MCPs":
		footer += "\nEnter status · s start · x stop · a authenticate · l logs · u uninstall"
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
