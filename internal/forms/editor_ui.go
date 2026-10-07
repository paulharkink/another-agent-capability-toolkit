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
	"strings"
	"unicode/utf8"
)

var ErrNotSubmitted = errors.New("form has not been submitted")

var focusedActionStyle = lipgloss.NewStyle().Bold(true).Reverse(true)

// BackMsg asks the parent workspace to return to the previous layer while
// carrying the current form draft. It is not a submission or cancellation.
type BackMsg struct{ Draft map[string]any }

// ExitRequestMsg asks the parent workspace to confirm leaving a dirty form.
// Draft contains the last committed field values; an active text buffer may
// still need to be committed by Submit before it can be applied.
type ExitRequestMsg struct {
	Reason string
	Draft  map[string]any
}

type FormModel struct {
	ctx                         context.Context
	editor                      *Editor
	defs                        []catalog.Input
	title                       string
	submitLabel                 string
	contextLine                 string
	selected, width, height     int
	editing                     bool
	buffer, editAction, message string
	editOriginal                string
	fieldErrors                 map[string]string
	cursor                      int
	choiceIndex, rowIndex       map[string]int
	browser                     *picker.BrowserModel
	pickerPending               pickedMsg
	hints                       map[string]string
	conditions                  map[string]fieldCondition
	disabled                    map[string]string
	done                        bool
	backNavigation              bool
	unsavedExitGuard            bool
	readOnly                    bool
	hideCancel                  bool
	result                      map[string]any
	err                         error
	sections                    []FormSection
	allSections                 []FormSection
	sectionContent              map[string][]string
	sectionActions              map[string][]FormAction
	focusedAction               bool
	sectionActionIndex          int
	resumeEditAfterPicker       bool
	sectionOffset               int
	detailOffset                int
	detailScrolled              bool
	sectionHeading              string
	sectionIndex                int
	area                        int
	actionIndex                 int
	exclusive                   map[string][]string
	visibilityContext           map[string]any
}

// FormSection groups fields into the left-hand navigation list of a split form.
// Forms without sections keep the legacy single-column interaction.
type FormSection struct {
	ID     string
	Title  string
	Fields []string
}

// FormAction is a selectable operation shown beside a section's read-only content.
type FormAction struct {
	ID, Label string
	Disabled  string
}

// ActionMsg is emitted when the user activates an enabled section action.
type ActionMsg struct{ Section, SectionID, ID string }

func (m *FormModel) SetSections(sections ...FormSection) {
	m.allSections = append([]FormSection(nil), sections...)
	if len(sections) > 0 {
		m.backNavigation = true
	}
	if len(m.allSections) == 0 {
		m.sections = nil
		m.sectionIndex = 0
		m.area = 0
		return
	}
	for i := range m.allSections {
		m.allSections[i].Fields = append([]string(nil), sections[i].Fields...)
	}
	m.refreshSections()
}

func (m *FormModel) refreshSections() {
	if len(m.allSections) == 0 {
		return
	}
	selected := m.currentSectionKey()
	oldIndex := m.sectionIndex
	selectedField := ""
	if m.selected >= 0 && m.selected < len(m.defs) {
		selectedField = m.defs[m.selected].Name
	}
	wasActionFocused := m.focusedAction
	oldActionIndex := m.sectionActionIndex
	oldArea := m.area
	visible := make([]FormSection, 0, len(m.allSections))
	for _, section := range m.allSections {
		if len(section.Fields) > 0 {
			shown := false
			for _, name := range section.Fields {
				if m.disabledReason(name) == "" {
					shown = true
					break
				}
			}
			if !shown {
				continue
			}
		}
		visible = append(visible, section)
	}
	m.sections = visible
	if len(visible) == 0 {
		m.sectionIndex = 0
		m.area = 0
		return
	}
	for i := range visible {
		if sectionKey(visible[i]) == selected {
			m.sectionIndex = i
			indices := m.splitFieldIndices(i)
			for _, fieldIndex := range indices {
				if selectedField != "" && m.defs[fieldIndex].Name == selectedField {
					m.selected = fieldIndex
					m.area = oldArea
					m.focusedAction = wasActionFocused
					m.sectionActionIndex = oldActionIndex
					return
				}
			}
			if len(indices) == 0 && wasActionFocused {
				actions := m.sectionActions[sectionKey(visible[i])]
				if len(actions) > 0 {
					m.selected = len(m.defs)
					m.area = oldArea
					m.focusedAction = true
					m.sectionActionIndex = min(max(oldActionIndex, 0), len(actions)-1)
					return
				}
			}
			m.selectSectionField()
			return
		}
	}
	m.sectionIndex = min(max(0, oldIndex), len(visible)-1)
	m.area = 0
	m.selectSectionField()
}

func (m *FormModel) SetSectionHeading(heading string) { m.sectionHeading = heading }

// SetSectionActions adds selectable actions to a section's details pane.
func (m *FormModel) SetSectionActions(title string, actions ...FormAction) {
	m.setSectionActions(m.resolveSectionKey(title), actions...)
}

func (m *FormModel) SetSectionActionsID(id string, actions ...FormAction) {
	m.setSectionActions(id, actions...)
}

func (m *FormModel) setSectionActions(key string, actions ...FormAction) {
	if m.sectionActions == nil {
		m.sectionActions = map[string][]FormAction{}
	}
	m.sectionActions[key] = append([]FormAction(nil), actions...)
	if key == m.currentSectionKey() && len(m.splitFieldIndices(m.sectionIndex)) == 0 {
		m.focusedAction = len(actions) > 0
		m.sectionActionIndex = 0
	}
}

// Sections returns a copy of the current navigation metadata.
func (m *FormModel) Sections() []FormSection {
	sections := append([]FormSection(nil), m.sections...)
	for i := range sections {
		sections[i].Fields = append([]string(nil), sections[i].Fields...)
	}
	return sections
}

// HasSection reports whether the form has a section with this title.
func (m *FormModel) HasSection(title string) bool {
	for _, section := range m.sections {
		if section.Title == title {
			return true
		}
	}
	return false
}

func (m *FormModel) HasSectionID(id string) bool {
	for _, section := range m.sections {
		if section.ID == id {
			return true
		}
	}
	return false
}

// Definitions returns a copy of the field definitions used to build sections.
func (m *FormModel) Definitions() []catalog.Input { return append([]catalog.Input(nil), m.defs...) }

// PaneWidth returns the current overall form width.
func (m *FormModel) PaneWidth() int { return m.width }

// PickerActive reports whether the embedded fallback picker owns input.
func (m *FormModel) PickerActive() bool { return m.browser != nil }

// IsEditingInput reports whether text entry or an embedded picker owns keys.
func (m *FormModel) IsEditingInput() bool { return m.editing || m.browser != nil }

// SetSectionContent adds read-only information to the right pane of a section.
// It may be combined with editable fields in that section.
func (m *FormModel) SetSectionContent(title string, lines []string) {
	m.setSectionContent(m.resolveSectionKey(title), lines)
}

