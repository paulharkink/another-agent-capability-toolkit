package forms

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func key(code rune, text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: text} }
func formDefaultBackgroundGlyphs(content string) int {
	painted, count := false, 0
	for i := 0; i < len(content); {
		if content[i] == '\x1b' && i+1 < len(content) && content[i+1] == '[' {
			end := strings.IndexByte(content[i:], 'm')
			if end > 0 {
				parameters := content[i+2 : i+end]
				if parameters == "" || parameters == "0" || parameters == "49" {
					painted = false
				}
				if strings.Contains(parameters, "48;2;") {
					painted = true
				}
				i += end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(content[i:])
		if r != '\n' && r != '\r' && !painted {
			count++
		}
		i += size
	}
	return count
}

func TestFormHasNoDefaultBackgroundGlyphs(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Label: "Token", Type: "secret"}}, nil)
	m.SetTitle("Install · Cluster Inspector")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if got := formDefaultBackgroundGlyphs(m.View().Content); got != 0 {
		t.Fatalf("form has %d default-background glyphs", got)
	}
}
func TestKeyboardFormEditsPrefillAndSaves(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "team", Type: "string", Required: true}}, map[string]any{"team": "old"})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('n', "new"))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, e := m.Result()
	if e != nil || got["team"] != "new" {
		t.Fatalf("%v %v", got, e)
	}
}

func TestLongFormKeepsSelectedFieldAndSaveVisible(t *testing.T) {
	defs := make([]catalog.Input, 30)
	for i := range defs {
		defs[i] = catalog.Input{Name: fmt.Sprintf("field_%02d", i), Label: fmt.Sprintf("Field %02d", i), Type: "string"}
	}
	m := NewForm(context.Background(), defs, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	for i := 0; i < 25; i++ {
		m.Update(key(tea.KeyDown, ""))
	}
	view := m.View().Content
	if !strings.Contains(view, "Field 25") || !strings.Contains(view, "Ctrl+S save") || strings.Contains(view, "Field 00") {
		t.Fatalf("long form did not scroll to selected field with footer visible:\n%s", view)
	}
	if lines := len(strings.Split(view, "\n")); lines > 16 {
		t.Fatalf("form exceeds terminal height (%d lines):\n%s", lines, view)
	}
}
func TestFormUsesFullHeightCommanderFrame(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "repo", Label: "Repository", Type: "string", Required: true}}, map[string]any{"repo": "/repos/team"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) != 24 || !strings.HasPrefix(lines[0], "╔") || !strings.HasPrefix(lines[len(lines)-1], "╚") {
		t.Fatalf("form is not a full-height framed screen (%d lines):\n%s", len(lines), m.View().Content)
	}
	if !strings.Contains(lines[len(lines)-3], "[ Save ]") && !strings.Contains(lines[len(lines)-4], "[ Save ]") {
		t.Fatalf("Save is not pinned to the bottom:\n%s", m.View().Content)
	}
}

func TestFormUsesApprovedBlueBackground(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "repo", Type: "string"}}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View().Content, "48;2;9;38;111m") {
		t.Fatal("setup form lacks the approved navy blue background")
	}
}

func TestFormHighlightsSelectedInput(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Label: "Token", Type: "secret"}}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View().Content, "48;2;233;242;251m") {
		t.Fatal("selected setup input has no light highlight")
	}
}

func TestNewCollectionItemRemainsVisibleInShortForm(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "directories", Type: "string", Multiple: true}}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	for i := 0; i < 14; i++ {
		if i == 0 {
			m.Update(key(tea.KeyEnter, ""))
		} else {
			m.Update(key('a', "a"))
		}
		m.Update(key('v', fmt.Sprintf("directory-%02d", i)))
		m.Update(key(tea.KeyEnter, ""))
	}
	if !strings.Contains(m.View().Content, "directory-13") {
		t.Fatalf("newly added collection item is off screen:\n%s", m.View().Content)
	}
}
func TestFramedFormFitsMinimumTerminalWithHintsAndEdit(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "source", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "credentials", Required: true}, {Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "credentials"}}, nil)
	m.SetHint("source", "target · production.toml · /very/long/path/to/production.toml")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.beginEdit("apply", "bad")
	m.message = "Selected path could not be read"
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > 16 || !strings.Contains(m.View().Content, "[ Save ]") || !strings.Contains(m.View().Content, m.message) {
		t.Fatalf("minimum form overflowed or hid controls/error (%d lines):\n%s", len(lines), m.View().Content)
	}
}
func TestKeyboardFormShowsSecretsAndCancels(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Type: "secret"}}, map[string]any{"token": "very-private-test-value"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View().Content, "very-private-test-value") {
		t.Fatal("secret value was hidden despite the approved visible-input design")
	}
	m.Update(key(tea.KeyEnter, ""))
	if !strings.Contains(m.View().Content, "Edit: very-private-test-value_") {
		t.Fatal("secret edit buffer was hidden")
	}
	m.Update(key(tea.KeyEscape, ""))
	m.Update(key(tea.KeyEscape, ""))
	if _, e := m.Result(); e != picker.ErrCancelled {
		t.Fatal(e)
	}
}

