package tui

import (
	tea "charm.land/bubbletea/v2"
	"errors"
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
	m.workspace = &workspaceState{Key: preview.Key, InstanceRef: m.packInstanceRequest(preview.Key).Ref, Active: true}
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
	m.Update(forms.ActionMsg{SectionID: "tool:components", ID: "select-all-components"})
	ids, _ = m.form.Values()[m.pendingSetupItemsField].([]string)
	if !slices.Contains(ids, "skill:one") || !slices.Contains(ids, "plugin:native") {
		t.Fatalf("Select all components omitted a component: %v", ids)
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

func TestExplicitSelectAllDoesNotBecomeEvolvingDefaultIntent(t *testing.T) {
	m := componentForm(t)
	preview := *m.pendingSetup
	preview.DefaultSelection = true
	preview.SelectedItemIDs = []string{"skill:one", "skill:two", "set:api", "plugin:native"}
	m.openSetupForm(preview)
	m.selectAllComponents()
	cmd := m.applySetup(m.form.Values())
	if cmd == nil {
		t.Fatalf("apply unavailable: %s", m.output)
	}
	runTeaCmd(t, m, cmd)
	request := m.backend.(*packProfileBackend).installRequest
	if request.DefaultSelection {
		t.Fatal("an explicit Select all action was saved as evolving default intent")
	}
	if !slices.Equal(request.ItemIDs, preview.SelectedItemIDs) {
		t.Fatalf("explicit selected items = %v, want %v", request.ItemIDs, preview.SelectedItemIDs)
	}
}

func TestComponentSelectionTouchSurvivesFailedApplyRetry(t *testing.T) {
	m := componentForm(t)
	backend := m.backend.(*packProfileBackend)
	backend.installErr = errors.New("fixture registration failure")
	preview := *m.pendingSetup
	preview.DefaultSelection = true
	preview.SelectedItemIDs = []string{"skill:one", "skill:two", "set:api"}
	m.openSetupForm(preview)
	m.form.SelectSectionID(sectionComponentsID)
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	selected, _ := m.form.Values()[m.pendingSetupItemsField].([]string)
	if slices.Equal(selected, preview.SelectedItemIDs) {
		t.Fatal("test did not change the explicit component selection")
	}
	cmd := m.applySetup(m.form.Values())
	if cmd == nil {
		t.Fatalf("apply unavailable: %s", m.output)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if !m.canEditResultAnswers() || m.setupRetry == nil {
		t.Fatalf("failed apply did not open retry flow: result=%+v", m.result)
	}
	m.returnToConfiguration()
	if !m.pendingSetupItemsTouched {
		t.Fatal("retry lost the explicit component interaction")
	}
	backend.installErr = nil
	retry := m.applySetup(m.form.Values())
	if retry == nil {
		t.Fatalf("retry unavailable: %s", m.output)
	}
	runTeaCmd(t, m, retry)
	if backend.installRequest.DefaultSelection {
		t.Fatal("retry replaced the explicit selection with current defaults")
	}
	if !slices.Equal(backend.installRequest.ItemIDs, selected) {
		t.Fatalf("retry selected items = %v, want preserved %v", backend.installRequest.ItemIDs, selected)
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

func TestPluginChoiceRequiresEverySelectedDestinationSupport(t *testing.T) {
	p := viewmodel.SetupPreview{Items: []catalog.InstallationItem{{ID: "plugin:native", Plugins: []string{"native"}}}, PluginDefinitions: []catalog.Plugin{{Name: "native", Format: "claude-code"}}, Destinations: []viewmodel.SetupDestination{{ID: "a", Features: agents.FeatureSet{PluginFormats: []string{"claude-code"}}}, {ID: "b", Features: agents.FeatureSet{Skills: true}}}}
	if choices := componentOptions(p, []string{"a", "b"}); len(choices) != 1 || choices[0].DisabledReason == "" {
		t.Fatalf("incompatible mixed destination offered: %v", choices)
	}
}
func TestSingleSelectableBundleWithSkillAndMCPIsAutoSelectedAndHidden(t *testing.T) {
	m := componentForm(t)
	preview := *m.pendingSetup
	preview.Items = []catalog.InstallationItem{{ID: "bundle:server", Label: "Server", Skills: []string{"server-skill"}, MCPs: []string{"server-mcp"}}}
	preview.SelectedItemIDs = nil
	preview.DefaultSelection = true
	m.openSetupForm(preview)
	if m.form.HasSectionID(sectionComponentsID) {
		t.Fatal("one selectable bundle rendered a redundant Components section")
	}
	if len(m.pendingSetup.SelectedItemIDs) != 1 || m.pendingSetup.SelectedItemIDs[0] != "bundle:server" {
		t.Fatalf("single selectable bundle was not selected automatically: %v", m.pendingSetup.SelectedItemIDs)
	}
}

func TestSingleComponentIsAutoSelectedAndHidden(t *testing.T) {
	m := componentForm(t)
	preview := *m.pendingSetup
	preview.Items = preview.Items[:1]
	preview.SelectedItemIDs = nil
	preview.DefaultSelection = true
	m.openSetupForm(preview)
	if m.form.HasSectionID(sectionComponentsID) {
		t.Fatal("single component still has a redundant Components section")
	}
	if len(m.pendingSetup.SelectedItemIDs) != 1 || m.pendingSetup.SelectedItemIDs[0] != "skill:one" {
		t.Fatalf("single component was not selected automatically: %v", m.pendingSetup.SelectedItemIDs)
	}
}

func TestSingleExplicitlyEmptyComponentSelectionCanBeAppliedAsEmpty(t *testing.T) {
	m := componentForm(t)
	preview := *m.pendingSetup
	preview.Items = preview.Items[:1]
	preview.SelectedItemIDs = []string{}
	preview.DefaultSelection = false
	m.openSetupForm(preview)
	if m.pendingSetupItemsField == "" {
		t.Fatal("explicit empty selection hid the only component choice")
	}
	ids, _ := m.form.Values()[m.pendingSetupItemsField].([]string)
	if len(ids) != 0 {
		t.Fatalf("explicit empty selection was auto-filled: %v", ids)
	}
	cmd := m.applySetup(m.form.Values())
	if cmd == nil {
		t.Fatalf("apply unavailable: %s", m.output)
	}
	runTeaCmd(t, m, cmd)
	request := m.backend.(*packProfileBackend).installRequest
	if request.ItemIDs == nil || len(request.ItemIDs) != 0 || request.DefaultSelection {
		t.Fatalf("single explicit empty selection changed on apply: %+v", request)
	}
}
