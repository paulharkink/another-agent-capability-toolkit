package forms

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestSplitFormUsesMockDialogBlueAndGoldBorder(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "token", Label: "Token", Type: "string"}}, nil)
	m.SetSections(FormSection{Title: "Authentication", Fields: []string{"token"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	view := m.View().Content
	if !strings.Contains(view, "48;2;12;49;133m") {
		t.Fatal("split form lacks mock dialog blue #0c3185")
	}
	if !strings.Contains(view, "\x1b[38;2;255;223;134m╔") || !strings.Contains(view, "\x1b[38;2;255;223;134m╚") {
		t.Fatal("split form lacks the mock's gold double-line border")
	}
}
