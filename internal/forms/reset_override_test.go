package forms

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestCtrlRRestoresInheritedValueAndMarksSavedOverrideForRemoval(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "saved-url"})
	m.SetResetValue("endpoint", "inherited-url", true)
	m.SetHint("endpoint", "Saved override · Ctrl+R restore inherited value")

	view := m.View().Content
	if !strings.Contains(view, "Ctrl+R restore inherited value") {
		t.Fatalf("saved override reset action is not discoverable in the field details:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

	if got := m.Values()["endpoint"]; got != "inherited-url" {
		t.Fatalf("Ctrl+R left the saved value in the field: %#v", got)
	}
	if !m.HasUnsavedChanges() {
		t.Fatal("removing an override was not treated as an unsaved change when values matched")
	}
	if labels, _ := m.ChangedFieldSummary(5); !reflect.DeepEqual(labels, []string{"Endpoint"}) {
		t.Fatalf("reset intent was missing from the unsaved summary: %#v", labels)
	}
	if got := m.ResetFields(); !reflect.DeepEqual(got, []string{"endpoint"}) {
		t.Fatalf("reset field was not marked for override removal: %#v", got)
	}
}

func TestCtrlRCanClearSavedValueWhenNoInheritedValueExists(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "saved-url"})
	m.SetResetValue("endpoint", nil, false)
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

	if _, ok := m.Values()["endpoint"]; ok {
		t.Fatalf("reset without an inherited value must leave the saved value unset: %#v", m.Values())
	}
	if got := m.ResetFields(); !reflect.DeepEqual(got, []string{"endpoint"}) {
		t.Fatalf("reset field was not marked for override removal: %#v", got)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, ok := m.Values()["endpoint"]; ok {
		t.Fatalf("opening and closing an unchanged empty field turned reset into a blank override: %#v", m.Values())
	}
	if got := m.ResetFields(); !reflect.DeepEqual(got, []string{"endpoint"}) {
		t.Fatalf("unchanged empty field edit cancelled reset intent: %#v", got)
	}
}

func TestEditingRestoredValueChangesHintBackToUnsavedOverride(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "endpoint", Label: "Endpoint [saved]", Type: "string"}}, map[string]any{"endpoint": "saved-url"})
	m.SetResetValue("endpoint", "inherited-url", true)
	m.SetResetPresentation("endpoint", "Endpoint [environment]", "Environment default · home.toml")
	m.SetOverridePresentation("endpoint", "Endpoint [unsaved override]", "Will save as an override · Ctrl+R restore inherited value")
	m.SetHint("endpoint", "Saved override · Ctrl+R restore inherited value")
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "custom-url"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View().Content
	if !strings.Contains(view, "Will save as an override") || !strings.Contains(view, "Endpoint [unsaved override]") {
		t.Fatalf("editing after restore left stale inherited provenance visible:\n%s", view)
	}
	if got := m.ResetFields(); len(got) != 0 {
		t.Fatalf("edited value still carries reset intent: %#v", got)
	}

	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	view = m.View().Content
	if !strings.Contains(view, "Environment default · home.toml") || !strings.Contains(view, "Endpoint [environment]") {
		t.Fatalf("Ctrl+R did not restore inherited provenance after editing:\n%s", view)
	}
	if got := m.ResetFields(); !reflect.DeepEqual(got, []string{"endpoint"}) {
		t.Fatalf("second restore did not re-enable removal intent: %#v", got)
	}
}
