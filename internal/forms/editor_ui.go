package forms

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrNotSubmitted = errors.New("form has not been submitted")

type FormModel struct {
	ctx                         context.Context
	editor                      *Editor
	defs                        []catalog.Input
	selected, width, height     int
	editing                     bool
	buffer, editAction, message string
	choiceIndex, rowIndex       map[string]int
	done                        bool
	result                      map[string]any
	err                         error
}

func NewForm(ctx context.Context, defs []catalog.Input, prefill map[string]any) *FormModel {
	return &FormModel{ctx: ctx, editor: NewEditor(defs, prefill), defs: append([]catalog.Input{}, defs...), width: 80, height: 24, choiceIndex: map[string]int{}, rowIndex: map[string]int{}}
}
func (m *FormModel) Init() tea.Cmd { return nil }
func (m *FormModel) Result() (map[string]any, error) {
	if !m.done {
		return nil, ErrNotSubmitted
	}
	return copyAnswers(m.result), m.err
}
func RunEditor(ctx context.Context, defs []catalog.Input, prefill map[string]any) (map[string]any, error) {
	model := NewForm(ctx, defs, prefill)
	result, e := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	if e != nil {
		if ctx.Err() != nil || errors.Is(e, tea.ErrProgramKilled) {
			return nil, picker.ErrCancelled
		}
		return nil, e
	}
	return result.(*FormModel).Result()
}

type pickedMsg struct {
	name, path, action string
	index              int
	err                error
}

func (m *FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case pickedMsg:
		if errors.Is(msg.err, picker.ErrCancelled) {
			m.message = "Selection cancelled"
			return m, nil
		}
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
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
	case tea.MouseMsg:
		return m.mouseUpdate(msg)
	case tea.KeyPressMsg:
		stroke := msg.String()
		if stroke == "ctrl+c" || stroke == "esc" && !m.editing {
			m.editor.Cancel()
			m.done = true
			m.err = picker.ErrCancelled
			return m, tea.Quit
		}
		if stroke == "ctrl+s" && !m.editing {
			values, e := m.editor.Commit()
			if e != nil {
				m.message = e.Error()
				return m, nil
			}
			m.done = true
			m.result = values
			return m, tea.Quit
		}
		if m.editing {
			switch stroke {
			case "esc":
				m.editing = false
				m.message = "Edit cancelled"
			case "ctrl+u":
				m.buffer = ""
			case "backspace":
				_, size := utf8.DecodeLastRuneInString(m.buffer)
				if size > 0 {
					m.buffer = m.buffer[:len(m.buffer)-size]
				}
			case "enter":
				m.commitBuffer()
			default:
				if msg.Text != "" {
					m.buffer += msg.Text
				}
			}
			return m, nil
		}
		if len(m.defs) == 0 {
			return m, nil
		}
		def := m.defs[m.selected]
		values := m.editor.Values()
		value := values[def.Name]
		switch stroke {
		case "tab", "down":
			m.selected = (m.selected + 1) % len(m.defs)
			m.message = ""
		case "shift+tab", "up":
			m.selected = (m.selected + len(m.defs) - 1) % len(m.defs)
			m.message = ""
		case "left", "right":
			if len(def.Options) > 0 {
				delta := 1
				if stroke == "left" {
					delta = -1
				}
				m.choiceIndex[def.Name] = (m.choiceIndex[def.Name] + len(def.Options) + delta) % len(def.Options)
			}
		case "[", "]":
			if paths := collectionRows(value); len(paths) > 0 {
				delta := 1
				if stroke == "[" {
					delta = -1
				}
				m.rowIndex[def.Name] = (m.rowIndex[def.Name] + len(paths) + delta) % len(paths)
			}
		case "r":
			if scalarDefinition(def).Multiple {
				m.setError(m.editor.RemoveValue(def.Name, m.rowIndex[def.Name]))
				m.rowIndex[def.Name] = 0
			}
		case "a":
			if def.Multiple && (def.Type == "directory" || def.Type == "file") {
				return m, m.pick(def, "add", "")
			} else if def.Multiple && len(def.Options) == 0 {
				m.beginEdit("add", "")
			}
		case "A":
			if def.Multiple && len(def.Options) == 0 {
				m.beginEdit("add", "")
			}
		case "e":
			if def.Multiple && (def.Type == "directory" || def.Type == "file") {
				if paths, ok := value.([]string); ok && len(paths) > 0 {
					index := m.rowIndex[def.Name] % len(paths)
					return m, m.pick(def, "edit", paths[index])
				}
			} else if def.Multiple && len(def.Options) == 0 {
				if rows := collectionRows(value); len(rows) > 0 {
					m.beginEdit("edit", textValue(rows[m.rowIndex[def.Name]%len(rows)]))
				}
			}
		case "m":
			if def.Type == "directory" || def.Type == "file" {
				if def.Multiple {
					if paths, ok := value.([]string); ok && len(paths) > 0 {
						index := m.rowIndex[def.Name] % len(paths)
						m.beginEdit("edit", paths[index])
					} else {
						m.beginEdit("add", "")
					}
				} else {
					m.beginEdit("apply", textValue(value))
				}
			}
		case "enter", "space", " ":
			if def.Type == "boolean" && !def.Multiple {
				current, _ := value.(bool)
				m.setError(m.editor.Apply(def.Name, !current))
			} else if len(def.Options) > 0 {
				m.choose(def, value)
			} else if def.Type == "directory" || def.Type == "file" {
				if def.Multiple {
					return m, m.pick(def, "add", "")
				}
				return m, m.pick(def, "apply", textValue(value))
			} else if def.Multiple {
				m.beginEdit("add", "")
			} else {
				m.beginEdit("apply", textValue(value))
			}
		}
	}
	return m, nil
}
func (m *FormModel) beginEdit(action, buffer string) {
	m.editing = true
	m.editAction = action
	m.buffer = buffer
	m.message = ""
}
func (m *FormModel) commitBuffer() {
	def := m.defs[m.selected]
	var e error
	switch m.editAction {
	case "add":
		e = m.editor.AddValue(def.Name, m.buffer)
	case "edit":
		e = m.editor.EditValue(def.Name, m.rowIndex[def.Name], m.buffer)
	default:
		e = m.editor.Apply(def.Name, m.buffer)
	}
	m.setError(e)
	if e == nil {
		m.editing = false
	}
}
func (m *FormModel) choose(def catalog.Input, current any) {
	option := def.Options[m.choiceIndex[def.Name]%len(def.Options)].Value
	if def.Multiple || def.Type == "multichoice" || def.Type == "multiple-choice" {
		values, _ := current.([]string)
		next := []string{}
		found := false
		for _, value := range values {
			if value == option {
				found = true
			} else {
				next = append(next, value)
			}
		}
		if !found {
			next = append(next, option)
		}
		m.setError(m.editor.Apply(def.Name, next))
	} else {
		m.setError(m.editor.Apply(def.Name, option))
	}
}
func (m *FormModel) setError(e error) {
	m.message = ""
	if e != nil {
		m.message = e.Error()
	}
}
func textValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
func (m *FormModel) pick(def catalog.Input, action, initial string) tea.Cmd {
	runner := &pickerExec{ctx: m.ctx, kind: def.Type, initial: initial}
	index := m.rowIndex[def.Name]
	return tea.Exec(runner, func(e error) tea.Msg {
		return pickedMsg{name: def.Name, path: runner.path, action: action, index: index, err: e}
	})
}