func (m *FormModel) SetSectionContentID(id string, lines []string) {
	m.setSectionContent(id, lines)
}

func (m *FormModel) setSectionContent(key string, lines []string) {
	if m.sectionContent == nil {
		m.sectionContent = map[string][]string{}
	}
	m.sectionContent[key] = append([]string(nil), lines...)
	if key == m.currentSectionKey() {
		m.sectionOffset = 0
	}
}

func (m *FormModel) SelectSectionID(id string) bool {
	for i, section := range m.sections {
		if section.ID == id {
			m.sectionIndex = i
			m.sectionOffset = 0
			m.detailOffset = 0
			m.selectSectionField()
			return true
		}
	}
	return false
}

func (m *FormModel) SectionID() string {
	if m.sectionIndex < 0 || m.sectionIndex >= len(m.sections) {
		return ""
	}
	return m.sections[m.sectionIndex].ID
}

func (m *FormModel) currentSectionKey() string {
	if m.sectionIndex < 0 || m.sectionIndex >= len(m.sections) {
		return ""
	}
	return sectionKey(m.sections[m.sectionIndex])
}

func sectionKey(section FormSection) string {
	if section.ID != "" {
		return section.ID
	}
	return section.Title
}

func (m *FormModel) resolveSectionKey(title string) string {
	for _, section := range m.sections {
		if section.Title == title {
			return sectionKey(section)
		}
	}
	for _, section := range m.sections {
		if section.ID == title {
			return sectionKey(section)
		}
	}
	return title
}

// SetBackNavigation enables Escape as a parent-navigation signal for a
// single-pane TUI editor. Standalone RunEditor keeps Escape cancellation by default.
func (m *FormModel) SetBackNavigation(enabled bool)   { m.backNavigation = enabled }
func (m *FormModel) SetReadOnly(enabled bool)         { m.readOnly = enabled }
func (m *FormModel) SetUnsavedExitGuard(enabled bool) { m.unsavedExitGuard = enabled }

// SetCancelHidden hides the bottom Cancel action; Escape remains available.
func (m *FormModel) SetCancelHidden(hidden bool) {
	m.hideCancel = hidden
	if hidden {
		m.actionIndex = 0
		if m.selected > len(m.defs) {
			m.selected = len(m.defs)
		}
	}
}

// ApplyValues loads a saved or restored draft through the declared field
// normalizers while keeping the existing dirty baseline intact.
func (m *FormModel) ApplyValues(values map[string]any) error {
	for _, def := range m.defs {
		value, ok := values[def.Name]
		if !ok {
			continue
		}
		if err := m.editor.Apply(def.Name, value); err != nil {
			return err
		}
	}
	for name := range values {
		if _, err := m.editor.definition(name); err != nil {
			return err
		}
	}
	m.refreshSections()
	return nil
}
func (m *FormModel) SetMessage(message string) { m.message = message }

// HasUnsavedChanges compares the semantic field values with the last clean
// baseline and also accounts for text still being edited in place.
func (m *FormModel) HasUnsavedChanges() bool {
	return m.editor.HasUnsavedChanges() || (m.editing && m.buffer != m.editOriginal)
}

// ChangedFieldSummary returns changed field labels in declaration order and
// the number omitted by limit. It deliberately never returns field values.
func (m *FormModel) ChangedFieldSummary(limit int) ([]string, int) {
	labels := make([]string, 0)
	for _, def := range m.defs {
		original := m.editor.original[def.Name]
		current := m.editor.values[def.Name]
		changed := !answerValueEqual(original, current)
		if m.editing && m.selected < len(m.defs) && m.defs[m.selected].Name == def.Name && m.buffer != m.editOriginal {
			changed = true
		}
		if changed {
			label := def.Label
			if label == "" {
				label = def.Name
			}
			labels = append(labels, label)
		}
	}
	limit = max(0, limit)
	if len(labels) <= limit {
		return labels, 0
	}
	return labels[:limit], len(labels) - limit
}

// MarkClean records the current semantic form values as the clean baseline.
func (m *FormModel) MarkClean() { m.editor.MarkClean() }

// PrepareRetry reopens a submitted form after its parent operation fails,
// retaining the submitted values and the existing clean baseline.
func (m *FormModel) PrepareRetry(message string) {
	m.done = false
	m.result = nil
	m.err = nil
	m.message = message
}

func (m *FormModel) SelectSection(title string) {
	for i, section := range m.sections {
		if section.Title == title {
			m.sectionIndex = i
			m.sectionOffset = 0
			m.detailOffset = 0
			m.detailScrolled = false
			m.selectSectionField()
			return
		}
	}
}

// Values returns a copy of the current editable draft.
func (m *FormModel) Values() map[string]any { return m.editor.Values() }

// ReconcileDraftField replaces an editable draft value with observed state
// after a partially applied operation. It intentionally skips validation:
// the value reflects effects already performed, not a new user submission.
func (m *FormModel) ReconcileDraftField(name string, value any) bool {
	if _, err := m.editor.definition(name); err != nil {
		return false
	}
	m.editor.values[name] = copyAnswers(map[string]any{name: value})[name]
	m.refreshSections()
	return true
}

// SectionTitle returns the title of the currently selected section, if any.
func (m *FormModel) SectionTitle() string {
	if m.sectionIndex < 0 || m.sectionIndex >= len(m.sections) {
		return ""
	}
	return m.sections[m.sectionIndex].Title
}

// FocusArea reports whether section navigation, section details, or actions
// currently own keyboard focus. It lets parent views provide read-only actions
// in the details pane without intercepting Enter in the section list.
func (m *FormModel) FocusArea() int { return m.area }

