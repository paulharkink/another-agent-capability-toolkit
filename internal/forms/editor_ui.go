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
	sections                    []FormSection
	sectionHeading              string
	sectionIndex                int
	area                        int
	actionIndex                 int
	exclusive                   map[string][]string
}

// FormSection groups fields into the left-hand navigation list of a split form.
// Forms without sections keep the legacy single-column interaction.
type FormSection struct {
	Title  string
	Fields []string
}

func (m *FormModel) SetSections(sections ...FormSection) {
	m.sections = append([]FormSection(nil), sections...)
	if len(m.sections) == 0 {
		m.sectionIndex = 0
		m.area = 0
		return
	}
	for i := range m.sections {
		m.sections[i].Fields = append([]string(nil), sections[i].Fields...)
	}
	m.sectionIndex = 0
	m.area = 0
	m.selectSectionField()
}

func (m *FormModel) SetSectionHeading(heading string) { m.sectionHeading = heading }

func (m *FormModel) SelectSection(title string) {
	for i, section := range m.sections {
		if section.Title == title {
			m.sectionIndex = i
			m.selectSectionField()
			return
		}
	}
}

// SetExclusiveFields makes edits to one named field clear the other fields in
// the group while leaving every field directly focusable.
func (m *FormModel) SetExclusiveFields(names ...string) {
	values := m.editor.Values()
	active := ""
	for _, name := range names {
		if value, ok := values[name].(string); ok && strings.TrimSpace(value) != "" {
			active = name
		}
	}
	for _, name := range names {
		if m.exclusive == nil {
			m.exclusive = map[string][]string{}
		}
		others := make([]string, 0, len(names)-1)
		for _, other := range names {
			if other != name {
				others = append(others, other)
			}
		}
		m.exclusive[name] = others
	}
	if active != "" {
		m.activateExclusive(active)
	}
}

func (m *FormModel) activateExclusive(name string) {
	for _, other := range m.exclusive[name] {
		m.setError(m.editor.Apply(other, ""))
	}
}

func (m *FormModel) displayHint(def catalog.Input, values map[string]any) string {
	hint := m.hints[def.Name]
	others, exclusive := m.exclusive[def.Name]
	if !exclusive {
		return hint
	}
	active := ""
	for _, name := range append([]string{def.Name}, others...) {
		if value, ok := values[name].(string); ok && strings.TrimSpace(value) != "" {
			active = name
			break
		}
	}
	if def.Type == "file" {
		if !strings.Contains(strings.ToLower(def.Name+" "+def.Label), "kubeconfig") {
			if active == def.Name {
				return "Active method"
			}
			if active == "" {
				return "Enter a path for " + def.Label
			}
			return "Type a path to switch to " + def.Label
		}
		switch active {
		case def.Name:
			return "Active method · imported, not a live path · Import source; AACT uses managed credential material at runtime"
		case "":
			return "Enter a source kubeconfig path · Import source; AACT uses managed credential material at runtime"
		default:
			return "Type a path to switch to kubeconfig · Import source; AACT uses managed credential material at runtime"
		}
	}
	if active == def.Name {
		return "Active method"
	}
	label := def.Label
	if label == "" {
		label = def.Name
	}
	if active == "" {
		return "Enter a " + label
	}
	return "Type here to switch to " + label
}

func (m *FormModel) splitFieldIndices(section int) []int {
	if section < 0 || section >= len(m.sections) {
		return nil
	}
	indices := make([]int, 0, len(m.sections[section].Fields))
	for _, name := range m.sections[section].Fields {
		for i, def := range m.defs {
			if def.Name == name && m.disabledReason(name) == "" {
				indices = append(indices, i)
				break
			}
		}
	}
	return indices
}

func (m *FormModel) selectSectionField() {
	indices := m.splitFieldIndices(m.sectionIndex)
	if len(indices) > 0 {
		m.selected = indices[0]
	}
}

func (m *FormModel) moveSection(delta int) {
	if len(m.sections) == 0 {
		return
	}
	m.sectionIndex = (m.sectionIndex + len(m.sections) + delta) % len(m.sections)
	m.message = ""
	m.selectSectionField()
}

func (m *FormModel) moveSplitControl(delta int) {
	indices := m.splitFieldIndices(m.sectionIndex)
	if len(indices) == 0 {
		return
	}
	if m.selected < len(m.defs) && isChoiceList(m.defs[m.selected]) {
		def := m.defs[m.selected]
		next := m.choiceIndex[def.Name] + delta
		if next >= 0 && next < len(def.Options) {
			m.choiceIndex[def.Name] = next
			return
		}
	}
	position := 0
	for i, index := range indices {
		if index == m.selected {
			position = i
			break
		}
	}
	next := position + delta
	if next < 0 {
		m.selected = indices[0]
		return
	}
	if next >= len(indices) {
		m.selected = indices[len(indices)-1]
		return
	}
	m.selected = indices[next]
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
	if m.selected < len(m.defs) {
		def := m.defs[m.selected]
		if isChoiceList(def) {
			m.choiceIndex[def.Name] = 0
			if delta < 0 {
				m.choiceIndex[def.Name] = len(def.Options) - 1
			}
		}
	}
}

