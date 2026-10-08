package forms

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestManifestVisibleWhenHidesAndRevealsFieldFromEqualityMap(t *testing.T) {
	m := NewForm(t.Context(), []catalog.Input{
		{Name: "mode", Label: "Mode", Type: "choice", Options: []catalog.Choice{{Value: "one", Label: "One"}, {Value: "two", Label: "Two"}}},
		{Name: "alpha", Label: "Alpha credential", Type: "secret", VisibleWhen: map[string]any{"mode": "one"}},
		{Name: "beta", Label: "Beta credential", Type: "secret", VisibleWhen: map[string]any{"mode": "two"}},
	}, map[string]any{"mode": "one"})
	m.SetSections(FormSection{Title: "Settings", Fields: []string{"mode", "alpha", "beta"}})
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "Alpha credential") || strings.Contains(got, "Beta credential") {
		t.Fatalf("visible_when not applied to initial values:\n%s", got)
	}
	if err := m.editor.Apply("mode", "two"); err != nil {
		t.Fatal(err)
	}
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "Beta credential") || strings.Contains(got, "Alpha credential") {
		t.Fatalf("visible_when did not react to changed controller:\n%s", got)
	}
}

func TestHiddenRequiredInputDoesNotBlockSaveAndRetainsItsValue(t *testing.T) {
	defs := []catalog.Input{
		{Name: "mode", Type: "choice", Options: []catalog.Choice{{Value: "one"}, {Value: "two"}}},
		{Name: "secret", Type: "secret", Required: true, VisibleWhen: map[string]any{"mode": "one"}},
	}
	e := NewEditor(defs, map[string]any{"mode": "one", "secret": "retained"})
	if err := e.Apply("mode", "two"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Commit()
	if err != nil {
		t.Fatalf("hidden required field blocked save: %v", err)
	}
	if got["secret"] != "retained" {
		t.Fatalf("hidden value was discarded: %#v", got)
	}
}

func TestFixedVisibilityControllerKeepsRequiredEditableBranchVisible(t *testing.T) {
	m := NewFormWithContext(t.Context(), []catalog.Input{{Name: "credential", Label: "Credential", Type: "secret", Required: true, VisibleWhen: map[string]any{"mode": "one"}}}, nil, map[string]any{"mode": "one"})
	m.SetSections(FormSection{Title: "Credentials", Fields: []string{"credential"}})
	if !strings.Contains(ansi.Strip(m.View().Content), "Credential") {
		t.Fatal("fixed controller did not expose its required editable field")
	}
	if _, err := m.editor.Commit(); err == nil {
		t.Fatal("empty required branch did not block save")
	}
	_, _ = m.save()
	if m.selected >= len(m.defs) || m.defs[m.selected].Name != "credential" || m.fieldErrors["credential"] == "" {
		t.Fatalf("visible required field error did not focus and mark credential: selected=%d errors=%v", m.selected, m.fieldErrors)
	}
}

func TestManifestSectionVisibilityReconcilesWithoutOverwritingBuiltinTitle(t *testing.T) {
	m := NewForm(t.Context(), []catalog.Input{
		{Name: "mode", Type: "choice", Options: []catalog.Choice{{Value: "off"}, {Value: "on"}}},
		{Name: "public", Label: "Public field", Type: "string", VisibleWhen: map[string]any{"mode": "on"}},
	}, map[string]any{"mode": "off"})
	m.SetSections(
		FormSection{ID: "tool:agents", Title: "Agents"},
		FormSection{ID: "package:agents", Title: "Agents", Fields: []string{"public"}},
	)
	if m.HasSectionID("package:agents") {
		t.Fatal("section with only hidden fields remained in navigation")
	}
	m.SetSectionContentID("tool:agents", []string{"Tool destinations"})
	if err := m.editor.Apply("mode", "on"); err != nil {
		t.Fatal(err)
	}
	m.refreshSections()
	if !m.HasSectionID("package:agents") {
		t.Fatal("manifest section did not return when its field became visible")
	}
	if !m.SelectSectionID("package:agents") {
		t.Fatal("could not select manifest section sharing the builtin title")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Public field") {
		t.Fatalf("public section was overwritten by builtin content:\n%s", view)
	}
	if m.SectionID() != "package:agents" {
		t.Fatalf("selected wrong same-title section: %q", m.SectionID())
	}
}

func TestVisibilityRefreshKeepsFocusedFieldInStableSection(t *testing.T) {
	defs := []catalog.Input{
		{Name: "mode", Type: "choice", Options: []catalog.Choice{{Value: "one"}, {Value: "two"}}},
		{Name: "first", Type: "string"},
		{Name: "second", Type: "string"},
	}
	form := NewForm(t.Context(), defs, map[string]any{"mode": "one", "first": "a", "second": "b"})
	form.SetSections(FormSection{ID: "package:auth", Title: "Authentication", Fields: []string{"mode", "first", "second"}})
	form.SelectSectionID("package:auth")
	form.selected = 2
	form.area = 1
	form.SetConditional("second", "mode", "one")
	form.refreshSections()
	if got := form.defs[form.selected].Name; got != "second" || form.area != 1 {
		t.Fatalf("refresh moved focus from the still-visible field: selected=%q area=%d", got, form.area)
	}
}

func TestSplitScalarChoiceCanChangeControllerWithUpDownAndEnter(t *testing.T) {
	m := NewForm(t.Context(), []catalog.Input{
		{Name: "mode", Label: "Mode", Type: "choice", Options: []catalog.Choice{{Value: "closed", Label: "Closed"}, {Value: "open", Label: "Open"}}},
		{Name: "credential", Label: "Credential", Type: "secret", VisibleWhen: map[string]any{"mode": "open"}},
	}, map[string]any{"mode": "closed"})
	m.SetSections(FormSection{ID: "package:settings", Title: "Settings", Fields: []string{"mode", "credential"}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Values()["mode"] != "open" || !m.HasSection("Settings") || m.disabledReason("credential") != "" {
		t.Fatalf("split scalar choice failed to reveal its branch: values=%v view=%s", m.Values(), ansi.Strip(m.View().Content))
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Credential") {
		t.Fatal("changing the controller did not reveal the dependent field")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "(*) Open") || !strings.Contains(view, "( ) Closed") || strings.Contains(view, "[x] Open") {
		t.Fatalf("single choice controller is not visually distinct from multichoice checkboxes:\n%s", view)
	}
}
