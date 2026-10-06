package forms

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
)

// pickerCommandParts keeps the terminal-releasing Bubble Tea exec wrapper and
// its result mapping together, while allowing interaction tests to run the
// exact command adapter with an instance-local native picker.
func (m *FormModel) pickerCommand(name, kind, action, initial string, index int) (*pickerExec, func(error) tea.Msg) {
	runner := &pickerExec{ctx: m.ctx, kind: kind, initial: initial, native: m.nativePicker}
	complete := func(err error) tea.Msg {
		return pickedMsg{name: name, path: runner.path, action: action, index: index, kind: kind, initial: initial, err: err}
	}
	return runner, complete
}

func (m *FormModel) applyPicked(msg pickedMsg) {
	if errors.Is(msg.err, picker.ErrCancelled) {
		return
	}
	if msg.err != nil {
		m.message = msg.err.Error()
		return
	}
	var err error
	switch msg.action {
	case "add":
		err = m.editor.AddPath(msg.name, msg.path)
	case "edit":
		err = m.editor.EditPath(msg.name, msg.index, msg.path)
	default:
		err = m.editor.Apply(msg.name, msg.path)
	}
	m.setError(err)
	if err == nil && msg.action != "add" {
		m.activateExclusive(msg.name)
	}
	if err == nil && msg.action == "add" {
		m.rowIndex[msg.name] = len(collectionRows(m.editor.Values()[msg.name])) - 1
	}
}

func (m *FormModel) pickerMetadata(msg pickedMsg) pickedMsg {
	for _, def := range m.defs {
		if def.Name != msg.name {
			continue
		}
		if msg.kind == "" {
			msg.kind = def.Type
		}
		if msg.initial == "" && msg.action != "add" {
			value := m.editor.Values()[def.Name]
			if isEditableCollection(def) && msg.action == "edit" {
				rows := collectionRows(value)
				index := msg.index
				if index < 0 || index >= len(rows) {
					index = m.rowIndex[def.Name]
				}
				if index >= 0 && index < len(rows) {
					msg.initial = textValue(rows[index])
				}
			} else {
				msg.initial = textValue(value)
			}
		}
		break
	}
	return msg
}

func (m *FormModel) resizeBrowser() {
	if m.browser == nil {
		return
	}
	width, height := max(1, m.width), max(1, m.height)
	innerWidth, innerHeight := width, height
	outerWidth := min(78, width-4)
	outerHeight := min(20, height-4)
	if outerWidth >= 32 && outerHeight >= 9 {
		innerWidth = outerWidth - 2
		innerHeight = outerHeight - 2
	}
	m.browser.Update(tea.WindowSizeMsg{Width: innerWidth, Height: innerHeight})
}

func (m *FormModel) pickerView() tea.View {
	base := m.layout().content
	if len(m.sections) > 0 {
		base = modalCanvas(base)
	} else {
		base = navyCanvas(base)
	}
	width, height := max(1, m.width), max(1, m.height)
	inner := m.browser.View().Content
	outerWidth := min(78, width-4)
	outerHeight := min(20, height-4)
	innerWidth := max(1, width)
	innerHeight := max(1, height)
	panel := inner
	x, y := 0, 0
	if outerWidth >= 32 && outerHeight >= 9 {
		innerWidth, innerHeight = outerWidth-2, outerHeight-2
		panel = lipgloss.NewStyle().Width(innerWidth).Height(innerHeight).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#8AB4F8")).
			Background(lipgloss.Color("#09266F")).Render(inner)
		x, y = (width-outerWidth)/2, (height-outerHeight)/2
	}
	if gotWidth := lipgloss.Width(panel); gotWidth > width {
		return tea.NewView(fmt.Sprintf("%s\n%s", base, inner))
	}
	compositor := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(panel).X(x).Y(y).Z(1),
	)
	view := tea.NewView(compositor.Render())
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