func (m *FormModel) mouseUpdate(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if _, ok := msg.(tea.MouseWheelMsg); ok {
		if len(m.defs) == 0 || m.editing {
			return m, nil
		}
		switch mouse.Button {
		case tea.MouseWheelDown:
			m.selected = min(len(m.defs)-1, m.selected+1)
		case tea.MouseWheelUp:
			m.selected = max(0, m.selected-1)
		}
		return m, nil
	}
	if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft {
		return m, nil
	}
	layout := m.layout()
	if mouse.Y == layout.footerY {
		if mouse.X >= 0 && mouse.X < len("[ Save ]") {
			if m.editing {
				m.commitBuffer()
				if m.editing {
					return m, nil
				}
			}
			return m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		}
		cancelStart := len("[ Save ]  ")
		if mouse.X >= cancelStart && mouse.X < cancelStart+len("[ Cancel ]") {
			m.editor.Cancel()
			m.done = true
			m.err = picker.ErrCancelled
			return m, tea.Quit
		}
	}
	bodyY := mouse.Y - 2
	if bodyY < 0 || bodyY >= len(layout.visibleFields) {
		return m, nil
	}
	field := layout.visibleFields[bodyY]
	if field < 0 {
		return m, nil
	}
	if m.editing {
		m.commitBuffer()
		if m.editing {
			return m, nil
		}
	}
	m.selected = field
	m.message = ""
	if row := layout.visibleRows[bodyY]; row >= 0 {
		m.rowIndex[m.defs[field].Name] = row
		return m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	}
	return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
}

type pickerExec struct {
	ctx                 context.Context
	kind, initial, path string
	in                  io.Reader
	out, err            io.Writer
}

