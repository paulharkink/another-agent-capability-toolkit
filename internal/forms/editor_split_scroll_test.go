package forms

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestLongSplitSectionKeepsActionsAndShowsScrollCues(t *testing.T) {
	defs := make([]catalog.Input, 20)
	fields := make([]string, len(defs))
	for i := range defs {
		name := fmt.Sprintf("field_%02d", i)
		defs[i] = catalog.Input{Name: name, Label: fmt.Sprintf("Field %02d", i), Type: "string"}
		fields[i] = name
	}
	m := NewForm(context.Background(), defs, nil)
	m.SetSections(FormSection{Title: "Connection", Fields: fields})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	first := ansi.Strip(m.View().Content)
	if got := len(strings.Split(first, "\n")); got != 24 {
		t.Fatalf("split form should fit 24 terminal rows, got %d", got)
	}
	if !strings.Contains(first, "[ Save ]") || !strings.Contains(first, "[ Cancel ]") || !strings.Contains(first, "↓ more") {
		t.Fatalf("long split form hid actions or scroll cue:\n%s", first)
	}
	if !strings.Contains(first, "█") || !strings.Contains(first, "│") {
		t.Fatalf("long split pane omitted its vertical scrollbar track/thumb:\n%s", first)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	for i := 0; i < 19; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	last := ansi.Strip(m.View().Content)
	if got := len(strings.Split(last, "\n")); got != 24 {
		t.Fatalf("scrolled split form should fit 24 terminal rows, got %d", got)
	}
	if !strings.Contains(last, "Field 19") || !strings.Contains(last, "↑ more") || !strings.Contains(last, "[ Save ]") {
		t.Fatalf("selected late field or actions are hidden after scrolling:\n%s", last)
	}
	clickVisibleText(t, m, "Field 18:")
	if m.selected != 18 || !m.editing {
		t.Fatalf("mouse click after split scroll selected field %d, editing=%t", m.selected, m.editing)
	}
}

func TestLongChoicePathSurvivesRightPaneScrollbarWrappingAndPaging(t *testing.T) {
	path := "/home/agent/configs/clusters/production/shared-platform/very-long-directory/cluster-configuration-file-name.yaml"
	m := NewForm(context.Background(), []catalog.Input{{
		Name: "destinations", Label: "Agent destinations", Type: "multichoice",
		Options: []catalog.Choice{{Value: path, Label: path}},
	}}, nil)
	m.SetSections(FormSection{ID: "agents", Title: "Agents", Fields: []string{"destinations"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 23})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	var rendered strings.Builder
	for page := 0; page < 12; page++ {
		for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			_, detail, ok := strings.Cut(line, "│")
			if !ok {
				continue
			}
			if cell, _, found := strings.Cut(detail, "║"); found {
				detail = cell
			}
			rendered.WriteString(strings.ReplaceAll(detail, "│", ""))
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, rendered.String())
	for start := 0; start < len(path); start += 16 {
		end := min(len(path), start+32)
		if chunk := path[start:end]; !strings.Contains(compact, chunk) {
			t.Fatalf("keyboard-scrolled form lost path chunk %q:\n%s", chunk, rendered.String())
		}
	}
}
