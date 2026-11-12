package forms

import (
	"slices"
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

func TestPresentationHiddenChoiceRemainsValidAndIsNotRendered(t *testing.T) {
	defs := []catalog.Input{{Name: "auth_method", Label: "Authentication", Type: "choice", Required: true, Options: []catalog.Choice{
		{Value: "oauth", Label: "OAuth"}, {Value: "service_account", Label: "Service account"}, {Value: "personal", Label: "Personal token"},
	}}}
	m := NewFormWithPresentation(t.Context(), defs, map[string]any{"auth_method": "personal"}, nil, nil, map[string][]string{"auth_method": {"personal"}})
	m.SetSections(FormSection{Title: "Authentication", Fields: []string{"auth_method"}})
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Personal token") || !strings.Contains(view, "OAuth") || !strings.Contains(view, "Service account") || !strings.Contains(view, "Hidden selections are preserved.") {
		t.Fatalf("presentation choices were not filtered: %s", view)
	}
	if got := m.Values()["auth_method"]; got != "personal" {
		t.Fatalf("filtered saved value was changed: %#v", got)
	}
	if err := m.CommitPendingInput(); err != nil {
		t.Fatalf("commit hidden saved choice: %v", err)
	}
	if got := m.Values()["auth_method"]; got != "personal" {
		t.Fatalf("no-op submit changed hidden saved choice: %#v", got)
	}
	values, err := m.editor.Commit()
	if err != nil || values["auth_method"] != "personal" {
		t.Fatalf("original definition did not validate/retain hidden choice: values=%#v err=%v", values, err)
	}
}

func TestPresentationCanHideEveryChoiceWithoutChangingSavedValue(t *testing.T) {
	m := NewFormWithPresentation(t.Context(), []catalog.Input{{Name: "mode", Type: "choice", Options: []catalog.Choice{{Value: "one"}, {Value: "two"}}}}, map[string]any{"mode": "one"}, nil, nil, map[string][]string{"mode": {"one", "two"}})
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "one") {
		t.Fatalf("hidden scalar choice value was rendered:\n%s", view)
	}
	m.choose(m.defs[0], m.Values()["mode"])
	if got := m.Values()["mode"]; got != "one" {
		t.Fatalf("form changed a value when every choice was hidden: %#v", got)
	}
}

func TestPresentationHiddenChoiceSurvivesVisibleMultichoiceToggle(t *testing.T) {
	defs := []catalog.Input{{Name: "features", Label: "Features", Type: "multichoice", Multiple: true, Options: []catalog.Choice{
		{Value: "visible-a", Label: "Visible A"}, {Value: "hidden", Label: "Hidden"}, {Value: "visible-b", Label: "Visible B"},
	}}}
	m := NewFormWithPresentation(t.Context(), defs, map[string]any{"features": []string{"hidden", "visible-a"}}, nil, nil, map[string][]string{"features": {"hidden"}})
	for _, size := range [][2]int{{80, 24}, {50, 24}} {
		m.width, m.height = size[0], size[1]
		view := ansi.Strip(m.View().Content)
		if strings.Contains(view, "hidden") {
			t.Fatalf("hidden selection value was rendered at size %dx%d:\n%s", size[0], size[1], view)
		}
	}
	m.choose(m.defs[0], m.Values()["features"])
	got, _ := m.Values()["features"].([]string)
	if !slices.Contains(got, "hidden") || slices.Contains(got, "visible-a") {
		t.Fatalf("visible toggle did not preserve hidden selection: %#v", got)
	}
	if _, err := m.editor.Commit(); err != nil {
		t.Fatalf("hidden selected choice was rejected by original definition: %v", err)
	}
}

func TestPresentationHiddenInputRetainsValueWithoutRenderingField(t *testing.T) {
	defs := []catalog.Input{{Name: "private_note", Label: "Private note", Type: "string", Required: true}}
	m := NewFormWithPresentation(t.Context(), defs, map[string]any{"private_note": "preserve me"}, nil, []string{"private_note"}, nil)
	if len(m.defs) != 0 || strings.Contains(ansi.Strip(m.View().Content), "Private note") {
		t.Fatalf("hidden input was rendered: %s", ansi.Strip(m.View().Content))
	}
	values, err := m.editor.Commit()
	if err != nil || values["private_note"] != "preserve me" {
		t.Fatalf("hidden input was lost or invalid: %#v %v", values, err)
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
