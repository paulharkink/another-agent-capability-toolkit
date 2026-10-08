package forms

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"strings"
	"testing"
)

func TestBooleanInputCheckboxAndTypedFalse(t *testing.T) {
	def := catalog.Input{Name: "enabled", Label: "Enabled", Type: "boolean", Required: true, Default: false}
	values, err := Resolve([]catalog.Input{def}, map[string]any{"enabled": "false"})
	if err != nil || values["enabled"] != false {
		t.Fatalf("typed required false: %#v, %v", values, err)
	}
	m := NewForm(context.Background(), []catalog.Input{def}, values)
	if !strings.Contains(m.View().Content, "[ ]") || m.HasUnsavedChanges() {
		t.Fatalf("unchecked or clean state missing: %s", m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if m.Values()["enabled"] != true || !strings.Contains(m.View().Content, "[x]") {
		t.Fatalf("checkbox did not toggle: %s", m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Values()["enabled"] != false || m.HasUnsavedChanges() {
		t.Fatal("returning checkbox to false did not restore clean draft")
	}
}

func TestBooleanInputFixedAndReset(t *testing.T) {
	def := catalog.Input{Name: "enabled", Type: "boolean", Default: false}
	m := NewForm(context.Background(), []catalog.Input{def}, map[string]any{"enabled": true})
	m.SetResetValue("enabled", false, true)
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.Values()["enabled"] != false {
		t.Fatal("reset lost typed false")
	}
	m.SetDisabled("enabled", "Fixed by profile")
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if m.Values()["enabled"] != false || !strings.Contains(m.View().Content, "Fixed by profile") {
		t.Fatal("fixed Boolean changed or lost lock reason")
	}
}

func TestBooleanInputSplitMouseToggle(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "enabled", Label: "Enabled", Type: "boolean", Default: false}}, nil)
	m.SetSections(FormSection{ID: "options", Title: "Options", Fields: []string{"enabled"}})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	clickVisibleText(t, m, "Enabled:")
	if m.Values()["enabled"] != true || !strings.Contains(m.View().Content, "[x]") {
		t.Fatal("mouse did not toggle the split Boolean checkbox")
	}
	m.SetDisabled("enabled", "Fixed by profile")
	clickVisibleText(t, m, "Enabled:")
	if m.Values()["enabled"] != true {
		t.Fatal("mouse changed a fixed true Boolean")
	}
}