func (e *pickerExec) Run() error {
	path, err := (picker.Picker{Native: picker.Native, Reader: e.in, Writer: e.out}).Select(e.ctx, e.kind, e.initial)
	e.path = path
	return err
}
func (e *pickerExec) SetStdin(in io.Reader)   { e.in = in }
func (e *pickerExec) SetStdout(out io.Writer) { e.out = out }
func (e *pickerExec) SetStderr(out io.Writer) { e.err = out }

type formLayout struct {
	content       string
	visibleFields []int
	visibleRows   []int
	footerY       int
}

func (m *FormModel) layout() formLayout {
	position := ""
	if len(m.defs) > 0 {
		position = fmt.Sprintf(" (%d/%d)", m.selected+1, len(m.defs))
	}
	title := lipgloss.NewStyle().Bold(true).Render("Edit package inputs" + position)
	rows := []string{}
	values := m.editor.Values()
	for i, def := range m.defs {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		label := def.Label
		if label == "" {
			label = def.Name
		}
		if def.Required {
			label += " *"
		}
		value := values[def.Name]
		display := textValue(value)
		if def.Type == "secret" && display != "" {
			display = "••••••••"
		}
		if paths := collectionRows(value); scalarDefinition(def).Multiple && len(def.Options) == 0 {
			display = fmt.Sprintf("%d items", len(paths))
			for index, path := range paths {
				mark := " "
				if i == m.selected && index == m.rowIndex[def.Name] {
					mark = ">"
				}
				row := textValue(path)
				if def.Type == "secret" {
					row = "••••••••"
				}
				display += "\n    " + mark + " " + row
			}
		}
		if i == m.selected && len(def.Options) > 0 {
			option := def.Options[m.choiceIndex[def.Name]%len(def.Options)]
			labelValue := option.Label
			if labelValue == "" {
				labelValue = option.Value
			}
			display += "  [" + labelValue + "]"
		}
		rows = append(rows, prefix+label+": "+display)
	}
	selectedLine := 0
	bodyLines := []string{}
	fieldLines := []int{}
	rowLines := []int{}
	for i, row := range rows {
		if i == m.selected {
			selectedLine = len(bodyLines)
		}
		for part, line := range strings.Split(row, "\n") {
			bodyLines = append(bodyLines, line)
			fieldLines = append(fieldLines, i)
			rowLines = append(rowLines, part-1)
		}
	}
	if len(bodyLines) == 0 {
		bodyLines = []string{"No inputs"}
		fieldLines = []int{-1}
		rowLines = []int{-1}
	}
	editInfo := ""
	if m.editing {
		buffer := m.buffer
		if m.defs[m.selected].Type == "secret" {
			buffer = strings.Repeat("•", utf8.RuneCountInString(buffer))
		}
		editInfo = "\n\nEdit: " + buffer + "_\nEnter applies; Ctrl+U clears; Esc cancels edit"
	}
	footer := "[ Save ]  [ Cancel ]\nTab/↑↓ field · Enter edit · ←→ choice · Space toggle · Ctrl+S save · Esc cancel"
	if len(m.defs) > 0 {
		def := m.defs[m.selected]
		if def.Multiple && len(def.Options) == 0 && def.Type != "directory" && def.Type != "file" {
			footer += "\na Add · e Edit · r Remove · [/] row"
		}
		if def.Type == "directory" || def.Type == "file" {
			if def.Multiple {
				footer += "\na Add · e Edit · r Remove · [/] row · A manual Add · m manual Edit"
			} else {
				footer += "\nm enter path manually"
			}
		}
	}
	footerLines := len(strings.Split(footer, "\n"))
	editLines := 0
	if editInfo != "" {
		editLines = len(strings.Split(editInfo, "\n")) - 1
	}
	visible := max(1, m.height-4-footerLines-editLines)
	start := max(0, selectedLine-visible+1)
	start = min(start, max(0, len(bodyLines)-visible))
	end := min(len(bodyLines), start+visible)
	body := strings.Join(bodyLines[start:end], "\n")
	content := title + "\n\n" + body + editInfo + "\n\n" + m.message + "\n" + footer
	footerY := -1
	for y, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "[ Save ]  [ Cancel ]") {
			footerY = y
			break
		}
	}
	return formLayout{content: content, visibleFields: fieldLines[start:end], visibleRows: rowLines[start:end], footerY: footerY}
}

func (m *FormModel) View() tea.View {
	content := m.layout().content
	width := m.width
	if width < 20 {
		width = 20
	}
	view := tea.NewView(lipgloss.NewStyle().MaxWidth(width).Render(content))
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
