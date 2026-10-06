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

func splitIsolationFixture() *FormModel {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "host", Label: "Listen address", Type: "string"},
		{Name: "port", Label: "Listen port", Type: "integer"},
		{Name: "token", Label: "Token", Type: "secret"},
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file"},
	}, nil)
	m.SetSections(
		FormSection{Title: "Connection", Fields: []string{"host", "port"}},
		FormSection{Title: "Authentication", Fields: []string{"token", "kubeconfig"}},
	)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return m
}

func TestSplitL3ArrowsOnlyChangeL4Content(t *testing.T) {
	m := splitIsolationFixture()
	m.Update(key(tea.KeyDown, ""))
	view := ansi.Strip(m.View().Content)
	if m.area != 0 || m.sectionIndex != 1 || !strings.Contains(view, "Token:") || strings.Contains(view, "Listen port:") {
		t.Fatalf("L3 Down did not only select Authentication content: area=%d section=%d\n%s", m.area, m.sectionIndex, view)
	}
	m.Update(key(tea.KeyUp, ""))
	view = ansi.Strip(m.View().Content)
	if m.area != 0 || m.sectionIndex != 0 || !strings.Contains(view, "Listen port:") || strings.Contains(view, "Token:") {
		t.Fatalf("L3 Up did not only select Connection content: area=%d section=%d\n%s", m.area, m.sectionIndex, view)
	}
}

func TestSplitL4ArrowsStayInTheSelectedSection(t *testing.T) {
	m := splitIsolationFixture()
	m.Update(key(tea.KeyDown, ""))
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	for i := 0; i < 4; i++ {
		m.Update(key(tea.KeyUp, ""))
	}
	if m.area != 1 || m.sectionIndex != 1 || m.selected != 2 {
		t.Fatalf("L4 Up crossed into L3: area=%d section=%d field=%d", m.area, m.sectionIndex, m.selected)
	}
	for i := 0; i < 4; i++ {
		m.Update(key(tea.KeyDown, ""))
	}
	if m.area != 1 || m.sectionIndex != 1 || m.selected != 3 {
		t.Fatalf("L4 Down crossed into another area: area=%d section=%d field=%d", m.area, m.sectionIndex, m.selected)
	}
}

func TestSplitMouseWheelOnlyMovesThePaneUnderPointer(t *testing.T) {
	m := splitIsolationFixture()
	layout := m.layout()
	m.Update(tea.MouseWheelMsg{X: 4, Y: layout.bodyStart + 2, Button: tea.MouseWheelDown})
	if m.area != 0 || m.sectionIndex != 1 {
		t.Fatalf("wheel over L3 did not select the next section: area=%d section=%d", m.area, m.sectionIndex)
	}
	m.Update(tea.MouseWheelMsg{X: layout.splitLeftWidth + 8, Y: layout.bodyStart + 2, Button: tea.MouseWheelDown})
	if m.area != 1 || m.sectionIndex != 1 || m.selected != 3 {
		t.Fatalf("wheel over L4 changed L3 or missed its field: area=%d section=%d field=%d", m.area, m.sectionIndex, m.selected)
	}
	m.Update(tea.MouseWheelMsg{X: layout.splitLeftWidth + 8, Y: layout.bodyStart + 2, Button: tea.MouseWheelDown})
	if m.area != 1 || m.sectionIndex != 1 || m.selected != 3 {
		t.Fatalf("wheel at L4 end escaped its pane: area=%d section=%d field=%d", m.area, m.sectionIndex, m.selected)
	}
}

func TestSplitL4ScrollDoesNotScrollL3(t *testing.T) {
	defs := make([]catalog.Input, 22)
	fields := make([]string, len(defs))
	for i := range defs {
		name := fmt.Sprintf("field_%02d", i)
		defs[i] = catalog.Input{Name: name, Label: fmt.Sprintf("Field %02d", i), Type: "string"}
		fields[i] = name
	}
	m := NewForm(context.Background(), defs, nil)
	m.SetSections(FormSection{Title: "Long section", Fields: fields}, FormSection{Title: "Other section"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(key(tea.KeyRight, ""))
	for i := 0; i < 21; i++ {
		m.Update(key(tea.KeyDown, ""))
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "> Long section") || !strings.Contains(view, "Other section") || !strings.Contains(view, "Field 21") {
		t.Fatalf("L4 scrolling hid L3 rows:\n%s", view)
	}
}

func TestSplitFormDistinguishesSectionsFieldsAndHeadings(t *testing.T) {
	m := splitIsolationFixture()
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"L3 Sections · FOCUSED", "> Connection", "› Authentication", "› Listen address:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("split form lacks row cue %q:\n%s", want, view)
		}
	}
	m.Update(key(tea.KeyRight, ""))
	if view = ansi.Strip(m.View().Content); !strings.Contains(view, "L4 · Connection · FOCUSED") {
		t.Fatalf("split form lacks a focused L4 title:\n%s", view)
	}
}