func TestExclusiveCredentialFormExplainsAutomaticClear(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "cluster_credentials"},
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "cluster_credentials"},
	}, map[string]any{"kubeconfig": "/tmp/config"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	view := m.View().Content
	if !strings.Contains(view, "Source kubeconfig") || !strings.Contains(view, "clears") {
		t.Fatalf("credential switch behavior was not explained: %s", view)
	}
}
func TestFormRequiredValidationKeepsFormOpen(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "required", Type: "string", Required: true}}, nil)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("quit despite validation failure")
	}
	if !strings.Contains(m.View().Content, "required") {
		t.Fatalf("%s", m.View().Content)
	}
}
func TestMultipleChoiceKeyboardRemainsArray(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "teams", Type: "multichoice", Options: []catalog.Choice{{Value: "alpha"}, {Value: "beta"}}}}, nil)
	m.Update(key(tea.KeySpace, " "))
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeySpace, " "))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, e := m.Result()
	if e != nil || len(got["teams"].([]string)) != 2 {
		t.Fatalf("%v %v", got, e)
	}
}

func TestTargetBackedMultiChoiceCannotCreateEmptyRows(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "connections", Type: "multichoice", OptionsFrom: "dbms.*.tenants.*"}}, nil)
	m.Update(key(tea.KeyEnter, ""))
	if m.editing {
		t.Fatal("Enter opened a free-text editor for target-backed choices")
	}
	if !strings.Contains(m.View().Content, "No choices are currently available") {
		t.Fatalf("missing empty-choice explanation: %s", m.View().Content)
	}
}

func TestTargetBackedMultiChoiceDeselectShowsNoArraySyntax(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{
		Name: "connections", Type: "multichoice", OptionsFrom: "dbms.*.tenants.*",
		Options: []catalog.Choice{{Value: "shared_postgres/plane", Label: "Plane — shared_postgres/plane"}},
	}}, nil)
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEnter, ""))
	view := m.View().Content
	if strings.Contains(view, "connections: []") || !strings.Contains(view, "[ ] Plane — shared_postgres/plane") || !strings.Contains(view, "Space/Enter toggle") {
		t.Fatalf("empty database selection rendered as array syntax or lost checkbox: %s", view)
	}
}

func TestMultiChoiceRowsAreFlatKeyboardControls(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "before", Label: "Before", Type: "string"},
		{Name: "databases", Label: "Databases", Type: "multichoice", Options: []catalog.Choice{{Value: "plane", Label: "Plane"}, {Value: "honcho", Label: "Honcho"}}},
		{Name: "after", Label: "After", Type: "string"},
	}, nil)
	m.Update(key(tea.KeyDown, ""))
	if m.selected != 1 || m.choiceIndex["databases"] != 0 || strings.Contains(m.View().Content, "> Databases:") || !strings.Contains(m.View().Content, "> [ ] Plane") {
		t.Fatalf("first checkbox did not receive clear focus: %s", m.View().Content)
	}
	m.Update(key(tea.KeyEnter, ""))
	if got := m.editor.Values()["databases"]; !reflect.DeepEqual(got, []string{"plane"}) {
		t.Fatalf("Enter did not toggle focused checkbox: %#v", got)
	}
	m.Update(key(tea.KeyTab, ""))
	if m.selected != 1 || m.choiceIndex["databases"] != 1 {
		t.Fatalf("Tab skipped the second checkbox: selected=%d choice=%d", m.selected, m.choiceIndex["databases"])
	}
	m.Update(key(tea.KeyDown, ""))
	if m.selected != 2 {
		t.Fatalf("Down did not leave checkbox list: selected=%d", m.selected)
	}
	m.Update(key(tea.KeyUp, ""))
	if m.selected != 1 || m.choiceIndex["databases"] != 1 {
		t.Fatalf("Up did not enter checkbox list at its last row: selected=%d choice=%d", m.selected, m.choiceIndex["databases"])
	}
}

