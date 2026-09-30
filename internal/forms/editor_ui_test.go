package forms

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
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
