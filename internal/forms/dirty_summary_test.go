package forms

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestChangedFieldSummaryUsesLabelsAndNeverValues(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "token", Label: "Access token", Type: "secret"},
		{Name: "endpoint", Label: "Endpoint", Type: "string"},
		{Name: "namespace", Label: "Namespace", Type: "string"},
		{Name: "context", Label: "Context", Type: "string"},
	}, map[string]any{"token": "credential-sentinel"})
	if got, remaining := m.ChangedFieldSummary(3); len(got) != 0 || remaining != 0 {
		t.Fatalf("initial summary = %v, %d; want empty", got, remaining)
	}
	for name, value := range map[string]any{
		"token": "new-secret-sentinel", "endpoint": "new-endpoint-sentinel",
		"namespace": "new-namespace-sentinel", "context": "new-context-sentinel",
	} {
		if err := m.editor.Apply(name, value); err != nil {
			t.Fatal(err)
		}
	}
	labels, remaining := m.ChangedFieldSummary(3)
	if len(labels) != 3 || remaining != 1 {
		t.Fatalf("summary = %v, %d; want three labels and one remaining field", labels, remaining)
	}
	for _, label := range labels {
		if strings.Contains(label, "sentinel") {
			t.Fatalf("summary exposed a value: %q", label)
		}
	}
	for _, forbidden := range []string{"credential-sentinel", "new-secret-sentinel", "new-endpoint-sentinel", "new-namespace-sentinel", "new-context-sentinel"} {
		if strings.Contains(strings.Join(labels, " "), forbidden) {
			t.Fatalf("summary exposed %q", forbidden)
		}
	}
}

func TestChangedFieldSummaryRevertsToCleanAndCountsActiveBuffer(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "port", Label: "Listen port", Type: "integer"}}, map[string]any{"port": 18766})
	if m.HasUnsavedChanges() {
		t.Fatal("typed default prefill made a new form dirty")
	}
	if err := m.editor.Apply("port", int64(18767)); err != nil {
		t.Fatal(err)
	}
	if labels, remaining := m.ChangedFieldSummary(3); len(labels) != 1 || labels[0] != "Listen port" || remaining != 0 {
		t.Fatalf("changed summary = %v, %d", labels, remaining)
	}
	if err := m.editor.Apply("port", int64(18766)); err != nil {
		t.Fatal(err)
	}
	if m.HasUnsavedChanges() {
		t.Fatal("reverting to the initial typed value remained dirty")
	}
	m.beginEdit("apply", "18766")
	m.buffer = "18767"
	if !m.HasUnsavedChanges() {
		t.Fatal("active changed edit buffer was not dirty")
	}
	if labels, remaining := m.ChangedFieldSummary(3); len(labels) != 1 || labels[0] != "Listen port" || remaining != 0 {
		t.Fatalf("active-buffer summary = %v, %d", labels, remaining)
	}
	m.buffer = "18766"
	if m.HasUnsavedChanges() {
		t.Fatal("reverting the active edit buffer remained dirty")
	}
}

func TestTypedNilCollectionPrefillIsSemanticallyClean(t *testing.T) {
	var saved []string
	m := NewForm(context.Background(), []catalog.Input{{Name: "connections", Type: "multichoice"}}, map[string]any{"connections": saved})
	if m.HasUnsavedChanges() {
		t.Fatalf("typed nil slice prefill became dirty: current=%#v original=%#v", m.editor.values, m.editor.original)
	}
	if labels, remaining := m.ChangedFieldSummary(3); len(labels) != 0 || remaining != 0 {
		t.Fatalf("typed nil slice summary = %v, %d", labels, remaining)
	}
}

func TestCopyAnswersPreservesTypedNilSliceSemantics(t *testing.T) {
	var saved []string
	copy := copyAnswers(map[string]any{"connections": saved})["connections"]
	got, ok := copy.([]string)
	if !ok || got != nil {
		t.Fatalf("copy of typed nil list = %#v (%T), want typed nil []string", copy, copy)
	}
}