func TestRequiredDestinationsErrorNamesVisibleControl(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "__aact_destinations", Label: "Destinations", Type: "multichoice", Required: true, Options: []catalog.Choice{{Value: "codex", Label: "Codex"}}}}, nil)
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !strings.Contains(m.message, "Select at least one destination") || strings.Contains(m.message, "__aact_destinations") || strings.Contains(m.message, "--interactive") {
		t.Fatalf("Save error is not actionable: %q", m.message)
	}
	if m.done || m.selected != 0 || m.choiceIndex["__aact_destinations"] != 0 {
		t.Fatalf("Save did not leave destination choice focused: done=%t selected=%d choice=%d", m.done, m.selected, m.choiceIndex["__aact_destinations"])
	}
}

func TestClickingChoiceHeadingOnlyFocusesFirstCheckbox(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "databases", Label: "Databases", Type: "multichoice", Options: []catalog.Choice{{Value: "plane", Label: "Plane"}}}}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	clickVisibleText(t, m, "Databases:")
	if got := m.editor.Values()["databases"]; len(collectionRows(got)) != 0 {
		t.Fatalf("clicking heading changed selection: %#v", got)
	}
	if m.selected != 0 || m.choiceIndex["databases"] != 0 {
		t.Fatal("clicking heading did not focus first checkbox")
	}
}

func TestTypedCollectionsKeyboardAddEditRemove(t *testing.T) {
	for _, tc := range []struct {
		kind, first, second, replacement string
		want                             any
	}{
		{"string", "alpha", "beta", "gamma", []string{"gamma"}},
		{"integer", "1", "2", "3", []int64{3}},
		{"number", "1.5", "2.5", "3.5", []float64{3.5}},
		{"boolean", "true", "false", "false", []bool{false}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			m := NewForm(context.Background(), []catalog.Input{{Name: "items", Type: tc.kind, Multiple: true, Required: true}}, nil)
			for i, v := range []string{tc.first, tc.second} {
				if i == 0 {
					m.Update(key(tea.KeyEnter, ""))
				} else {
					m.Update(key('a', "a"))
				}
				m.Update(key('v', v))
				m.Update(key(tea.KeyEnter, ""))
			}
			if !strings.Contains(m.View().Content, "r Remove") {
				t.Fatalf("collection controls missing: %s", m.View().Content)
			}
			m.Update(key(']', "]"))
			m.Update(key('r', "r"))
			m.Update(key('e', "e"))
			m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			m.Update(key('v', tc.replacement))
			m.Update(key(tea.KeyEnter, ""))
			m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			got, err := m.Result()
			if err != nil || !reflect.DeepEqual(got["items"], tc.want) {
				t.Fatalf("got %v, err %v, want %v", got, err, tc.want)
			}
		})
	}
}
func TestTypedCollectionInvalidEditPreservesRows(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "ports", Type: "integer", Multiple: true}}, map[string]any{"ports": []int64{8080}})
	m.Update(key('e', "e"))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('x', "bad"))
	m.Update(key(tea.KeyEnter, ""))
	if !m.editing || !reflect.DeepEqual(m.editor.Values()["ports"], []int64{8080}) {
		t.Fatalf("invalid edit changed rows: %v", m.editor.Values())
	}
	m.Update(key(tea.KeyEscape, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if _, err := m.Result(); err != nil {
		t.Fatal(err)
	}
}

func clickVisibleText(t *testing.T, m *FormModel, needle string) tea.Cmd {
	t.Helper()
	for y, line := range strings.Split(m.View().Content, "\n") {
		plain := ansi.Strip(line)
		if index := strings.Index(plain, needle); index >= 0 {
			_, cmd := m.Update(tea.MouseClickMsg{X: lipgloss.Width(plain[:index]), Y: y, Button: tea.MouseLeft})
			return cmd
		}
	}
	t.Fatalf("%q not visible in form:\n%s", needle, m.View().Content)
	return nil
}

func TestMouseCanEditVisibleFieldAndSaveCurrentBuffer(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "name", Label: "Name", Type: "string", Required: true},
		{Name: "team", Label: "Team", Type: "string", Required: true},
	}, map[string]any{"name": "original", "team": "old"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	clickVisibleText(t, m, "Team *:")
	if m.selected != 1 || !m.editing {
		t.Fatalf("field click did not open Team editor: selected=%d editing=%t", m.selected, m.editing)
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('n', "new"))
	clickVisibleText(t, m, "[ Save ]")
	got, err := m.Result()
	if err != nil || got["name"] != "original" || got["team"] != "new" {
		t.Fatalf("mouse Save did not apply edited field: %v, %v", got, err)
	}
}