// FocusSection returns keyboard focus to the section list.
func (m *FormModel) FocusSection() {
	if len(m.sections) > 0 {
		m.area = 0
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
	m.refreshSections()
}

func (m *FormModel) displayHint(def catalog.Input, values map[string]any) string {
	parts := []string{}
	if hint := m.hints[def.Name]; hint != "" {
		parts = append(parts, hint)
	}
	if def.Hint != "" && def.Hint != m.hints[def.Name] {
		parts = append(parts, def.Hint)
	}
	others, exclusive := m.exclusive[def.Name]
	if exclusive {
		active := ""
		for _, name := range append([]string{def.Name}, others...) {
			if value, ok := values[name].(string); ok && strings.TrimSpace(value) != "" {
				active = name
				break
			}
		}
		label := def.Label
		if label == "" {
			label = def.Name
		}
		state := "Active method · " + label
		if active == "" {
			state = "Enter a value for " + label
		} else if active != def.Name {
			state = "Type a value to switch to " + label
		}
		parts = append([]string{state}, parts...)
	}
	return strings.Join(parts, " · ")
}

func (m *FormModel) exclusiveInactive(name string, values map[string]any) bool {
	others, ok := m.exclusive[name]
	if !ok {
		return false
	}
	for _, candidate := range append([]string{name}, others...) {
		if value, ok := values[candidate].(string); ok && strings.TrimSpace(value) != "" {
			return candidate != name
		}
	}
	return false
}

func wrapHint(text string, width int) []string {
	width = max(1, width)
	lines := []string{}
	clauses := strings.Split(text, " · ")
	if len(clauses) > 1 && clauses[1] == "editable" {
		clauses[0] += " · " + clauses[1]
		clauses = append(clauses[:1], clauses[2:]...)
	}
	if len(clauses) > 1 && clauses[0] == "Active method" && strings.HasPrefix(clauses[1], "imported,") {
		clauses = append([]string{clauses[0] + " · " + clauses[1]}, clauses[2:]...)
	}
	for _, clause := range clauses {
		words := strings.Fields(clause)
		if len(words) == 0 {
			continue
		}
		line := ""
		for _, word := range words {
			chunks := wrapCellText(word, width)
			if len(chunks) > 1 {
				if line != "" {
					lines = append(lines, line)
				}
				lines = append(lines, chunks[:len(chunks)-1]...)
				line = chunks[len(chunks)-1]
				continue
			}
			if line == "" {
				line = word
			} else if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
			} else {
				lines = append(lines, line)
				line = word
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func wrapCellText(text string, width int) []string {
	width = max(1, width)
	lines := []string{}
	line := ""
	for _, r := range text {
		next := string(r)
		if line != "" && lipgloss.Width(line+next) > width {
			lines = append(lines, line)
			line = next
		} else {
			line += next
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

func focusedPaneTitle(title string, focused bool) string {
	if !focused {
		return title
	}
	return focusedActionStyle.Render(title)
}

func (m *FormModel) emptyChoicesText(def catalog.Input) string {
	label := def.Label
	if label == "" {
		label = def.Name
	}
	message := "No choices are currently available for " + label
	if hint := m.hints[def.Name]; hint != "" {
		message += " · choices supplied by " + hint
	} else if def.OptionsFrom != "" {
		message += " · options are resolved from the current setup context"
	}
	return message
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
		m.focusedAction = false
	} else {
		m.selected = len(m.defs)
		actions := m.sectionActions[m.currentSectionKey()]
		m.focusedAction = len(actions) > 0
		m.sectionActionIndex = 0
	}
}

func (m *FormModel) activateSectionAction() tea.Cmd {
	actions := m.sectionActions[m.currentSectionKey()]
	if m.sectionActionIndex < 0 || m.sectionActionIndex >= len(actions) {
		return nil
	}
	action := actions[m.sectionActionIndex]
	if action.Disabled != "" {
		m.message = action.Disabled
		return nil
	}
	section := m.SectionTitle()
	sectionID := m.SectionID()
	return func() tea.Msg { return ActionMsg{Section: section, SectionID: sectionID, ID: action.ID} }
}

func (m *FormModel) moveSection(delta int) {
	if len(m.sections) == 0 {
		return
	}
	m.sectionIndex = (m.sectionIndex + len(m.sections) + delta) % len(m.sections)
	m.sectionOffset = 0
	m.detailOffset = 0
	m.detailScrolled = false
	m.message = ""
	m.selectSectionField()
}

func (m *FormModel) moveSplitControl(delta int) {
	m.detailOffset = 0
	m.detailScrolled = false
	indices := m.splitFieldIndices(m.sectionIndex)
	if len(indices) == 0 {
		actions := m.sectionActions[m.currentSectionKey()]
		if len(actions) > 0 {
			m.focusedAction = true
			m.sectionActionIndex = min(max(0, m.sectionActionIndex+delta), len(actions)-1)
			return
		}
		if m.sectionIndex >= 0 && m.sectionIndex < len(m.sections) {
			content := m.sectionContent[m.currentSectionKey()]
			m.sectionOffset = min(max(0, m.sectionOffset+delta), max(0, len(content)-1))
		}
		return
	}
	if m.selected < len(m.defs) && (isChoiceList(m.defs[m.selected]) || isScalarChoice(m.defs[m.selected])) {
		def := m.defs[m.selected]
		next := m.choiceIndex[def.Name] + delta
		if next >= 0 && next < len(def.Options) {
			m.choiceIndex[def.Name] = next
			return
		}
	}
	if m.selected < len(m.defs) && isEditableCollection(m.defs[m.selected]) {
		def := m.defs[m.selected]
		last := len(collectionRows(m.editor.Values()[def.Name])) // The final row adds an item.
		next := m.rowIndex[def.Name] + delta
		if next >= 0 && next <= last {
			m.rowIndex[def.Name] = next
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

type fieldCondition struct {
	Equals             map[string]any
	LegacyChoicePrompt bool
}

func NewForm(ctx context.Context, defs []catalog.Input, prefill map[string]any) *FormModel {
	m := &FormModel{ctx: ctx, editor: NewEditor(defs, prefill), defs: append([]catalog.Input{}, defs...), title: "Edit package inputs", width: 80, height: 24, choiceIndex: map[string]int{}, rowIndex: map[string]int{}, hints: map[string]string{}, conditions: map[string]fieldCondition{}, disabled: map[string]string{}}
	values := m.editor.Values()
	for _, def := range defs {
		if len(def.VisibleWhen) > 0 {
			m.conditions[def.Name] = fieldCondition{Equals: cloneCondition(def.VisibleWhen)}
		}
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

// NewFormWithContext evaluates visibility and required validation using fixed
// controller values that are not editable or included in the returned answers.
func NewFormWithContext(ctx context.Context, defs []catalog.Input, prefill, visibilityContext map[string]any) *FormModel {
	m := NewForm(ctx, defs, prefill)
	m.visibilityContext = copyAnswers(visibilityContext)
	m.editor.SetVisibilityContext(visibilityContext)
	return m
}

func (m *FormModel) conditionValues() map[string]any {
	values := copyAnswers(m.visibilityContext)
	for name, value := range m.editor.Values() {
		values[name] = value
	}
	return values
}
func (m *FormModel) SetTitle(title string) { m.title = title }

// SetSubmitLabel changes the primary action label. Empty restores "Save".
func (m *FormModel) SetSubmitLabel(label string) { m.submitLabel = strings.TrimSpace(label) }
func (m *FormModel) SetHint(name, hint string)   { m.hints[name] = hint }
func (m *FormModel) SetContext(line string)      { m.contextLine = line }
func (m *FormModel) SetConditional(name, selector, choice string) {
	m.conditions[name] = fieldCondition{Equals: map[string]any{selector: choice}, LegacyChoicePrompt: true}
}
func (m *FormModel) SetDisabled(name, reason string) {
	m.disabled[name] = reason
	m.setError(m.editor.Apply(name, ""))
	if m.selected < len(m.defs) && m.defs[m.selected].Name == name {
		m.moveFocus(1)
	}
	m.refreshSections()
}
func cloneCondition(values map[string]any) map[string]any {
	copy := make(map[string]any, len(values))
	for name, value := range values {
		copy[name] = value
	}
	return copy
}

func conditionMatches(condition fieldCondition, values map[string]any) bool {
	return catalog.InputVisible(catalog.Input{VisibleWhen: condition.Equals}, values)
}
func (m *FormModel) disabledReason(name string) string {
	if reason := m.disabled[name]; reason != "" {
		return reason
	}
	condition, ok := m.conditions[name]
	if !ok {
		return ""
	}
	values := m.conditionValues()
	if conditionMatches(condition, values) {
		return ""
	}
	if condition.LegacyChoicePrompt && len(condition.Equals) == 1 {
		for selector, expected := range condition.Equals {
			selected, _ := values[selector].(string)
			for _, definition := range m.defs {
				if definition.Name != selector {
					continue
				}
				for _, option := range definition.Options {
					if option.Value == selected {
						label := option.Label
						if label == "" {
							label = selected
						}
						return "inactive while " + label + " is selected"
					}
				}
			}
			_ = expected
		}
	}
	return "inactive for the selected options"
}
func (m *FormModel) moveFocus(delta int) {
	count := len(m.defs) + 1
	if !m.hideCancel {
		count++
	}
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

func isScalarChoice(def catalog.Input) bool {
	return len(def.Options) > 0 && !isChoiceList(def)
}

func isEditableCollection(def catalog.Input) bool {
	return def.Multiple && len(def.Options) == 0
}

func (m *FormModel) moveControl(delta int) {
	if m.selected < len(m.defs) {
		def := m.defs[m.selected]
		if isChoiceList(def) || isScalarChoice(def) {
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
	kind, initial      string
	index              int
	resumeEdit         bool
	err                error
}

func (m *FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	if m.browser != nil {
		if size, ok := msg.(tea.WindowSizeMsg); ok {
			m.width, m.height = size.Width, size.Height
			m.resizeBrowser()
			return m, nil
		}
		if mouse, ok := msg.(tea.MouseMsg); ok {
			translated, inside := m.pickerMouse(mouse)
			if !inside {
				return m, nil
			}
			msg = translated
		}
		_, cmd := m.browser.Update(msg)
		if _, err := m.browser.Result(); !errors.Is(err, picker.ErrNotSubmitted) {
			result, resultErr := m.browser.Result()
			pending := m.pickerPending
			m.browser = nil
			pending.path, pending.err = result, resultErr
			m.applyPicked(pending)
		}
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.PasteMsg:
		if m.editing {
			runes := []rune(m.buffer)
			m.buffer = string(runes[:m.cursor]) + msg.Content + string(runes[m.cursor:])
			m.cursor += utf8.RuneCountInString(msg.Content)
		}
		return m, nil
	case tea.PasteStartMsg, tea.PasteEndMsg:
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case pickedMsg:
		m.applyPicked(msg)
	case tea.MouseMsg:
		return m.mouseUpdate(msg)
	case tea.KeyPressMsg:
		stroke := msg.String()
		if stroke == "ctrl+c" {
			return m.discard()
		}
		if m.readOnly && stroke != "esc" {
			if stroke == "enter" {
				if len(m.sections) > 0 && m.area == 0 {
					m.area = 1
				} else if m.area == 1 && len(m.splitFieldIndices(m.sectionIndex)) == 0 && len(m.sectionActions[m.currentSectionKey()]) > 0 {
					return m, m.activateSectionAction()
				} else if len(m.sections) > 0 && m.area == 2 {
					return m.cancel()
				}
				return m, nil
			}
			switch stroke {
			case "tab", "shift+tab", "left", "right", "up", "down", "home", "end", "pgup", "pgdown":
			default:
				return m, nil
			}
		}
		if stroke == "esc" && !m.editing {
			if len(m.sections) > 0 {
				if m.area == 1 || m.area == 2 {
					m.area = 0
					return m, nil
				}
				if m.backNavigation {
					if m.unsavedExitGuard && m.HasUnsavedChanges() {
						return m, func() tea.Msg { return ExitRequestMsg{Reason: "back", Draft: m.Values()} }
					}
					return m, func() tea.Msg { return BackMsg{Draft: m.Values()} }
				}
				return m.cancel()
			}
			if m.backNavigation {
				if m.unsavedExitGuard && m.HasUnsavedChanges() {
					return m, func() tea.Msg { return ExitRequestMsg{Reason: "back", Draft: m.Values()} }
				}
				return m, func() tea.Msg { return BackMsg{Draft: m.Values()} }
			}
			return m.cancel()
		}
		if stroke == "ctrl+s" {
			if m.readOnly {
				return m, nil
			}
			if m.editing {
				m.commitBuffer()
				if m.editing {
					return m, nil
				}
			}
			return m.Submit()
		}
		if m.editing {
			switch stroke {
			case "esc":
				m.editing = false
				m.buffer = m.editOriginal
				delete(m.fieldErrors, m.defs[m.selected].Name)
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
				} else if m.area == 2 && !m.hideCancel {
					m.actionIndex = (m.actionIndex + 1) % 2
				}
				return m, nil
			case "right":
				if m.area == 0 {
					m.area = 1
				} else if m.area == 2 && !m.hideCancel {
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
				} else if m.area == 2 && !m.hideCancel {
					m.actionIndex = (m.actionIndex + 1) % 2
				}
				return m, nil
			case "pgup", "pgdown":
				if m.area == 1 {
					// Half-page steps overlap the current view so wrapped paths and
					// notes cannot fall between two scroll positions.
					delta := max(1, (m.height-8)/2)
					if stroke == "pgup" {
						delta = -delta
					}
					m.detailScrolled = true
					m.detailOffset = max(0, m.detailOffset+delta)
				}
				return m, nil
			case "enter":
				if m.area == 1 && m.selected < len(m.defs) && isScalarChoice(m.defs[m.selected]) {
					def := m.defs[m.selected]
					m.choose(def, m.editor.Values()[def.Name])
					return m, nil
				}
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
				if m.area == 1 && len(m.splitFieldIndices(m.sectionIndex)) == 0 {
					return m, m.activateSectionAction()
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
				return m.cancel()
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
		case "r", "delete", "backspace":
			if scalarDefinition(def).Multiple {
				if m.rowIndex[def.Name] < len(collectionRows(value)) {
					m.setError(m.editor.RemoveValue(def.Name, m.rowIndex[def.Name]))
					m.rowIndex[def.Name] = min(m.rowIndex[def.Name], len(collectionRows(m.editor.Values()[def.Name])))
				}
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
				if paths, ok := value.([]string); ok && m.rowIndex[def.Name] < len(paths) {
					m.beginEdit("edit", paths[m.rowIndex[def.Name]])
				}
			} else if def.Multiple && len(def.Options) == 0 {
				if rows := collectionRows(value); m.rowIndex[def.Name] < len(rows) {
					m.beginEdit("edit", textValue(rows[m.rowIndex[def.Name]]))
				}
			}
		case "b":
			if def.Type == "directory" || def.Type == "file" {
				if def.Multiple {
					if paths, ok := value.([]string); ok && m.rowIndex[def.Name] < len(paths) {
						return m, m.pick(def, "edit", paths[m.rowIndex[def.Name]])
					}
					return m, m.pick(def, "add", "")
				}
				return m, m.pick(def, "apply", textValue(value))
			}
		case "m":
			if def.Type == "directory" || def.Type == "file" {
				if def.Multiple {
					if paths, ok := value.([]string); ok && m.rowIndex[def.Name] < len(paths) {
						index := m.rowIndex[def.Name]
						m.beginEdit("edit", paths[index])
					} else {
						m.beginEdit("add", "")
					}
				} else {
					m.beginEdit("apply", textValue(value))
				}
			}
		case "enter", "space", " ":
			if isScalarChoice(def) {
				m.choose(def, value)
			} else if def.Type == "boolean" && !def.Multiple {
				current, _ := value.(bool)
				m.setError(m.editor.Apply(def.Name, !current))
				m.refreshSections()
			} else if def.OptionsFrom != "" && len(def.Options) == 0 {
				m.message = m.emptyChoicesText(def)
			} else if len(def.Options) > 0 {
				m.choose(def, value)
			} else if def.Type == "directory" || def.Type == "file" {
				if def.Multiple {
					if paths, ok := value.([]string); ok && m.rowIndex[def.Name] < len(paths) {
						m.beginEdit("edit", paths[m.rowIndex[def.Name]])
					} else {
						m.beginEdit("add", "")
					}
				} else {
					m.beginEdit("apply", textValue(value))
				}
			} else if def.Multiple {
				if rows := collectionRows(value); m.rowIndex[def.Name] < len(rows) {
					m.beginEdit("edit", textValue(rows[m.rowIndex[def.Name]]))
				} else {
					m.beginEdit("add", "")
				}
			} else {
				m.beginEdit("apply", textValue(value))
			}
		}
	}
	return m, nil
}

func (m *FormModel) cancel() (tea.Model, tea.Cmd) {
	if m.unsavedExitGuard && m.HasUnsavedChanges() {
		return m, func() tea.Msg { return ExitRequestMsg{Reason: "cancel", Draft: m.Values()} }
	}
	return m.discard()
}

func (m *FormModel) discard() (tea.Model, tea.Cmd) {
	m.editor.Cancel()
	m.done = true
	m.err = picker.ErrCancelled
	return m, tea.Quit
}

// Submit commits an active field buffer and follows the same validation and
// save path as Ctrl-S. Validation failures leave the form open for correction.
func (m *FormModel) Submit() (tea.Model, tea.Cmd) {
	if m.editing {
		m.commitBuffer()
		if m.editing {
			return m, nil
		}
	}
	return m.save()
}

func (m *FormModel) save() (tea.Model, tea.Cmd) {
	values, err := m.editor.Commit()
	if err != nil {
		m.message = err.Error()
		for i, def := range m.defs {
			if !catalog.InputVisible(def, m.conditionValues()) {
				continue
			}
			validationErr := Validate([]catalog.Input{def}, m.conditionValues())
			if m.editor.initialErrors[def.Name] != nil || validationErr != nil {
				m.focusValidationField(i, def.Name)
				if m.fieldErrors == nil {
					m.fieldErrors = map[string]string{}
				}
				if initialErr := m.editor.initialErrors[def.Name]; initialErr != nil {
					validationErr = initialErr
				}
				if validationErr != nil {
					m.fieldErrors[def.Name] = m.validationMessage(def, validationErr)
					m.message = m.fieldErrors[def.Name]
				}
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

func (m *FormModel) focusValidationField(index int, name string) {
	m.selected = index
	m.area = 1
	m.focusedAction = false
	m.sectionOffset = 0
	m.detailOffset = 0
	m.detailScrolled = false
	for sectionIndex, section := range m.sections {
		for _, field := range section.Fields {
			if field == name {
				m.sectionIndex = sectionIndex
				return
			}
		}
	}
}

func (m *FormModel) beginEdit(action, buffer string) {
	m.editing = true
	m.editAction = action
	m.buffer = buffer
	m.editOriginal = buffer
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
		delete(m.fieldErrors, def.Name)
		m.activateExclusive(def.Name)
		if m.editAction == "add" {
			m.rowIndex[def.Name] = len(collectionRows(m.editor.Values()[def.Name])) - 1
		}
		m.editing = false
		m.refreshSections()
	} else {
		if m.fieldErrors == nil {
			m.fieldErrors = map[string]string{}
		}
		m.fieldErrors[def.Name] = m.validationMessage(def, e)
	}
}

func (m *FormModel) validationMessage(def catalog.Input, err error) string {
	message := err.Error()
	prefix := "input " + def.Name + ": "
	message = strings.TrimPrefix(message, prefix)
	if strings.HasPrefix(message, "input "+def.Name+" is ") {
		message = "is " + strings.TrimPrefix(message, "input "+def.Name+" is ")
	}
	label := def.Label
	if label == "" {
		label = def.Name
	}
	if strings.HasPrefix(message, "must be at least ") {
		return label + " must be at least " + strings.TrimPrefix(message, "must be at least ") + "."
	}
	return label + ": " + message
}
func (m *FormModel) choose(def catalog.Input, current any) {
	selectedOption := def.Options[m.choiceIndex[def.Name]%len(def.Options)]
	option := selectedOption.Value
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
		if selectedOption.DisabledReason != "" && !found {
			m.message = selectedOption.DisabledReason
			return
		}
		if !found {
			next = append(next, option)
		}
		m.setError(m.editor.Apply(def.Name, next))
		m.refreshSections()
	} else {
		if selectedOption.DisabledReason != "" && current != option {
			m.message = selectedOption.DisabledReason
			return
		}
		err := m.editor.Apply(def.Name, option)
		m.setError(err)
		if err == nil {
			m.refreshSections()
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

func editViewport(buffer string, cursor, width int) string {
	runes := []rune(buffer)
	cursor = max(0, min(cursor, len(runes)))
	width = max(1, width)
	start, end := 0, len(runes)
	render := func() string {
		left, right := "", ""
		if start > 0 {
			left = "…"
		}
		if end < len(runes) {
			right = "…"
		}
		return left + string(runes[start:cursor]) + "_" + string(runes[cursor:end]) + right
	}
	for lipgloss.Width(render()) > width {
		leftWidth := lipgloss.Width(string(runes[start:cursor]))
		rightWidth := lipgloss.Width(string(runes[cursor:end]))
		if start < cursor && (leftWidth >= rightWidth || end == cursor) {
			start++
		} else if end > cursor {
			end--
		} else {
			break
		}
	}
	return render()
}
func (m *FormModel) pick(def catalog.Input, action, initial string) tea.Cmd {
	index := m.rowIndex[def.Name]
	m.pickerPending = pickedMsg{
		name: def.Name, action: action, index: index,
		kind: def.Type, initial: initial, resumeEdit: m.resumeEditAfterPicker,
	}
	m.resumeEditAfterPicker = false
	m.browser = picker.NewBrowser(m.ctx, def.Type, initial)
	m.resizeBrowser()
	return m.browser.Init()
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
		if m.readOnly {
			return m.cancel()
		}
		saveText := "[ " + m.submitActionLabel() + " ]"
		if mouse.X >= 1 && mouse.X < 1+len(saveText) {
			if m.editing {
				m.commitBuffer()
				if m.editing {
					return m, nil
				}
			}
			return m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		}
		cancelStart := 1 + len(saveText) + 2
		if !m.hideCancel && mouse.X >= cancelStart && mouse.X < cancelStart+len("[ Cancel ]") {
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
			if field < 0 && bodyY < len(layout.splitChoices) && layout.splitChoices[bodyY] >= 0 {
				m.area = 1
				m.focusedAction = true
				m.sectionActionIndex = layout.splitChoices[bodyY]
				return m, m.activateSectionAction()
			}
			return m, nil
		}
		if bodyY < len(layout.splitChoices) && layout.splitChoices[bodyY] == -2 {
			def := m.defs[field]
			if reason := m.disabledReason(def.Name); reason != "" {
				m.message = reason
				return m, nil
			}
			initial := textValue(m.editor.Values()[def.Name])
			if m.editing {
				initial = m.buffer
				m.resumeEditAfterPicker = true
				m.message = ""
			}
			m.area = 1
			m.selected = field
			return m, m.pick(def, "apply", initial)
		}
		if reason := m.disabledReason(m.defs[field].Name); reason != "" {
			m.message = reason
			return m, nil
		}
		m.area = 1
		m.selected = field
		m.message = ""
		if isEditableCollection(m.defs[field]) {
			if row := layout.splitChoices[bodyY]; row >= 0 {
				m.rowIndex[m.defs[field].Name] = row
				if bodyY < len(layout.splitRemoveX) && layout.splitRemoveX[bodyY] >= 0 && mouse.X >= layout.splitRemoveX[bodyY] && mouse.X < layout.splitRemoveX[bodyY]+len("[Remove]") {
					return m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
				}
				return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			return m, nil
		}
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
	if bodyY < len(layout.visibleRows) && layout.visibleRows[bodyY] == -2 && (m.defs[field].Type == "file" || m.defs[field].Type == "directory") {
		if reason := m.disabledReason(m.defs[field].Name); reason != "" {
			m.message = reason
			return m, nil
		}
		initial := textValue(m.editor.Values()[m.defs[field].Name])
		if m.editing {
			initial = m.buffer
			m.resumeEditAfterPicker = true
			m.message = ""
		}
		m.selected = field
		return m, m.pick(m.defs[field], "apply", initial)
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
	splitRemoveX   []int
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
			display = m.emptyChoicesText(def)
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
				if m.editing && i == m.selected && index == m.rowIndex[def.Name] && m.editAction == "edit" {
					row = "Edit: " + editViewport(m.buffer, m.cursor, max(8, m.width-30))
				}
				if def.Type == "directory" || def.Type == "file" {
					display += "\n    " + mark + " " + row + "  [Edit] [Remove]"
				} else {
					display += "\n    " + mark + " " + row
				}
			}
			if m.editing && i == m.selected && m.editAction == "add" {
				display += "\n    > + Edit: " + editViewport(m.buffer, m.cursor, max(8, m.width-30))
			}
		}
		if m.editing && i == m.selected && !scalarDefinition(def).Multiple && def.OptionsFrom == "" {
			display = "Edit: " + editViewport(m.buffer, m.cursor, max(8, m.width-lipgloss.Width(label)-14))
		}
		if validation := m.fieldErrors[def.Name]; validation != "" {
			display += "\n    ! " + validation
		}
		if (def.Type == "directory" || def.Type == "file") && !def.Multiple {
			display += "\n    [Browse · b]"
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
			if i == m.selected && strings.Contains(line, "[Browse · b]") {
				selectedLine = len(bodyLines) + part
			}
			bodyLines = append(bodyLines, line)
			fieldLines = append(fieldLines, i)
			rowLine := part - 1
			if strings.Contains(line, "[Browse · b]") {
				rowLine = -2
			}
			rowLines = append(rowLines, rowLine)
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
	escapeAction := "Esc cancel"
	if m.backNavigation {
		escapeAction = "Esc back"
	}
	saveControl := "[ " + m.submitActionLabel() + " ]"
	actionsControl := saveControl
	if !m.hideCancel {
		actionsControl += "  [ Cancel ]"
	}
	footer := actionsControl + "\nTab/↑↓ field · Enter edit · ←→ choice · Space toggle\nCtrl+S save · " + escapeAction
	if len(m.defs) > 0 && m.selected < len(m.defs) {
		def := m.defs[m.selected]
		if isChoiceList(def) {
			footer = actionsControl + "\n↑↓/Tab next control · Space/Enter toggle\nCtrl+S save · Esc cancel"
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
				footer += "\na Add (picker) · e Edit · Backspace Remove · m type path · b Browse"
			} else {
				footer += "\nEnter type path · b Browse"
			}
		}
	}
	footerLines := len(strings.Split(footer, "\n"))
	contextLines := 0
	if m.contextLine != "" {
		contextLines = 1
	}
	visible := max(1, m.height-6-footerLines-contextLines)
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
	head := strings.Split(heading+body, "\n")
	actions := actionsControl
	if m.readOnly {
		actions = "[ Back ]"
	}
	if !m.readOnly && m.selected == len(m.defs) {
		actions = focusedActionStyle.Render("> " + actionsControl)
	}
	if !m.readOnly && !m.hideCancel && m.selected == len(m.defs)+1 {
		actions = focusedActionStyle.Render(saveControl + "  > [ Cancel ]")
	}
	footer = strings.Replace(footer, actionsControl, actions, 1)
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
		if strings.Contains(line, actionsControl) {
			footerY = y
			break
		}
	}
	return formLayout{content: content, visibleFields: fieldLines[start:end], visibleRows: rowLines[start:end], visibleChoices: choiceLines[start:end], footerY: footerY, bodyStart: 3 + contextLines}
}

func (m *FormModel) submitActionLabel() string {
	if m.submitLabel != "" {
		return m.submitLabel
	}
	return "Save"
}

func (m *FormModel) splitLayout() formLayout {
	innerWidth := max(18, m.width-2)
	leftWidth := max(18, innerWidth/3)
	rightWidth := max(18, innerWidth-leftWidth-3)
	rightContentWidth := max(1, rightWidth-1) // Reserve the final pane cell for its scrollbar.
	values := m.editor.Values()
	sectionHeading := m.sectionHeading
	if sectionHeading == "" {
		sectionHeading = "Sections"
	}
	leftTitle := "── " + sectionHeading
	leftLines := []string{focusedPaneTitle(leftTitle, m.area == 0)}
	for i, section := range m.sections {
		prefix := "› "
		if i == m.sectionIndex {
			prefix = "> "
		}
		leftLines = append(leftLines, prefix+section.Title)
	}
	indices := m.splitFieldIndices(m.sectionIndex)
	rightTitle := "── " + m.sections[m.sectionIndex].Title
	rightLines := []string{focusedPaneTitle(rightTitle, m.area == 1)}
	rightFields := []int{-1}
	rightLineChoices := []int{-1}
	sectionContent := m.sectionContent[m.currentSectionKey()]
	for _, line := range sectionContent {
		for part, wrapped := range wrapCellText(line, max(1, rightWidth-2)) {
			prefix := "  "
			if part == 0 {
				prefix = "· "
			}
			rightLines = append(rightLines, prefix+wrapped)
			rightFields = append(rightFields, -1)
			rightLineChoices = append(rightLineChoices, -1)
		}
	}
	sectionActions := m.sectionActions[m.currentSectionKey()]
	for actionIndex, action := range sectionActions {
		label := "› [ " + action.Label + " ]"
		if action.Disabled != "" {
			label += " · " + action.Disabled
		}
		if actionIndex == m.sectionActionIndex && m.focusedAction {
			label = "> [ " + action.Label + " ]"
			if action.Disabled != "" {
				label += " · " + action.Disabled
			}
		}
		for _, wrapped := range wrapCellText(label, rightContentWidth) {
			rightLines = append(rightLines, wrapped)
			rightFields = append(rightFields, -1)
			rightLineChoices = append(rightLineChoices, actionIndex)
		}
	}
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
		choiceRows := []string{}
		choiceRowIndexes := []int{}
		if def.OptionsFrom != "" && len(def.Options) == 0 {
			display = m.emptyChoicesText(def)
		}
		if isEditableCollection(def) {
			rows := collectionRows(values[def.Name])
			unit := "items"
			if len(rows) == 1 {
				unit = "item"
			}
			display = fmt.Sprintf("%d %s", len(rows), unit)
			for row, value := range rows {
				cursor := "›"
				if index == m.selected && m.area == 1 && m.rowIndex[def.Name] == row {
					cursor = ">"
				}
				shown := textValue(value)
				if index == m.selected && m.editing && m.editAction == "edit" && m.rowIndex[def.Name] == row {
					shown = "Edit: " + editViewport(m.buffer, m.cursor, max(8, rightWidth-27))
				}
				limit := max(8, rightWidth-30)
				if !m.editing && lipgloss.Width(shown) > limit {
					shown = ansi.TruncateLeft(shown, lipgloss.Width(shown)-limit+1, "…")
				}
				display += "\n  " + cursor + " " + shown + "  [Edit] [Remove]"
			}
			addLabel := "Add item…"
			if def.Type == "directory" {
				addLabel = "Add directory…"
			} else if def.Type == "file" {
				addLabel = "Add file…"
			}
			cursor := "›"
			if index == m.selected && m.area == 1 && m.rowIndex[def.Name] == len(rows) {
				cursor = ">"
			}
			if m.editing && index == m.selected && m.editAction == "add" {
				display += "\n  > + Edit: " + editViewport(m.buffer, m.cursor, max(8, rightWidth-20))
			} else {
				display += "\n  " + cursor + " + " + addLabel
			}
		}
		if m.editing && index == m.selected && !isEditableCollection(def) && def.OptionsFrom == "" {
			display = "Edit: " + editViewport(m.buffer, m.cursor, max(8, rightWidth-lipgloss.Width(label)-14))
		}
		if validation := m.fieldErrors[def.Name]; validation != "" {
			display += "\n    ! " + validation
		}
		if len(def.Options) > 0 && (isChoiceList(def) || isScalarChoice(def)) {
			display = ""
			chosen, _ := values[def.Name].([]string)
			for optionIndex, option := range def.Options {
				selectedOption := false
				if isScalarChoice(def) {
					selectedOption = values[def.Name] == option.Value
				} else {
					for _, current := range chosen {
						if current == option.Value {
							selectedOption = true
							break
						}
					}
				}
				mark := "[ ]"
				if isScalarChoice(def) {
					mark = "( )"
				}
				if selectedOption {
					mark = "[x]"
					if isScalarChoice(def) {
						mark = "(*)"
					}
				}
				optionLabel := option.Label
				if optionLabel == "" {
					optionLabel = option.Value
				}
				if option.DisabledReason != "" {
					optionLabel += " — " + option.DisabledReason
				}
				cursor := "› "
				if index == m.selected && m.area == 1 && m.choiceIndex[def.Name] == optionIndex {
					cursor = "> "
				}
				// Choice rows receive an additional two-space indent below.
				for _, wrapped := range wrapCellText(cursor+mark+" "+optionLabel, max(1, rightContentWidth-2)) {
					choiceRows = append(choiceRows, wrapped)
					choiceRowIndexes = append(choiceRowIndexes, optionIndex)
				}
			}
		}
		if reason := m.disabledReason(def.Name); reason != "" {
			display += " — " + reason
		}
		rightLines = append(rightLines, prefix+label+": "+display)
		rightFields = append(rightFields, index)
		rightLineChoices = append(rightLineChoices, -1)
		for row, choice := range choiceRows {
			rightLines = append(rightLines, "  "+choice)
			rightFields = append(rightFields, index)
			rightLineChoices = append(rightLineChoices, choiceRowIndexes[row])
		}
		if hint := m.displayHint(def, values); hint != "" {
			// Both hint prefixes occupy six cells before the wrapped text.
			wrapped := wrapHint(hint, max(1, rightContentWidth-6))
			for line, part := range wrapped {
				if line == 0 {
					rightLines = append(rightLines, "    · "+part)
				} else {
					rightLines = append(rightLines, "      "+part)
				}
				rightFields = append(rightFields, -1)
				rightLineChoices = append(rightLineChoices, -1)
			}
		}
		if (def.Type == "directory" || def.Type == "file") && !def.Multiple {
			control := "  › [Browse · b]"
			if index == m.selected && m.area == 1 {
				control = "  > [Browse · b]"
			}
			rightLines = append(rightLines, control)
			rightFields = append(rightFields, index)
			rightLineChoices = append(rightLineChoices, -2)
		}
	}
	if len(indices) == 0 {
		if len(sectionContent) == 0 {
			rightLines = append(rightLines, "· No fields in this section")
			rightFields = append(rightFields, -1)
			rightLineChoices = append(rightLineChoices, -1)
		}
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
				if i < len(rightLineChoices) && rightLineChoices[i] != -1 {
					choice = rightLineChoices[i]
				} else if part > 0 && field >= 0 && (isChoiceList(m.defs[field]) || isScalarChoice(m.defs[field]) || isEditableCollection(m.defs[field])) {
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
			areaLabels[i] = focusedActionStyle.Render("[" + areaLabels[i] + "]")
		}
	}
	saveControl := "[ " + m.submitActionLabel() + " ]"
	actionsControl := saveControl
	if !m.hideCancel {
		actionsControl += "  [ Cancel ]"
	}
	actions := actionsControl
	if m.readOnly {
		actions = "[ Back ]"
	}
	if m.area == 2 && !m.readOnly {
		if m.actionIndex == 0 {
			actions = focusedActionStyle.Render("> " + actionsControl)
		} else if !m.hideCancel {
			actions = focusedActionStyle.Render(saveControl + "  > [ Cancel ]")
		}
	}
	escapeAction := "Esc Cancel"
	if m.backNavigation {
		escapeAction = "Esc Back"
	}
	controls := "↑↓ Controls · ←→ Panes · Tab Areas · Ctrl-S Save · " + escapeAction
	if m.readOnly {
		controls = "↑↓ Navigate · ←→ Panes · Tab Areas · Esc Back"
	}
	footer := strings.Join(areaLabels, " · ") + "\n" + controls + "\n" + actions
	if m.selected < len(m.defs) && isEditableCollection(m.defs[m.selected]) {
		if def := m.defs[m.selected]; def.Type == "directory" || def.Type == "file" {
			footer = "Enter edit · a Add (picker) · Backspace Remove · m Type path · b Browse\n" + footer
		} else {
			footer = "Enter edit/add · a Add · Backspace Remove\n" + footer
		}
	}
	if m.editing {
		footer = "Editing in place · Enter finish · Esc restore · Ctrl+U clear\n" + footer
	}
	if m.message != "" {
		footer = m.message + "\n" + footer
	}
	if m.height <= 12 && (m.selected >= len(m.defs) || !isEditableCollection(m.defs[m.selected])) {
		compact := "↑↓ Move · ←→ Pane · Tab Area · Ctrl-S Save · " + escapeAction
		footer = strings.Join(areaLabels, " · ") + "\n" + compact + "\n" + actions
		if m.message != "" {
			footer = m.message + "\n" + footer
		}
	}
	footerLines := strings.Split(footer, "\n")
	header := []string{m.title}
	if m.contextLine != "" {
		header = append(header, m.contextLine)
	}
	header = append(header, "")
	bodyStart := 1 + len(header)
	bodyHeight := max(1, m.height-2-len(header)-1-len(footerLines))
	leftScrollable := len(leftLines) > bodyHeight
	leftRows, splitSections, _ := scrollSplitPane(leftLines, leftSections, nil, m.sectionIndex+1, bodyHeight)
	rightTarget := 0
	if len(indices) == 0 && len(sectionContent) > 0 {
		rightTarget = min(max(0, m.sectionOffset+1), len(rightRows)-1)
	}
	if m.focusedAction {
		for row, actionIndex := range rightRowChoices {
			if actionIndex == m.sectionActionIndex && rightRowFields[row] < 0 {
				rightTarget = row
				break
			}
		}
	}
	for row, field := range rightRowFields {
		if m.detailScrolled {
			break
		}
		if m.area != 1 {
			break
		}
		if field != m.selected {
			continue
		}
		def := m.defs[field]
		if !isEditableCollection(def) && (def.Type == "file" || def.Type == "directory") {
			for row, rowField := range rightRowFields {
				if rowField == field && rightRowChoices[row] == -2 {
					rightTarget = row
					break
				}
			}
		} else if isEditableCollection(def) {
			if rightRowChoices[row] == m.rowIndex[def.Name] {
				rightTarget = row
			}
		} else if rightRowChoices[row] < 0 || rightRowChoices[row] == m.choiceIndex[def.Name] {
			rightTarget = row
		}
	}
	var splitFields, splitChoices []int
	rightTotal := len(rightRows)
	rightScrollable := rightTotal > bodyHeight
	if m.detailScrolled && len(rightRows) > 1 {
		detailRows, detailFields, detailChoices := scrollSplitPaneAt(rightRows[1:], rightRowFields[1:], rightRowChoices[1:], m.detailOffset, bodyHeight-1)
		rightRows = append([]string{rightRows[0]}, detailRows...)
		splitFields = append([]int{-1}, detailFields...)
		splitChoices = append([]int{-1}, detailChoices...)
	} else {
		rightRows, splitFields, splitChoices = scrollSplitPane(rightRows, rightRowFields, rightRowChoices, rightTarget, bodyHeight)
	}
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#081f5b")).Background(lipgloss.Color("#e9f2fb"))
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a8bddb"))
	body := make([]string, max(len(leftRows), len(rightRows)))
	removeX := make([]int, len(body))
	for i := range body {
		removeX[i] = -1
		left, right := "", ""
		if i < len(leftRows) {
			left = leftRows[i]
		}
		if i < len(rightRows) {
			right = rightRows[i]
		}
		left = ansi.Truncate(left, max(1, leftWidth-1), "")
		right = ansi.Truncate(right, max(1, rightWidth-1), "")
		if i < len(splitFields) && splitFields[i] >= 0 && i < len(splitChoices) && splitChoices[i] >= 0 && isEditableCollection(m.defs[splitFields[i]]) {
			if index := strings.Index(right, "[Remove]"); index >= 0 {
				removeX[i] = 1 + leftWidth + 3 + lipgloss.Width(right[:index])
			}
		}
		leftCell := left + strings.Repeat(" ", max(0, leftWidth-1-lipgloss.Width(left)))
		leftCell += scrollbarGlyph(leftScrollable, i, len(leftLines), m.sectionIndex+1, bodyHeight)
		if m.area == 0 && i < len(splitSections) && splitSections[i] == m.sectionIndex {
			leftCell = selectedStyle.Render(leftCell)
		}
		rightCell := right + strings.Repeat(" ", max(0, rightWidth-1-lipgloss.Width(right)))
		rightCell += scrollbarGlyph(rightScrollable, i, rightTotal, rightTarget, bodyHeight)
		selected := false
		if m.area == 1 && i < len(splitFields) && splitFields[i] == m.selected {
			selected = true
			if choice := splitChoices[i]; choice >= 0 {
				def := m.defs[m.selected]
				selected = choice == m.choiceIndex[def.Name]
			}
		}
		if m.area == 1 && m.focusedAction && i < len(splitFields) && splitFields[i] < 0 && i < len(splitChoices) {
			selected = splitChoices[i] == m.sectionActionIndex
		}
		if selected {
			rightCell = selectedStyle.Render(rightCell)
		} else if i < len(splitFields) && splitFields[i] >= 0 && m.exclusiveInactive(m.defs[splitFields[i]].Name, values) {
			rightCell = inactiveStyle.Render(rightCell)
		}
		body[i] = leftCell + " │ " + rightCell
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
		if strings.Contains(line, saveControl) {
			footerY = y
			break
		}
	}
	return formLayout{content: content, visibleFields: indices, footerY: footerY, bodyStart: bodyStart, split: true, splitLeftWidth: leftWidth, splitFields: splitFields, splitChoices: splitChoices, splitSections: splitSections, splitRemoveX: removeX}
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

func scrollbarGlyph(scrollable bool, row, total, target, height int) string {
	if !scrollable || height <= 0 {
		return " "
	}
	thumb := 0
	if total > 1 {
		thumb = int(float64(height-1) * float64(max(0, min(target, total-1))) / float64(total-1))
	}
	if row == thumb {
		return "█"
	}
	return "│"
}

func scrollSplitPaneAt(rows []string, primary, secondary []int, start, height int) ([]string, []int, []int) {
	if len(rows) <= height {
		return rows, primary, secondary
	}
	visibleCount := max(1, height-2)
	start = max(0, min(start, len(rows)-visibleCount))
	end := min(len(rows), start+visibleCount)
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
	if m.browser != nil {
		return m.pickerView()
	}
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
