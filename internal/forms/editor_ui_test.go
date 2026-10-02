package forms

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"reflect"
	"strings"
	"testing"
)

func key(code rune, text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: text} }
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
func TestKeyboardFormMasksSecretsAndCancels(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Type: "secret"}}, map[string]any{"token": "very-private-test-value"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(m.View().Content, "very-private-test-value") {
		t.Fatal("secret rendered")
	}
	m.Update(key(tea.KeyEscape, ""))
	if _, e := m.Result(); e != picker.ErrCancelled {
		t.Fatal(e)
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
		if x := strings.Index(line, needle); x >= 0 {
			_, cmd := m.Update(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
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

func TestMouseChoicesAndBooleanUseEditorValues(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "enabled", Label: "Enabled", Type: "boolean"},
		{Name: "targets", Label: "Targets", Type: "multichoice", Options: []catalog.Choice{{Value: "one"}, {Value: "two"}}},
	}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	clickVisibleText(t, m, "Enabled:")
	clickVisibleText(t, m, "Targets:")
	if got := m.editor.Values()["targets"]; !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("choice click did not toggle first choice: %v", got)
	}
	m.Update(key(tea.KeyRight, ""))
	clickVisibleText(t, m, "Targets:")
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