func TestSplitFormMouseCanSelectSectionToggleControlAndCancel(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret"},
		{Name: "destinations", Label: "Destinations", Type: "multichoice", Options: []catalog.Choice{{Value: "codex", Label: "Codex"}}},
	}, nil)
	m.SetSections(FormSection{Title: "Authentication", Fields: []string{"token"}}, FormSection{Title: "Destinations", Fields: []string{"destinations"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})
	layout := m.layout()
	m.Update(tea.MouseClickMsg{X: 2, Y: layout.bodyStart + 2, Button: tea.MouseLeft})
	if m.sectionIndex != 1 || m.area != 0 {
		t.Fatalf("left pane click did not select Destinations: section=%d area=%d", m.sectionIndex, m.area)
	}
	layout = m.layout()
	m.Update(tea.MouseClickMsg{X: layout.splitLeftWidth + 8, Y: layout.bodyStart + 2, Button: tea.MouseLeft})
	selected, _ := m.editor.Values()["destinations"].([]string)
	if len(selected) != 1 || selected[0] != "codex" {
		t.Fatalf("right-pane checkbox click did not select Codex: %v\n%s", selected, m.View().Content)
	}
	layout = m.layout()
	m.Update(tea.MouseClickMsg{X: 1 + len("[ Save ]  "), Y: layout.footerY, Button: tea.MouseLeft})
	if !m.done || m.err != picker.ErrCancelled {
		t.Fatalf("fixed Cancel click did not close the split form: done=%t err=%v", m.done, m.err)
	}
}

func TestMouseChoicesAndBooleanUseEditorValues(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "enabled", Label: "Enabled", Type: "boolean"},
		{Name: "targets", Label: "Targets", Type: "multichoice", Options: []catalog.Choice{{Value: "one"}, {Value: "two"}}},
	}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	clickVisibleText(t, m, "Enabled:")
	clickVisibleText(t, m, "[ ] one")
	if got := m.editor.Values()["targets"]; !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("choice click did not toggle first choice: %v", got)
	}
	clickVisibleText(t, m, "[ ] two")
	if got := m.editor.Values()["targets"]; !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("choice click did not toggle second choice: %v", got)
	}
	if got := m.editor.Values()["enabled"]; got != true {
		t.Fatalf("boolean click did not toggle value: %v", got)
	}
}

func TestMouseClickAfterScrollTargetsVisibleField(t *testing.T) {
	defs := make([]catalog.Input, 30)
	for i := range defs {
		defs[i] = catalog.Input{Name: fmt.Sprintf("field_%02d", i), Label: fmt.Sprintf("Field %02d", i), Type: "string"}
	}
	m := NewForm(context.Background(), defs, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	for i := 0; i < 25; i++ {
		m.Update(key(tea.KeyDown, ""))
	}
	clickVisibleText(t, m, "Field 23:")
	if m.selected != 23 || !m.editing {
		t.Fatalf("scroll hit mapped to wrong field: selected=%d editing=%t", m.selected, m.editing)
	}
}

func TestMouseSaveValidatesAndCancelCancels(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "required", Label: "Required", Type: "string", Required: true}}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	if cmd := clickVisibleText(t, m, "[ Save ]"); cmd != nil || m.done {
		t.Fatal("Save click bypassed required validation")
	}
	clickVisibleText(t, m, "[ Cancel ]")
	if _, err := m.Result(); err != picker.ErrCancelled {
		t.Fatalf("Cancel click did not cancel: %v", err)
	}
}

func TestDestinationChoicesAreVisibleAndToggleIndependently(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "agents", Label: "Destinations", Type: "multichoice", Options: []catalog.Choice{{Value: "codex", Label: "Codex — ~/.codex"}, {Value: "opencode", Label: "OpenCode — ~/.config/opencode"}}}}, map[string]any{"agents": []string{"codex"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"[x] Codex — ~/.codex", "[ ] OpenCode — ~/.config/opencode"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing visible destination %q:\n%s", want, view)
		}
	}
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeySpace, " "))
	if got := m.editor.Values()["agents"]; !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Fatalf("toggle changed wrong destinations: %v", got)
	}
}

func TestFormButtonsReceiveKeyboardFocus(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Label: "Name", Type: "string", Required: true}}, map[string]any{"name": "existing"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	m.Update(key(tea.KeyDown, ""))
	if !strings.Contains(ansi.Strip(m.View().Content), "> [ Save ]") {
		t.Fatal("Save button has no keyboard focus")
	}
	m.Update(key(tea.KeyEnter, ""))
	if got, err := m.Result(); err != nil || got["name"] != "existing" {
		t.Fatalf("Enter on Save did not submit: %v %v", got, err)
	}
}

