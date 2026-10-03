package forms

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
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
	title                       string
	contextLine                 string
	selected, width, height     int
	editing                     bool
	buffer, editAction, message string
	cursor                      int
	choiceIndex, rowIndex       map[string]int
	hints                       map[string]string
	conditions                  map[string]fieldCondition
	disabled                    map[string]string
	done                        bool
	result                      map[string]any
	err                         error
}
type fieldCondition struct{ Selector, Choice string }

func NewForm(ctx context.Context, defs []catalog.Input, prefill map[string]any) *FormModel {
	m := &FormModel{ctx: ctx, editor: NewEditor(defs, prefill), defs: append([]catalog.Input{}, defs...), title: "Edit package inputs", width: 80, height: 24, choiceIndex: map[string]int{}, rowIndex: map[string]int{}, hints: map[string]string{}, conditions: map[string]fieldCondition{}, disabled: map[string]string{}}
	values := m.editor.Values()
	for _, def := range defs {
		if def.Multiple || def.Type == "multichoice" || def.Type == "multiple-choice" {
			continue
		}
		for i, option := range def.Options {
			if values[def.Name] == option.Value {
				m.choiceIndex[def.Name] = i
				break
			}
		}
	}
	return m
}
func (m *FormModel) SetTitle(title string)     { m.title = title }
func (m *FormModel) SetHint(name, hint string) { m.hints[name] = hint }
func (m *FormModel) SetContext(line string)    { m.contextLine = line }
func (m *FormModel) SetConditional(name, selector, choice string) {
	m.conditions[name] = fieldCondition{Selector: selector, Choice: choice}
	m.clearInactiveForSelector(selector)
}
func (m *FormModel) SetDisabled(name, reason string) {
	m.disabled[name] = reason
	m.setError(m.editor.Apply(name, ""))
	if m.selected < len(m.defs) && m.defs[m.selected].Name == name {
		m.moveFocus(1)
	}
}
func (m *FormModel) clearInactiveForSelector(selector string) {
	selected, _ := m.editor.Values()[selector].(string)
	for name, condition := range m.conditions {
		if condition.Selector == selector && condition.Choice != selected {
			m.setError(m.editor.Apply(name, ""))
		}
	}
}
func (m *FormModel) disabledReason(name string) string {
	if reason := m.disabled[name]; reason != "" {
		return reason
	}
	condition, ok := m.conditions[name]
	if !ok {
		return ""
	}
	selected, _ := m.editor.Values()[condition.Selector].(string)
	if selected == condition.Choice {
		return ""
	}
	for _, def := range m.defs {
		if def.Name != condition.Selector {
			continue
		}
		for _, option := range def.Options {
			if option.Value == selected {
				label := option.Label
				if label == "" {
					label = selected
				}
				return "inactive while " + label + " is selected"
			}
		}
	}
	return "inactive for the selected method"
}
func (m *FormModel) moveFocus(delta int) {
	count := len(m.defs) + 2
	for step := 0; step < count; step++ {
		m.selected = (m.selected + count + delta) % count
		if m.selected >= len(m.defs) || m.disabledReason(m.defs[m.selected].Name) == "" {
			break
		}
	}
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
		if err == nil && msg.action == "add" {
			m.rowIndex[msg.name] = len(collectionRows(m.editor.Values()[msg.name])) - 1
		}
	case tea.MouseMsg:
		return m.mouseUpdate(msg)
	case tea.KeyPressMsg:
		stroke := msg.String()
		if stroke == "ctrl+c" {
			return m.cancel()
		}
		if stroke == "esc" && !m.editing {
			return m.cancel()
		}
		if stroke == "ctrl+s" {
			if m.editing {
				m.commitBuffer()
				if m.editing {
					return m, nil
				}
			}
			return m.save()
		}
		if m.editing {
			switch stroke {
			case "esc":
				m.editing = false
				m.message = "Edit cancelled"
			case "ctrl+u":
				m.buffer = ""
				m.cursor = 0
			case "left":
				m.cursor = max(0, m.cursor-1)
			case "right":
				m.cursor = min(utf8.RuneCountInString(m.buffer), m.cursor+1)
			case "home":
				m.cursor = 0
			case "end":
				m.cursor = utf8.RuneCountInString(m.buffer)
			case "backspace":
				if m.cursor > 0 {
					runes := []rune(m.buffer)
					m.buffer = string(append(runes[:m.cursor-1], runes[m.cursor:]...))
					m.cursor--
				}
			case "delete":
				runes := []rune(m.buffer)
				if m.cursor < len(runes) {
					m.buffer = string(append(runes[:m.cursor], runes[m.cursor+1:]...))
				}
			case "enter":
				m.commitBuffer()
			case "tab", "down", "up", "shift+tab":
				m.commitBuffer()
				if !m.editing {
					delta := 1
					if stroke == "up" || stroke == "shift+tab" {
						delta = -1
					}
					m.moveFocus(delta)
				}
			default:
				if msg.Text != "" {
					runes := []rune(m.buffer)
					m.buffer = string(runes[:m.cursor]) + msg.Text + string(runes[m.cursor:])
					m.cursor += utf8.RuneCountInString(msg.Text)
				}
			}
			return m, nil
		}
		switch stroke {
		case "down":
			if m.selected < len(m.defs) {
				def := m.defs[m.selected]
				if (def.Multiple || def.Type == "multichoice" || def.Type == "multiple-choice") && len(def.Options) > 0 && m.choiceIndex[def.Name] < len(def.Options)-1 {
					m.choiceIndex[def.Name]++
					return m, nil
				}
			}
			m.moveFocus(1)
			m.message = ""
			return m, nil
		case "tab":
			m.moveFocus(1)
			m.message = ""
			return m, nil
		case "up":
			if m.selected < len(m.defs) {
				def := m.defs[m.selected]
				if (def.Multiple || def.Type == "multichoice" || def.Type == "multiple-choice") && len(def.Options) > 0 && m.choiceIndex[def.Name] > 0 {
					m.choiceIndex[def.Name]--
					return m, nil
				}
			}
			m.moveFocus(-1)
			m.message = ""
			return m, nil
		case "shift+tab":
			m.moveFocus(-1)
			m.message = ""
			return m, nil
		}
		if m.selected >= len(m.defs) {
			if stroke == "enter" {
				if m.selected == len(m.defs) {
					return m.save()
				}
				m.editor.Cancel()
				m.done = true
				m.err = picker.ErrCancelled
				return m, tea.Quit
			}
			return m, nil
		}
		def := m.defs[m.selected]
		if reason := m.disabledReason(def.Name); reason != "" {
			m.message = reason
			return m, nil
		}
		values := m.editor.Values()
		value := values[def.Name]
		switch stroke {
		case "left", "right":
			if len(def.Options) > 0 {
				delta := 1
				if stroke == "left" {
					delta = -1
				}
				m.choiceIndex[def.Name] = (m.choiceIndex[def.Name] + len(def.Options) + delta) % len(def.Options)
				if !def.Multiple && def.Type != "multichoice" && def.Type != "multiple-choice" {
					m.choose(def, value)
				}
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
			} else if def.OptionsFrom != "" && len(def.Options) == 0 {
				m.message = "No choices available in selected target"
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

func (m *FormModel) cancel() (tea.Model, tea.Cmd) {
	m.editor.Cancel()
	m.done = true
	m.err = picker.ErrCancelled
	return m, tea.Quit
}

func (m *FormModel) save() (tea.Model, tea.Cmd) {
	values, err := m.editor.Commit()
	if err != nil {
		m.message = err.Error()
		for i, def := range m.defs {
			if m.editor.initialErrors[def.Name] != nil || Validate([]catalog.Input{def}, m.editor.Values()) != nil {
				m.selected = i
				break
			}
		}
		return m, nil
	}
	m.done = true
	m.result = values
	return m, tea.Quit
}
func (m *FormModel) beginEdit(action, buffer string) {
	m.editing = true
	m.editAction = action
	m.buffer = buffer
	m.cursor = utf8.RuneCountInString(buffer)
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
		if m.editAction == "add" {
			m.rowIndex[def.Name] = len(collectionRows(m.editor.Values()[def.Name])) - 1
		}
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
		err := m.editor.Apply(def.Name, option)
		m.setError(err)
		if err == nil {
			m.clearInactiveForSelector(def.Name)
		}
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
		if mouse.X >= 1 && mouse.X < 1+len("[ Save ]") {
			if m.editing {
				m.commitBuffer()
				if m.editing {
					return m, nil
				}
			}
			return m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		}
		cancelStart := 1 + len("[ Save ]  ")
		if mouse.X >= cancelStart && mouse.X < cancelStart+len("[ Cancel ]") {
			return m.cancel()
		}
	}
	bodyY := mouse.Y - layout.bodyStart
	if bodyY < 0 || bodyY >= len(layout.visibleFields) {
		return m, nil
	}
	field := layout.visibleFields[bodyY]
	if field < 0 {
		return m, nil
	}
	if reason := m.disabledReason(m.defs[field].Name); reason != "" {
		m.message = reason
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
	if choice := layout.visibleChoices[bodyY]; choice >= 0 {
		m.choiceIndex[m.defs[field].Name] = choice
		return m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	}
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
	content        string
	visibleFields  []int
	visibleRows    []int
	visibleChoices []int
	footerY        int
	bodyStart      int
}

func (m *FormModel) layout() formLayout {
	position := ""
	if len(m.defs) > 0 && m.selected < len(m.defs) {
		position = fmt.Sprintf(" (%d/%d)", m.selected+1, len(m.defs))
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffe38a")).Render(m.title + position)
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
		if def.OptionsFrom != "" && len(def.Options) == 0 {
			display = "No choices available in selected target"
		} else if def.OptionsFrom != "" && len(def.Options) > 0 {
			selected, _ := value.([]string)
			if len(selected) == 0 {
				display = ""
			} else {
				display = fmt.Sprintf("%d selected", len(selected))
			}
		}
		if reason := m.disabledReason(def.Name); reason != "" {
			display += " — " + reason
		}
		if paths := collectionRows(value); scalarDefinition(def).Multiple && len(def.Options) == 0 {
			display = fmt.Sprintf("%d items", len(paths))
			for index, path := range paths {
				mark := " "
				if i == m.selected && index == m.rowIndex[def.Name] {
					mark = ">"
				}
				row := textValue(path)
				display += "\n    " + mark + " " + row
			}
		}
		if i == m.selected && len(def.Options) > 0 && def.Type != "multichoice" && def.Type != "multiple-choice" && !def.Multiple {
			option := def.Options[m.choiceIndex[def.Name]%len(def.Options)]
			labelValue := option.Label
			if labelValue == "" {
				labelValue = option.Value
			}
			display += "  [" + labelValue + "]"
		}
		rows = append(rows, prefix+label+": "+display)
		if len(def.Options) > 0 && (def.Type == "multichoice" || def.Type == "multiple-choice" || def.Multiple) {
			chosen, _ := value.([]string)
			for optionIndex, option := range def.Options {
				mark := "[ ]"
				for _, current := range chosen {
					if current == option.Value {
						mark = "[x]"
						break
					}
				}
				name := option.Label
				if name == "" {
					name = option.Value
				}
				pointer := " "
				if i == m.selected && optionIndex == m.choiceIndex[def.Name]%len(def.Options) {
					pointer = ">"
				}
				rows[len(rows)-1] += "\n    " + pointer + " " + mark + " " + name
			}
		}
	}
	selectedLine := 0
	bodyLines := []string{}
	fieldLines := []int{}
	rowLines := []int{}
	choiceLines := []int{}
	for i, row := range rows {
		if i == m.selected {
			selectedLine = len(bodyLines)
			if len(m.defs[i].Options) > 0 && (m.defs[i].Type == "multichoice" || m.defs[i].Type == "multiple-choice" || m.defs[i].Multiple) {
				selectedLine += 1 + m.choiceIndex[m.defs[i].Name]%len(m.defs[i].Options)
			}
			if paths := collectionRows(values[m.defs[i].Name]); scalarDefinition(m.defs[i]).Multiple && len(m.defs[i].Options) == 0 && len(paths) > 0 {
				selectedLine += 1 + min(m.rowIndex[m.defs[i].Name], len(paths)-1)
			}
		}
		for part, line := range strings.Split(row, "\n") {
			bodyLines = append(bodyLines, line)
			fieldLines = append(fieldLines, i)
			rowLines = append(rowLines, part-1)
			choice := -1
			if len(m.defs[i].Options) > 0 && (m.defs[i].Type == "multichoice" || m.defs[i].Type == "multiple-choice" || m.defs[i].Multiple) && part > 0 {
				choice = part - 1
			}
			choiceLines = append(choiceLines, choice)
		}
	}
	if len(bodyLines) == 0 {
		bodyLines = []string{"No inputs"}
		fieldLines = []int{-1}
		rowLines = []int{-1}
		choiceLines = []int{-1}
	}
	editInfo := ""
	if m.editing {
		runes := []rune(m.buffer)
		cursor := min(m.cursor, len(runes))
		editInfo = "\n\nEdit: " + string(runes[:cursor]) + "_" + string(runes[cursor:]) + "\nEnter applies; Ctrl+U clears; Esc cancels edit"
	}
	footer := "[ Save ]  [ Cancel ]\nTab/↑↓ field · Enter edit · ←→ choice · Space toggle\nCtrl+S save · Esc cancel"
	if len(m.defs) > 0 && m.selected < len(m.defs) {
		def := m.defs[m.selected]
		if def.OptionsFrom != "" && len(def.Options) > 0 {
			footer = "[ Save ]  [ Cancel ]\n↑↓ fields/choices · Tab next field · Space/Enter toggle\nCtrl+S save · Esc cancel"
		}
		if def.ExclusiveGroup != "" && m.disabledReason(def.Name) == "" {
			others := []string{}
			for _, other := range m.defs {
				if other.Name == def.Name || other.ExclusiveGroup != def.ExclusiveGroup {
					continue
				}
				name := other.Label
				if name == "" {
					name = other.Name
				}
				others = append(others, name)
			}
			if len(others) > 0 {
				footer = "Mutually exclusive with " + strings.Join(others, ", ") + "; entering a value clears those inputs\n" + footer
			}
		}
		if hint := m.hints[def.Name]; hint != "" {
			footer = "Source: " + hint + "\n" + footer
		}
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
		editLines = strings.Count(editInfo, "\n") + 1
	}
	contextLines := 0
	if m.contextLine != "" {
		contextLines = 1
	}
	visible := max(1, m.height-6-footerLines-editLines-contextLines)
	start := max(0, selectedLine-visible+1)
	if m.selected >= len(m.defs) {
		start = max(0, len(bodyLines)-visible)
	}
	start = min(start, max(0, len(bodyLines)-visible))
	end := min(len(bodyLines), start+visible)
	body := strings.Join(bodyLines[start:end], "\n")
	heading := title + "\n\n"
	if m.contextLine != "" {
		heading = title + "\n" + m.contextLine + "\n\n"
	}
	head := strings.Split(heading+body+editInfo, "\n")
	actions := "[ Save ]  [ Cancel ]"
	if m.selected == len(m.defs) {
		actions = "> [ Save ]  [ Cancel ]"
	}
	if m.selected == len(m.defs)+1 {
		actions = "[ Save ]  > [ Cancel ]"
	}
	footer = strings.Replace(footer, "[ Save ]  [ Cancel ]", actions, 1)
	feet := strings.Split(m.message+"\n"+footer, "\n")
	innerHeight := max(1, m.height-2)
	padding := max(1, innerHeight-len(head)-len(feet))
	framedRows := append([]string{}, head...)
	for i := 0; i < padding; i++ {
		framedRows = append(framedRows, "")
	}
	framedRows = append(framedRows, feet...)
	innerWidth := max(18, m.width-2)
	framed := []string{"╔" + strings.Repeat("═", innerWidth) + "╗"}
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#081f5b")).Background(lipgloss.Color("#e9f2fb"))
	for _, row := range framedRows {
		clipped := ansi.Truncate(row, innerWidth, "")
		padded := clipped + strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(clipped)))
		if strings.HasPrefix(row, "> ") {
			padded = selectedStyle.Render(padded)
		}
		framed = append(framed, "║"+padded+"║")
	}
	framed = append(framed, "╚"+strings.Repeat("═", innerWidth)+"╝")
	content := strings.Join(framed, "\n")
	footerY := -1
	for y, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "[ Save ]  [ Cancel ]") {
			footerY = y
			break
		}
	}
	return formLayout{content: content, visibleFields: fieldLines[start:end], visibleRows: rowLines[start:end], visibleChoices: choiceLines[start:end], footerY: footerY, bodyStart: 3 + contextLines}
}

func (m *FormModel) View() tea.View {
	content := m.layout().content
	width := m.width
	if width < 20 {
		width = 20
	}
	view := tea.NewView(navyCanvas(content))
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