func TestCleanBaselinePreservesTypedNilSavedList(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "connections", Type: "string", Multiple: true}}, nil)
	m.editor.values["connections"] = []string(nil)
	m.MarkClean()
	if m.HasUnsavedChanges() {
		t.Fatalf("marking a typed nil saved list clean made it dirty: current=%#v original=%#v", m.editor.values, m.editor.original)
	}
	if labels, remaining := m.ChangedFieldSummary(3); len(labels) != 0 || remaining != 0 {
		t.Fatalf("typed nil saved-list summary = %v, %d", labels, remaining)
	}
}

func TestEmptySavedListRepresentationsAreSemanticallyEqual(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "connections", Type: "string", Multiple: true}}, nil)
	m.editor.values["connections"] = []string(nil)
	m.MarkClean()
	m.editor.values["connections"] = []string{}
	if m.HasUnsavedChanges() {
		t.Fatalf("nil and empty saved lists were treated as different: current=%#v original=%#v", m.editor.values, m.editor.original)
	}
	if labels, remaining := m.ChangedFieldSummary(3); len(labels) != 0 || remaining != 0 {
		t.Fatalf("empty-list summary = %v, %d", labels, remaining)
	}
}

func TestWorkspaceCanEstablishCleanBaselineAfterSeedingAndSetSubmitLabel(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{
		{Name: "endpoint", Label: "Endpoint", Type: "string"},
		{Name: "targets", Label: "Targets", Type: "string", Multiple: true},
	}, nil)
	if err := m.editor.Apply("endpoint", "https://example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := m.editor.Apply("targets", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if !m.HasUnsavedChanges() {
		t.Fatal("workspace-seeded values did not differ from the initial empty draft")
	}
	m.MarkClean()
	if m.HasUnsavedChanges() {
		t.Fatal("clean baseline did not include seeded scalar and list values")
	}
	m.SetSubmitLabel("Save and apply")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if !strings.Contains(m.View().Content, "Save and apply") {
		t.Fatalf("custom submit label missing from form:\n%s", m.View().Content)
	}
	if strings.Contains(m.View().Content, "[ Save ]") {
		t.Fatalf("default Save action remained visible after label update:\n%s", m.View().Content)
	}
}

func TestApplyValuesPreservesCleanBaselineAndMarksOverrideDirty(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{Name: "name", Label: "Name", Type: "string"}}, map[string]any{"name": "preview"})
	if err := m.ApplyValues(map[string]any{"name": "saved override"}); err != nil {
		t.Fatal(err)
	}
	if !m.HasUnsavedChanges() {
		t.Fatal("applying saved override silently reset the original preview baseline")
	}
	if got := m.Values()["name"]; got != "saved override" {
		t.Fatalf("override value = %#v", got)
	}
	if err := m.ApplyValues(map[string]any{"missing": "value"}); err == nil {
		t.Fatal("undeclared override was accepted")
	}
}

func TestDisabledDestinationChoiceShowsReasonAndOnlyAllowsDeselection(t *testing.T) {
	m := NewForm(context.Background(), []catalog.Input{{
		Name: "destinations", Label: "Destinations", Type: "multichoice",
		Options: []catalog.Choice{
			{Value: "one", Label: "One", DisabledReason: "agent is not detected"},
			{Value: "two", Label: "Two"},
		},
	}}, map[string]any{"destinations": []string{"one"}})
	m.SetSections(FormSection{Title: "Targets", Fields: []string{"destinations"}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if !strings.Contains(m.View().Content, "agent is not detected") {
		t.Fatal("disabled destination reason was not shown")
	}
	// The unavailable selected destination can still be unchecked to remove it.
	m.choose(m.defs[0], m.Values()["destinations"])
	if got := m.Values()["destinations"].([]string); len(got) != 0 {
		t.Fatalf("selected disabled destination could not be removed: %v", got)
	}
	// An unavailable destination that is not selected cannot be added.
	m.choose(m.defs[0], m.Values()["destinations"])
	if got := m.Values()["destinations"].([]string); len(got) != 0 {
		t.Fatalf("unavailable destination was added: %v", got)
	}
}
