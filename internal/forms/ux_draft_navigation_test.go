package forms

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
)

func TestUXPaneArrowIsolationAndReachableButtons(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "a", Type: "string"}, {Name: "b", Type: "string"}}, nil)
	m.SetSections(FormSection{Title: "First", Fields: []string{"a"}}, FormSection{Title: "Second", Fields: []string{"b"}})
	if m.SectionTitle() != "First" {
		t.Fatalf("initial section title = %q", m.SectionTitle())
	}
	m.SelectSection("Second")
	if m.SectionTitle() != "Second" {
		t.Fatalf("selected section title = %q", m.SectionTitle())
	}
	m.FocusSection()
	if m.area != 0 {
		t.Fatalf("FocusSection left focus at area %d", m.area)
	}
	m.SelectSection("First")
	draft := m.Values()
	draft["a"] = "mutated copy"
	if m.Values()["a"] != nil {
		t.Fatalf("Values returned a mutable alias: %#v", m.Values())
	}
	m.Update(key(tea.KeyDown, ""))
	if m.sectionIndex != 1 || m.area != 0 {
		t.Fatalf("Down in sections changed details: section=%d area=%d", m.sectionIndex, m.area)
	}
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))
	if m.sectionIndex != 1 || m.selected != 1 {
		t.Fatalf("Down in details changed section: section=%d selected=%d", m.sectionIndex, m.selected)
	}
	m.Update(key(tea.KeyTab, ""))
	if m.area != 2 {
		t.Fatalf("Tab did not focus visible button bar: area=%d", m.area)
	}
	if m.View().Content == "" {
		t.Fatal("focused actions have no visible rendering")
	}
}

func TestUXBackRetainsDraftUntilExplicitCancel(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Type: "string"}}, map[string]any{"name": "old"})
	m.SetSections(FormSection{Title: "Details", Fields: []string{"name"}})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(key('n', "n"))
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEscape, ""))
	if got := m.editor.Values()["name"]; got != "n" || m.done {
		t.Fatalf("Back discarded draft or exited form: value=%#v done=%v", got, m.done)
	}
	_, backCmd := m.Update(key(tea.KeyEscape, ""))
	if backCmd == nil {
		t.Fatal("Escape at the section pane did not return a parent-navigation message")
	}
	back, ok := backCmd().(BackMsg)
	if !ok || back.Draft["name"] != "n" {
		t.Fatalf("BackMsg did not preserve the current draft: %#v", backCmd())
	}
	if _, err := m.Result(); err != ErrNotSubmitted {
		t.Fatalf("Back submitted or cancelled the form: %v", err)
	}
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyTab, ""))
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyEnter, ""))
	if _, err := m.Result(); err != picker.ErrCancelled {
		t.Fatalf("explicit Cancel did not report cancellation: %v", err)
	}
}
