package forms

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
)

// This black-box regression uses only FormModel's pre-Task-4 API so it can be
// copied unchanged into a baseline source fixture and prove the behavior gap.
func TestUXUnavailableNativeEmbedsBrowserBehavior(t *testing.T) {
	dir := t.TempDir()
	m := NewForm(context.Background(), []catalog.Input{{Name: "root", Type: "directory"}}, map[string]any{"root": dir})
	m.SetSections(FormSection{Title: "Paths", Fields: []string{"root"}}, FormSection{Title: "Other", Fields: nil})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	field, section := m.selected, m.sectionIndex
	m.Update(pickedMsg{name: "root", action: "apply", err: picker.ErrUnavailable})

	view := m.View().Content
	if !strings.Contains(view, "Browse directory") || !strings.Contains(view, "Current:") {
		t.Fatalf("unavailable native picker did not show the embedded browser:\n%s", view)
	}
	if m.selected != field || m.sectionIndex != section {
		t.Fatalf("opening picker changed parent selection: field %d→%d section %d→%d", field, m.selected, section, m.sectionIndex)
	}
	if got := m.Values()["root"]; got != dir {
		t.Fatalf("opening picker changed the form draft: got %#v want %q", got, dir)
	}
}
