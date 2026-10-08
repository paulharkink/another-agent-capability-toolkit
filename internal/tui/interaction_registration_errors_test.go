package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func openAgentMetadataWorkspace(t *testing.T, setup *setupBackendFixture, preview viewmodel.SetupPreview) (*Model, *capabilityProfileBackend) {
	t.Helper()
	m, profile := typedProfileFixture()
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	backend := &capabilityProfileBackend{Backend: profile, profile: profile, setup: setup, preview: preview}
	m.backend = backend
	request := viewmodel.SetupRequest{SourceID: "team-source", PackageID: "plain", Environment: "dev", Target: "foreign"}
	cmd := m.openTargetWorkspace(request, "Agents")
	if cmd == nil {
		t.Fatal("could not open manifest-backed complete-binding workspace")
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || !m.workspace.Active {
		t.Fatalf("complete-binding workspace did not load:\n%s", m.View().Content)
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	return m, backend
}

func TestAchievedDestinationsRequireSkillAndExactParentKey(t *testing.T) {
	m, _ := typedProfileFixture()
	m.catalog = []catalog.Package{{ID: "plain", Skill: &catalog.Skill{Name: "plain"}, MCPs: []catalog.MCP{{Name: "plain"}}}}
	m.sourceLabels = map[string]string{}
	m.settings["source"] = "team-source"
	draft := &setupRetryDraft{
		preview: viewmodel.SetupPreview{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}, MCP: true, MCPDefinitions: []catalog.MCP{{Name: "plain"}}},
		values:  map[string]any{"__aact_destinations": []string{"codex"}}, destinationField: "__aact_destinations",
	}
	result := &viewmodel.OperationResult{Changes: []state.Installation{
		{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign", MCP: "plain"}, AgentID: "codex", Component: "mcp"},
	}}
	if got := m.achievedDestinationIDs(draft, result); len(got) != 0 {
		t.Fatalf("MCP-only effect falsely completed skill+MCP binding: %v", got)
	}
	result.Changes = append(result.Changes,
		state.Installation{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "other", MCP: ""}, AgentID: "codex", Component: "skill"},
	)
	if got := m.achievedDestinationIDs(draft, result); len(got) != 0 {
		t.Fatalf("unrelated target effect falsely completed binding: %v", got)
	}
}

func TestInteractionAgentDestinationShowsDeclaredDetectionFailure(t *testing.T) {
	preview := capabilityProfilePreview()
	preview.Destinations[0].Detection = "failed: permission denied"
	preview.Destinations[0].Note = "Could not inspect this destination configuration"
	m, _ := openAgentMetadataWorkspace(t, &setupBackendFixture{}, preview)
	view := m.View().Content
	for _, want := range []string{"failed: permission denied", "Could not inspect this destination configuration"} {
		if !strings.Contains(view, want) {
			t.Fatalf("declared destination diagnostic %q is hidden in Agents:\n%s", want, view)
		}
	}
}

func TestInteractionAgentDestinationShowsDeclaredConflictDetail(t *testing.T) {
	preview := capabilityProfilePreview()
	preview.Destinations[1].Note = `Name conflict: existing MCP entry "plain-dev" is not owned by AACT`
	m, _ := openAgentMetadataWorkspace(t, &setupBackendFixture{}, preview)
	view := m.View().Content
	for _, want := range []string{"Name conflict:", "plain-dev"} {
		if !strings.Contains(view, want) {
			t.Fatalf("manifest destination detail is missing %q:\n%s", want, view)
		}
	}
}

func TestInteractionPartialCompleteBindingReportsStructuredAchievementsAndErrors(t *testing.T) {
	setup := &setupBackendFixture{installResult: &viewmodel.OperationResult{
		Saved: true,
		Changes: []state.Installation{
			{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}, AgentID: "codex", Component: "skill"},
			{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign", MCP: "plain"}, AgentID: "codex", Component: "mcp"},
		},
		Errors: []string{`claude: refusing foreign MCP registration "plain-dev"`},
	}}
	preview := capabilityProfilePreview()
	preview.Destinations[0].Selected = true
	preview.Destinations[1].Selected = true
	m, backend := openAgentMetadataWorkspace(t, setup, preview)
	cmd := m.applySetup(m.form.Values())
	if cmd == nil {
		t.Fatalf("complete capability binding was not submitted: output=%q\n%s", m.output, m.View().Content)
	}
	message := runTeaCmd(t, m, cmd)
	_, _ = m.Update(message)
	if backend.setup.installRequest == nil {
		t.Fatal("complete-binding save did not call UIInstall")
	}
	if got := backend.setup.installRequest.DestinationIDs; !containsString(got, "codex") || !containsString(got, "claude") {
		t.Fatalf("save did not send the complete selected destination set: %v", got)
	}
	view := m.View().Content
	if !strings.Contains(view, "codex") || !strings.Contains(view, "claude: refusing foreign MCP registration") {
		t.Fatalf("structured success and per-destination failure were not both reported:\n%s", view)
	}
	if m.setupRetry == nil || !m.result.CanReturn {
		t.Fatalf("failed binding did not retain its editable retry draft: retry=%+v result=%+v", m.setupRetry, m.result)
	}
	requested, _ := m.setupRetry.values[m.setupRetry.destinationField].([]string)
	if !containsString(requested, "claude") {
		t.Fatalf("retry intent lost the requested failed destination: %v", requested)
	}
	m.returnToConfiguration()
	achieved, _ := m.form.Values()[m.pendingSetupField].([]string)
	if !containsString(achieved, "codex") || containsString(achieved, "claude") {
		t.Fatalf("edit answers did not show only complete achieved bindings: %v", achieved)
	}
}

func TestUnsavedExitApplyFailureReconcilesDestinationSelection(t *testing.T) {
	setup := &setupBackendFixture{installResult: &viewmodel.OperationResult{
		Changes: []state.Installation{
			{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}, AgentID: "codex", Component: "skill"},
			{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign", MCP: "plain"}, AgentID: "codex", Component: "mcp"},
		},
		Errors: []string{"claude endpoint rejected"},
	}}
	preview := capabilityProfilePreview()
	preview.Destinations[0].Selected = true
	preview.Destinations[1].Selected = false
	m, backend := openAgentMetadataWorkspace(t, setup, preview)
	m.form.SetBackNavigation(true)
	m.form.SetUnsavedExitGuard(true)
	if !m.form.ReconcileDraftField(m.pendingSetupField, []string{"codex", "claude"}) {
		t.Fatal("could not seed a dirty destination draft")
	}
	_, requestExit := m.form.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if requestExit != nil {
		m.Update(requestExit())
	}
	if m.unsavedExit == nil {
		t.Fatal("Back from dirty configuration did not show the unsaved exit popup")
	}
	cmd := m.chooseUnsavedExit(0)
	if cmd == nil {
		t.Fatal("Apply changes did not submit the configuration")
	}
	m.Update(runTeaCmd(t, m, cmd))
	got, _ := m.form.Values()[m.pendingSetupField].([]string)
	if !containsString(got, "codex") || containsString(got, "claude") {
		t.Fatalf("failed Apply left the failed destination checked: %v", got)
	}
	if backend.setup.installRequest == nil || m.result == nil || !m.unsavedExitFailure {
		t.Fatalf("failed Apply lost its request or error result: request=%+v result=%+v", backend.setup.installRequest, m.result)
	}
}
