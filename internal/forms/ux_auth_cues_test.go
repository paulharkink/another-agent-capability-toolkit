package forms

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestUXAuthCuesKeepImportAndChoiceVisibleInNarrowForm(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"},
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"},
	}, nil)
	m.SetSections(FormSection{Title: "Authentication", Fields: []string{"token", "kubeconfig"}})
	m.SetHint("token", "Enter a Token")
	m.SetHint("kubeconfig", "Enter a source kubeconfig path · Import source; AACT uses managed credential material at runtime")
	m.SetExclusiveFields("token", "kubeconfig")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	view := m.View().Content
	for _, want := range []string{"Token", "Source kubeconfig", "Enter a Token", "Enter a source kubeconfig path", "Import source", "not a live path", "managed credential material", "runtime"} {
		if !strings.Contains(view, want) {
			t.Errorf("narrow authentication form omitted %q:\n%s", want, view)
		}
	}
}

func TestUXAuthSwitchCueRetainsImportedNotLiveMeaning(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"},
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"},
	}, map[string]any{"token": "old-token"})
	m.SetSections(FormSection{Title: "Authentication", Fields: []string{"token", "kubeconfig"}})
	m.SetExclusiveFields("token", "kubeconfig")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	m.Update(key(tea.KeyDown, ""))

	view := m.View().Content
	for _, want := range []string{"Type a path to switch", "Import source", "not a live path", "old-token"} {
		if !strings.Contains(view, want) {
			t.Errorf("editing alternate credential omitted %q:\n%s", want, view)
		}
	}
}
