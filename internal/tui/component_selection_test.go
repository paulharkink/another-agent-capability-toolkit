package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"slices"
	"testing"
)

func componentForm(t *testing.T) *Model {
	m, _ := profileHomeFixture(t)
	preview := viewmodel.SetupPreview{Key: m.profiles()[0].Key, PackageName: "Guidance", PackRoot: "/pack", Items: []catalog.InstallationItem{{ID: "skill:one", Label: "One", Skills: []string{"one"}}, {ID: "skill:two", Label: "Two", Skills: []string{"two"}}, {ID: "set:api", Label: "API", Skills: []string{"api"}, MCPs: []string{"server"}}, {ID: "plugin:native", Label: "Native", Plugins: []string{"native"}}}, SelectedItemIDs: []string{"skill:one", "skill:two", "set:api"}, Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true, Features: agents.FeatureSet{Skills: true, MCPs: true}}, {ID: "absent", DisabledReason: "Not installed"}}, Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "enabled", Type: "boolean"}, Value: false, HasValue: true, Editable: true}}}
	m.workspace = &workspaceState{Key: preview.Key, Reference: m.packProfileRequest(preview.Key).Ref, Active: true}
	m.openSetupForm(preview)
	return m
}
func TestComponentSelectionKeyboardAndSelectAll(t *testing.T) {
	m := componentForm(t)
	if !m.form.HasSectionID("tool:components") {
		t.Fatal("component section missing")
	}
	m.form.SelectSectionID("tool:components")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	ids, _ := m.form.Values()[m.pendingSetupItemsField].([]string)
	if slices.Contains(ids, "skill:one") || !slices.Contains(ids, "skill:two") {
		t.Fatalf("toggle changed unrelated items: %v", ids)
	}
	m.Update(forms.ActionMsg{SectionID: "tool:components", ID: "select-all-skills"})
	ids, _ = m.form.Values()[m.pendingSetupItemsField].([]string)
	if !slices.Contains(ids, "skill:one") || slices.Contains(ids, "plugin:native") {
		t.Fatalf("Select all skills changed plugin choice: %v", ids)
	}
}
func TestAdapterFeatureSelectionUnavailableChoice(t *testing.T) {
	m := componentForm(t)
	defs := m.form.Definitions()
	for _, def := range defs {
		if def.Name == m.pendingSetupField {
			for _, choice := range def.Options {
				if choice.Value == "absent" && choice.DisabledReason == "" {
					t.Fatal("absent agent offered")
				}
			}
		}
	}
	m.form.SelectSectionID("tool:components")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	for i := 0; i < 3; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	ids, _ := m.form.Values()[m.pendingSetupItemsField].([]string)
	if slices.Contains(ids, "plugin:native") {
		t.Fatal("unsupported plugin selected")
	}
}
func TestComponentSelectionApplyPreservesEmptyIntent(t *testing.T) {
	m := componentForm(t)
	m.form.ApplyValues(map[string]any{m.pendingSetupItemsField: []string{}})
	cmd := m.applySetup(m.form.Values())
	if cmd == nil {
		t.Fatalf("apply unavailable: %s", m.output)
	}
	runTeaCmd(t, m, cmd)
	b := m.backend.(*packProfileBackend)
	if b.installRequest.ItemIDs == nil || len(b.installRequest.ItemIDs) != 0 || b.installRequest.Ref.Name != "ota" {
		t.Fatalf("empty intent or profile was lost: %+v", b.installRequest)
	}
}
func TestComponentSelectionRequiresEverySkillForCheckedDestination(t *testing.T) {
	m := componentForm(t)
	draft := &setupRetryDraft{preview: *m.pendingSetup, values: m.form.Values(), destinationField: m.pendingSetupField, itemField: m.pendingSetupItemsField}
	out := &viewmodel.OperationResult{}
	out.Changes = append(out.Changes, state.Installation{Key: m.pendingSetup.Key, AgentID: "codex", Component: "skill", Destination: "/skills/one"})
	if ids := m.achievedDestinationIDs(draft, out); len(ids) != 0 {
		t.Fatalf("partial capability checked: %v", ids)
	}
}