func TestDestinationsDownThenTabNeverSubmits(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "agents", Label: "Destinations", Type: "multichoice", Options: []catalog.Choice{{Value: "codex", Label: "Codex"}, {Value: "opencode", Label: "OpenCode"}}}}, nil)
	m.Update(key(tea.KeySpace, " "))
	m.Update(key(tea.KeyDown, ""))
	if m.selected != 0 || m.choiceIndex["agents"] != 1 {
		t.Fatalf("Down should focus next destination: field=%d option=%d", m.selected, m.choiceIndex["agents"])
	}
	m.Update(key(tea.KeySpace, " "))
	m.Update(key(tea.KeyTab, ""))
	if m.done || m.selected != len(m.defs) {
		t.Fatalf("Tab submitted instead of focusing Save: done=%t selected=%d", m.done, m.selected)
	}
	if got := m.editor.Values()["agents"]; !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Fatalf("destination selection changed: %v", got)
	}
	m.Update(key(tea.KeySpace, " "))
	if m.done {
		t.Fatal("Space on Save submitted without explicit Enter")
	}
	m.Update(key(tea.KeyEnter, ""))
	if _, err := m.Result(); err != nil {
		t.Fatalf("Enter on Save did not submit: %v", err)
	}
}

func TestFormValidationKeepsDraftAndFocusesInvalidInput(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Label: "Name", Type: "string", Required: true}, {Name: "note", Label: "Note", Type: "string"}}, map[string]any{"note": "draft"})
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyDown, ""))
	m.Update(key(tea.KeyEnter, ""))
	if m.done || m.selected != 0 || m.editor.Values()["note"] != "draft" {
		t.Fatalf("validation lost draft/focus: done=%t selected=%d values=%v", m.done, m.selected, m.editor.Values())
	}
}

func TestTextEditorMovesCursorWithinValue(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "host", Label: "Host", Type: "string"}}, map[string]any{"host": "ac"})
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyLeft, ""))
	m.Update(key('b', "b"))
	if !strings.Contains(ansi.Strip(m.View().Content), "Edit: ab_c") {
		t.Fatalf("cursor position not shown: %s", m.View().Content)
	}
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if got, err := m.Result(); err != nil || got["host"] != "abc" {
		t.Fatalf("cursor edit failed: %v %v", got, err)
	}
}

func TestClickVisibleDestinationTogglesThatDestination(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "agents", Label: "Destinations", Type: "multichoice", Options: []catalog.Choice{{Value: "codex", Label: "Codex"}, {Value: "opencode", Label: "OpenCode"}}}}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	clickVisibleText(t, m, "[ ] OpenCode")
	if got := m.editor.Values()["agents"]; !reflect.DeepEqual(got, []string{"opencode"}) {
		t.Fatalf("click selected wrong destination: %v", got)
	}
}

func TestTabCommitsTextAndMovesToNextControl(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}, {Name: "note", Type: "string"}}, nil)
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key('x', "x"))
	m.Update(key(tea.KeyTab, ""))
	if m.editing || m.selected != 1 || m.editor.Values()["name"] != "x" {
		t.Fatalf("Tab did not commit and advance: editing=%t selected=%d values=%v", m.editing, m.selected, m.editor.Values())
	}
}

func TestConditionalFieldIsSkippedAndClickExplainsWhy(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "method", Label: "Authentication", Type: "choice", Options: []catalog.Choice{{Value: "token", Label: "Token"}, {Value: "kube", Label: "Source kubeconfig"}}},
		{Name: "token", Label: "Token", Type: "secret"},
		{Name: "kube", Label: "Source kubeconfig", Type: "file"},
	}, map[string]any{"method": "kube"})
	m.SetConditional("token", "method", "token")
	m.SetConditional("kube", "method", "kube")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(key(tea.KeyDown, ""))
	if m.selected != 2 {
		t.Fatalf("Down focused inactive token field: selected=%d", m.selected)
	}
	clickVisibleText(t, m, "Token: ")
	if m.selected != 2 || m.editing || !strings.Contains(m.View().Content, "inactive while Source kubeconfig") {
		t.Fatal("inactive field click changed focus or hid reason")
	}
}

func TestPrefilledChoiceShowsItsSelectedOption(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "method", Label: "Authentication", Type: "choice", Options: []catalog.Choice{{Value: "token", Label: "Token"}, {Value: "kube", Label: "Source kubeconfig"}}}}, map[string]any{"method": "kube"})
	if !strings.Contains(ansi.Strip(m.View().Content), "[Source kubeconfig]") {
		t.Fatalf("prefilled selector cursor disagrees with saved value: %s", m.View().Content)
	}
}