func isChoiceList(def catalog.Input) bool {
	return len(def.Options) > 0 && (def.Multiple || def.Type == "multichoice" || def.Type == "multiple-choice")
}

func (m *FormModel) moveControl(delta int) {
	if m.selected < len(m.defs) {
		def := m.defs[m.selected]
		if isChoiceList(def) {
			next := m.choiceIndex[def.Name] + delta
			if next >= 0 && next < len(def.Options) {
				m.choiceIndex[def.Name] = next
				return
			}
		}
	}
	m.moveFocus(delta)
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
		if err == nil && msg.action != "add" {
			m.activateExclusive(msg.name)
		}
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
			if len(m.sections) > 0 {
				if m.area == 1 || m.area == 2 {
					m.area = 0
					return m, nil
				}
			}
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
					if len(m.sections) > 0 && (stroke == "tab" || stroke == "shift+tab") {
						if stroke == "tab" {
							m.area = (m.area + 1) % 3
						} else {
							m.area = (m.area + 2) % 3
						}
					} else if len(m.sections) > 0 {
						delta := 1
						if stroke == "up" {
							delta = -1
						}
						m.moveSplitControl(delta)
					} else {
						delta := 1
						if stroke == "up" || stroke == "shift+tab" {
							delta = -1
						}
						m.moveControl(delta)
					}
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
		if len(m.sections) > 0 {
			switch stroke {
			case "tab":
				m.area = (m.area + 1) % 3
				return m, nil
			case "shift+tab":
				m.area = (m.area + 2) % 3
				return m, nil
			case "left":
				if m.area == 1 {
					m.area = 0
				} else if m.area == 2 {
					m.actionIndex = (m.actionIndex + 1) % 2
				}
				return m, nil
			case "right":
				if m.area == 0 {
					m.area = 1
				} else if m.area == 2 {
					m.actionIndex = (m.actionIndex + 1) % 2
				}
				return m, nil
			case "up", "down":
				delta := 1
				if stroke == "up" {
					delta = -1
				}
				if m.area == 0 {
					m.moveSection(delta)
				} else if m.area == 1 {
					m.moveSplitControl(delta)
				} else if delta < 0 {
					m.area = 1
				}
				return m, nil
			case "enter":
				if m.area == 2 {
					if m.actionIndex == 0 {
						return m.save()
					}
					return m.cancel()
				}
				if m.area == 0 {
					m.area = 1
					return m, nil
				}
			}
		}
		switch stroke {
		case "down", "tab":
			m.moveControl(1)
			m.message = ""
			return m, nil
		case "up", "shift+tab":
			m.moveControl(-1)
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
				if def.Required && isChoiceList(def) && len(collectionRows(m.editor.Values()[def.Name])) == 0 {
					label := def.Label
					if label == "" {
						label = def.Name
					}
					m.message = "Select at least one option for " + label
					if def.Name == "__aact_destinations" {
						m.message = "Select at least one destination"
					}
				}
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
		m.activateExclusive(def.Name)
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
		if len(m.sections) > 0 {
			if m.editing {
				return m, nil
			}
			layout := m.layout()
			bodyY := mouse.Y - layout.bodyStart
			if bodyY < 0 || bodyY >= len(layout.splitSections) {
				return m, nil
			}
			delta := 1
			if mouse.Button == tea.MouseWheelUp {
				delta = -1
			} else if mouse.Button != tea.MouseWheelDown {
				return m, nil
			}
			if mouse.X <= layout.splitLeftWidth+1 {
				m.area = 0
				m.moveSection(delta)
			} else {
				m.area = 1
				m.moveSplitControl(delta)
			}
			return m, nil
		}
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
	if layout.split {
		bodyY := mouse.Y - layout.bodyStart
		if bodyY < 0 || bodyY >= len(layout.splitSections) {
			return m, nil
		}
		if mouse.X <= layout.splitLeftWidth+1 {
			section := layout.splitSections[bodyY]
			if section >= 0 && section < len(m.sections) {
				m.sectionIndex = section
				m.area = 0
				m.message = ""
				m.selectSectionField()
			}
			return m, nil
		}
		field := layout.splitFields[bodyY]
		if field < 0 || field >= len(m.defs) {
			return m, nil
		}
		if reason := m.disabledReason(m.defs[field].Name); reason != "" {
			m.message = reason
			return m, nil
		}
		m.area = 1
		m.selected = field
		m.message = ""
		if choice := layout.splitChoices[bodyY]; choice >= 0 {
			m.choiceIndex[m.defs[field].Name] = choice
			return m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		}
		return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if isChoiceList(m.defs[field]) && layout.visibleChoices[bodyY] < 0 {
		m.choiceIndex[m.defs[field].Name] = 0
		return m, nil
	}
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
	split          bool
	splitLeftWidth int
	splitFields    []int
	splitChoices   []int
	splitSections  []int
}

func (m *FormModel) layout() formLayout {
	if len(m.sections) > 0 {
		return m.splitLayout()
	}
	position := ""
	if len(m.defs) > 0 && m.selected < len(m.defs) {
		position = fmt.Sprintf(" (%d/%d)", m.selected+1, len(m.defs))
		if def := m.defs[m.selected]; isChoiceList(def) {
			position += fmt.Sprintf(" · choice %d/%d", m.choiceIndex[def.Name]+1, len(def.Options))
		}
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffe38a")).Render(m.title + position)
	rows := []string{}
	values := m.editor.Values()
	for i, def := range m.defs {
		prefix := "  "
		if i == m.selected && !isChoiceList(def) {
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
		if isChoiceList(def) {
			footer = "[ Save ]  [ Cancel ]\n↑↓/Tab next control · Space/Enter toggle\nCtrl+S save · Esc cancel"
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
		if hint := m.displayHint(def, values); hint != "" {
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

func (m *FormModel) splitLayout() formLayout {
	innerWidth := max(18, m.width-2)
	leftWidth := max(18, innerWidth/3)
	rightWidth := max(18, innerWidth-leftWidth-3)
	values := m.editor.Values()
	sectionHeading := m.sectionHeading
	if sectionHeading == "" {
		sectionHeading = "Sections"
	}
	leftLines := []string{"── " + sectionHeading}
	for i, section := range m.sections {
		prefix := "› "
		if i == m.sectionIndex {
			prefix = "> "
		}
		leftLines = append(leftLines, prefix+section.Title)
	}
	indices := m.splitFieldIndices(m.sectionIndex)
	rightLines := []string{"── " + m.sections[m.sectionIndex].Title}
	rightFields := []int{-1}
	for _, index := range indices {
		def := m.defs[index]
		label := def.Label
		if label == "" {
			label = def.Name
		}
		prefix := "› "
		if index == m.selected && m.area == 1 {
			prefix = "> "
		}
		display := textValue(values[def.Name])
		if len(def.Options) > 0 && isChoiceList(def) {
			chosen, _ := values[def.Name].([]string)
			if isChoiceList(def) && !def.Multiple && def.Type != "multichoice" && def.Type != "multiple-choice" {
				display = ""
				for _, option := range def.Options {
					if option.Value == values[def.Name] {
						if option.Label != "" {
							display = option.Label
						} else {
							display = option.Value
						}
					}
				}
			} else {
				display = ""
				for optionIndex, option := range def.Options {
					mark := "[ ]"
					for _, value := range chosen {
						if value == option.Value {
							mark = "[x]"
						}
					}
					optionLabel := option.Label
					if optionLabel == "" {
						optionLabel = option.Value
					}
					cursor := "› "
					if index == m.selected && m.area == 1 && m.choiceIndex[def.Name] == optionIndex {
						cursor = "> "
					}
					display += "\n" + cursor + mark + " " + optionLabel
				}
			}
		}
		if reason := m.disabledReason(def.Name); reason != "" {
			display += " — " + reason
		}
		rightLines = append(rightLines, prefix+label+": "+display)
		rightFields = append(rightFields, index)
		if hint := m.displayHint(def, values); hint != "" {
			rightLines = append(rightLines, "    · "+hint)
			rightFields = append(rightFields, -1)
		}
	}
	if len(indices) == 0 {
		rightLines = append(rightLines, "· No fields in this section")
	}
	leftSections := make([]int, len(leftLines))
	for i := range leftSections {
		leftSections[i] = i - 1
	}
	rightRows, rightRowFields, rightRowChoices := []string{}, []int{}, []int{}
	for i, line := range rightLines {
		for part, row := range strings.Split(line, "\n") {
			rightRows = append(rightRows, row)
			field, choice := -1, -1
			if i < len(rightFields) {
				field = rightFields[i]
				if part > 0 && field >= 0 && isChoiceList(m.defs[field]) {
					choice = part - 1
				}
			}
			rightRowFields = append(rightRowFields, field)
			rightRowChoices = append(rightRowChoices, choice)
		}
	}
	areaLabels := []string{"Sections", "Details", "Actions"}
	for i := range areaLabels {
		if i == m.area {
			areaLabels[i] = "[" + areaLabels[i] + "]"
		}
	}
	actions := "[ Save ]  [ Cancel ]"
	if m.area == 2 {
		if m.actionIndex == 0 {
			actions = "> [ Save ]  [ Cancel ]"
		} else {
			actions = "[ Save ]  > [ Cancel ]"
		}
	}
	footer := strings.Join(areaLabels, " · ") + "\n↑↓ Controls · ←→ Panes · Tab Areas · Ctrl-S Save · Esc Back\n" + actions
	if m.message != "" {
		footer = m.message + "\n" + footer
	}
	footerLines := strings.Split(footer, "\n")
	header := []string{m.title}
	if m.contextLine != "" {
		header = append(header, m.contextLine)
	}
	header = append(header, "")
	bodyStart := 1 + len(header)
	bodyHeight := max(1, m.height-2-len(header)-1-len(footerLines))
	leftRows, splitSections, _ := scrollSplitPane(leftLines, leftSections, nil, m.sectionIndex+1, bodyHeight)
	rightTarget := 0
	for row, field := range rightRowFields {
		if field == m.selected && (rightRowChoices[row] < 0 || rightRowChoices[row] == m.choiceIndex[m.defs[field].Name]) {
			rightTarget = row
		}
	}
	rightRows, splitFields, splitChoices := scrollSplitPane(rightRows, rightRowFields, rightRowChoices, rightTarget, bodyHeight)
	body := make([]string, max(len(leftRows), len(rightRows)))
	for i := range body {
		left, right := "", ""
		if i < len(leftRows) {
			left = leftRows[i]
		}
		if i < len(rightRows) {
			right = rightRows[i]
		}
		left = ansi.Truncate(left, leftWidth, "")
		right = ansi.Truncate(right, rightWidth, "")
		body[i] = left + strings.Repeat(" ", max(1, leftWidth-lipgloss.Width(left))) + " │ " + right
	}
	for len(splitSections) < len(body) {
		splitSections = append(splitSections, -1)
	}
	for len(splitFields) < len(body) {
		splitFields = append(splitFields, -1)
		splitChoices = append(splitChoices, -1)
	}
	lines := append([]string{}, header...)
	lines = append(lines, body...)
	lines = append(lines, "")
	lines = append(lines, footerLines...)
	for len(lines) < m.height-2 {
		lines = append(lines[:len(lines)-len(footerLines)], append([]string{""}, lines[len(lines)-len(footerLines):]...)...)
	}
	framed := []string{goldFG + "╔" + strings.Repeat("═", innerWidth) + "╗"}
	for _, line := range lines {
		clipped := ansi.Truncate(line, innerWidth, "")
		framed = append(framed, goldFG+"║"+modalSGR+clipped+strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(clipped)))+goldFG+"║")
	}
	framed = append(framed, goldFG+"╚"+strings.Repeat("═", innerWidth)+"╝")
	content := strings.Join(framed, "\n")
	footerY := -1
	for y, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "[ Save ]") {
			footerY = y
			break
		}
	}
	return formLayout{content: content, visibleFields: indices, footerY: footerY, bodyStart: bodyStart, split: true, splitLeftWidth: leftWidth, splitFields: splitFields, splitChoices: splitChoices, splitSections: splitSections}
}

func scrollSplitPane(rows []string, primary, secondary []int, target, height int) ([]string, []int, []int) {
	if len(rows) <= height {
		return rows, primary, secondary
	}
	target = max(0, min(target, len(rows)-1))
	start, end := 0, 0
	switch {
	case target < height-1:
		end = height - 1
	case target >= len(rows)-(height-1):
		end = len(rows)
		start = end - (height - 1)
	default:
		visibleCount := max(1, height-2)
		start = max(1, min(target-visibleCount+1, len(rows)-visibleCount-1))
		end = start + visibleCount
	}
	visible := append([]string{}, rows[start:end]...)
	ids := append([]int{}, primary[start:end]...)
	var details []int
	if secondary != nil {
		details = append([]int{}, secondary[start:end]...)
	}
	if start > 0 {
		visible = append([]string{"↑ more"}, visible...)
		ids = append([]int{-1}, ids...)
		if secondary != nil {
			details = append([]int{-1}, details...)
		}
	}
	if end < len(rows) {
		visible = append(visible, "↓ more")
		ids = append(ids, -1)
		if secondary != nil {
			details = append(details, -1)
		}
	}
	return visible, ids, details
}

func (m *FormModel) View() tea.View {
	content := m.layout().content
	width := m.width
	if width < 20 {
		width = 20
	}
	if len(m.sections) > 0 {
		view := tea.NewView(modalCanvas(content))
		view.MouseMode = tea.MouseModeCellMotion
		return view
	}
	view := tea.NewView(navyCanvas(content))
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
