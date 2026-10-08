package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type setupBackendFixture struct {
	fixtureBackend
	previewRequest viewmodel.SetupRequest
	installRequest *viewmodel.SetupInstallRequest
	extraInputs    []viewmodel.SetupInput
	installResult  *viewmodel.OperationResult
	installErr     error
}

func setupProfileKey(request viewmodel.SetupRequest) state.Key {
	return state.Key{Source: request.Ref.PackID, Package: request.Ref.CapabilityID, Target: request.Ref.Name}
}

func TestSetupFailureBeforeLaterCancellationRemainsForeground(t *testing.T) {
	failure := errors.New("claude registration failed")
	b := &setupBackendFixture{
		installResult: &viewmodel.OperationResult{
			Saved: true, Message: "Registration update completed with errors", Step: "registration", Target: "plain / dev / prod",
			Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}}, Errors: []string{failure.Error()},
		},
		installErr: errors.Join(failure, picker.ErrCancelled),
	}
	m := NewContext(context.Background(), b)
	m.pendingSetup = &viewmodel.SetupPreview{Key: state.Key{Source: "team", Package: "plain", Environment: "dev", Target: "prod"}}
	m.pendingSetupField = "__aact_destinations"
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex", "claude"}})
	if cmd == nil {
		t.Fatal("mixed registration operation was not submitted")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if m.result == nil || !m.result.Failed {
		t.Fatalf("earlier failure was hidden by later cancellation: output=%q result=%+v", m.output, m.result)
	}
	joined := strings.Join(m.result.Rows, "\n")
	for _, want := range []string{"Inputs saved", "codex: mcp configured", "Step: registration", "Target: plain / dev / prod", failure.Error()} {
		if !strings.Contains(joined, want) {
			t.Errorf("foreground result omitted %q: %s", want, joined)
		}
	}
}

func TestSetupProgressEmitterDoesNotBlockOrPanicAfterFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan viewmodel.OperationProgress, 1)
	done := make(chan struct{})
	emitter := setupProgressEmitter{ctx: ctx, events: events, done: done}
	emitted := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			emitter.emit(viewmodel.OperationProgress{Output: "output"})
		}
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(time.Second):
		close(done)
		cancel()
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for {
				select {
				case <-events:
				case <-emitted:
					return
				}
			}
		}()
		select {
		case <-drained:
		case <-time.After(time.Second):
		}
		t.Fatal("progress emitter blocked without a receiver")
	}
	close(done)
	lateDone := make(chan struct{})
	go func() {
		emitter.emit(viewmodel.OperationProgress{Output: "late output"})
		close(lateDone)
	}()
	select {
	case <-lateDone:
	case <-time.After(time.Second):
		t.Fatal("late progress callback did not return after completion")
	}
	select {
	case event := <-events:
		if event.Output != "output" {
			t.Fatalf("late callback changed final queued output: %+v", event)
		}
	default:
		t.Fatal("nonblocking emitter lost its most recent event")
	}
	cancel()
	cancelledEvents := make(chan viewmodel.OperationProgress, 1)
	cancelled := setupProgressEmitter{ctx: ctx, events: cancelledEvents, done: make(chan struct{})}
	cancelledDone := make(chan struct{})
	go func() {
		cancelled.emit(viewmodel.OperationProgress{Output: "after cancellation"})
		close(cancelledDone)
	}()
	select {
	case <-cancelledDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled progress callback did not return")
	}
	if len(cancelledEvents) != 0 {
		t.Fatal("cancelled operation retained output for a later operation")
	}
}

// setupSection selects a section from the L3 navigation list and opens its
// fields in the L4 details pane. Sections are ordered by openSetupForm.
func setupSection(m *Model, down int) {
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft}) // Return to L3 even when called from details.
	for i := 0; i < down; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
}

func (b *setupBackendFixture) UISetupPreview(_ context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = q
	key := setupProfileKey(q)
	preview := viewmodel.SetupPreview{
		Key:           key,
		PackageName:   "Plain",
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "inputs", Title: "Inputs", Fields: []string{"repo", "mode"}}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string", Required: true}, Value: "/repos/team", HasValue: true, Provenance: "source", ProvenancePath: "/catalog/aact.toml", Editable: true},
			{Definition: catalog.Input{Name: "mode", Label: "Mode", Type: "choice", Required: true, Options: []catalog.Choice{{Value: "fast", Label: "Fast"}, {Value: "safe", Label: "Safe"}}}, Value: "safe", HasValue: true, Provenance: "package", ProvenancePath: "/catalog/plain/package.toml", Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "all", Path: "/home/test/.agents/skills", Selected: true}, {ID: "codex", Path: "/home/test/.agents/skills"}},
	}
	preview.Inputs = append(preview.Inputs, b.extraInputs...)
	return preview, nil
}

func TestSetupDestinationFieldDoesNotOverwritePackageInput(t *testing.T) {
	b := &setupBackendFixture{extraInputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "destination", Label: "Output destination", Type: "string"}, Value: "/output", HasValue: true, Editable: true}}}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	startHomeSetup(m)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("setup form did not save")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || b.installRequest.Inputs["destination"] != "/output" || !reflect.DeepEqual(b.installRequest.DestinationIDs, []string{"all"}) || b.installRequest.ExternalURL != "" {
		t.Fatalf("package destination collided with installer destination field: %+v", b.installRequest)
	}
}

func (b *setupBackendFixture) UIInstall(_ context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.installRequest = &q
	if b.installResult != nil || b.installErr != nil {
		if b.installResult == nil {
			return viewmodel.OperationResult{}, b.installErr
		}
		return *b.installResult, b.installErr
	}
	return viewmodel.OperationResult{Message: "Installed", Changes: []state.Installation{{AgentID: "all", Component: "skill"}}}, nil
}

func TestSetupResultDistinguishesSavedInputsFromFailedApply(t *testing.T) {
	b := &setupBackendFixture{installResult: &viewmodel.OperationResult{Saved: true}, installErr: errors.New("MCP port 9000 is already allocated")}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(m.Init()())
	startHomeSetup(m)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Save did not submit")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if !strings.Contains(m.output, "Inputs saved") || !strings.Contains(m.output, "port 9000 is already allocated") || strings.Contains(m.output, "configured") {
		t.Fatalf("result hid save/apply distinction: %q", m.output)
	}
}

func TestSelectedForeignWorkspaceForwardsItsExplicitEndpointOnSave(t *testing.T) {
	for _, owner := range []string{"other-aact", "unknown"} {
		t.Run(owner, func(t *testing.T) {
			m, profiles := typedProfileFixture()
			profiles.snapshot.Profiles[1].Ownership = owner
			m.Update(m.load()())
			backend := &registrationWorkspaceBackend{Backend: profiles, profileBackend: profiles, setupBackendFixture: &setupBackendFixture{}}
			m.backend = backend
			m.focusPane(ProfilesPane)
			foreignKey := profiles.snapshot.Profiles[1].Key
			m.selectPane(ProfilesPane, 1)
			_, open := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if open == nil {
				t.Fatal("selected foreign profile did not open its target workspace")
			}
			m.Update(open())
			if m.workspace == nil || m.workspace.Key != foreignKey || m.workspace.Profile == nil || m.workspace.Profile.URL != "http://127.0.0.1:8765/mcp" {
				t.Fatalf("explicitly selected foreign target facts changed: %+v", m.workspace)
			}
			capabilityBackend := openCapabilityProfileWorkspace(t, m, profiles, "foreign", "Agents")
			if !m.form.ReconcileDraftField(m.pendingSetupField, []string{"codex"}) {
				t.Fatal("could not select Codex for the complete capability binding")
			}
			save := m.applySetup(m.form.Values())
			if save == nil {
				t.Fatalf("foreign workspace Save did not submit the selected capability draft: output=%q form=%t workspace=%+v values=%v", m.output, m.form != nil, m.workspace, m.form.Values())
			}
			m.Update(runTeaCmd(t, m, save))
			request := capabilityBackend.setup.installRequest
			if request == nil || request.SetupRequest.Ref.PackID != foreignKey.Source || request.SetupRequest.Ref.CapabilityID != foreignKey.Package || request.SetupRequest.Ref.Name != foreignKey.Target || request.ExternalURL != "http://127.0.0.1:8765/mcp" || !containsString(request.DestinationIDs, "codex") {
				t.Fatalf("Save did not forward the selected foreign endpoint and destinations: %+v", request)
			}
		})
	}
}

func TestCapabilitySetupUsesOneDeclaredInputAndDestinationForm(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	startHomeSetup(m)
	if m.busy || m.form == nil || m.pendingSetup == nil {
		t.Fatalf("skill-only setup did not immediately request preview: busy=%v form=%v", m.busy, m.form)
	}
	if m.form == nil || m.busy || b.previewRequest.Ref.PackID != "team-source" || b.previewRequest.Ref.CapabilityID != "plain" || b.previewRequest.Ref.Name != "test-profile" {
		t.Fatalf("typed setup preview did not open: %+v, %v", b.previewRequest, m.form)
	}
	view := m.View().Content
	for _, want := range []string{"Sections", "Inputs"} {
		if !strings.Contains(view, want) {
			t.Fatalf("single setup form missing %q:\n%s", want, view)
		}
	}
	setupSection(m, 1) // Inputs follows the workspace Overview section.
	view = m.View().Content
	for _, want := range []string{"Repository", "Mode", "aact.toml"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup Inputs section missing %q:\n%s", want, view)
		}
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	if !strings.Contains(m.View().Content, "Agents") || !strings.Contains(m.View().Content, "All") {
		t.Fatalf("setup Agents section missing:\n%s", m.View().Content)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || !m.busy || m.form != nil {
		t.Fatal("Save did not apply the typed setup once")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || !reflect.DeepEqual(b.installRequest.DestinationIDs, []string{"all"}) || b.installRequest.Inputs["repo"] != "/repos/team" || b.installRequest.Inputs["mode"] != "safe" {
		t.Fatalf("one-shot install received wrong form values: %+v", b.installRequest)
	}
	if !strings.Contains(m.output, "Installed") {
		t.Fatalf("install result hidden: %s", m.output)
	}
}

func TestTargetChoiceFormCanRemovePreviouslySavedEmptyEntry(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "cluster-inspector", Target: "target-a"},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "database", Title: "Databases", Fields: []string{"connections"}}},
		Inputs: []viewmodel.SetupInput{{
			Definition: catalog.Input{Name: "connections", Label: "Read-only database queries (optional)", Type: "multichoice", OptionsFrom: "dbms.*.tenants.*", Options: []catalog.Choice{{Value: "shared_postgres/plane", Label: "Plane — shared_postgres/plane"}}},
			Value:      []string{""}, HasValue: true, Provenance: "saved", Editable: true,
		}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/tmp/codex/config.toml", Selected: true}},
	})
	setupSection(m, 0) // Databases.
	if !strings.Contains(m.View().Content, "[x] Empty saved entry — deselect to remove") {
		t.Fatalf("saved empty entry cannot be identified or removed: %s", m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // Move from Plane to the saved empty entry.
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if strings.Contains(m.View().Content, "[x] Empty saved entry") {
		t.Fatalf("saved empty entry stayed selected after Enter: %s", m.View().Content)
	}
}
func TestFixedTargetInputIsHiddenAndNotSubmitted(t *testing.T) {
	b := &setupBackendFixture{extraInputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "api_server", Label: "API server", Type: "string", Required: true}, Value: "https://fixed.example", HasValue: true, Provenance: "target", Editable: false}}}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	startHomeSetup(m)
	if strings.Contains(m.View().Content, "API server") {
		t.Fatal("fixed target input appeared in form")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Save unavailable")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil {
		t.Fatal("Save did not submit")
	}
	if _, ok := b.installRequest.Inputs["api_server"]; ok {
		t.Fatalf("fixed target input was submitted: %+v", b.installRequest.Inputs)
	}
}

func TestLongProvenanceKeepsInputValueVisible(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "plain", Target: "default"},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "repo", Title: "Inputs", Fields: []string{"repo"}}},
		Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string"},
			Value: "/repos/team", HasValue: true, Provenance: "source",
			ProvenancePath: "/a/very/long/checkout/path/for/a/company/private/capabilities/repository/that/exceeds/the/terminal/width/aact.toml", Editable: true}},
		Destinations: []viewmodel.SetupDestination{{ID: "all", Path: "/home/test/.agents/skills", Selected: true}},
	})
	setupSection(m, 0) // Inputs.
	view := m.View().Content
	if !strings.Contains(view, "Repository [source]: /repos/team") || !strings.Contains(view, "aact.toml") {
		t.Fatalf("provenance hid the field's value: %s", view)
	}
}

func TestManagedProfileParametersOpenItsExactSetupTarget(t *testing.T) {
	b := &setupEnvironmentBackend{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(m.Init()())
	m.catalog[0].MCP = &catalog.MCP{Transport: "streamable-http"}
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "team-source", Package: "plain", Environment: "company", Target: "production"}, RuntimeStatus: "never-started", Ownership: "local"}}}
	m.reconcileHome()
	m.focusPane(ProfilesPane)
	key := m.profileSnapshot.Profiles[0].Key
	for index, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key == key {
			m.selectContext(index)
			break
		}
	}
	cmd := m.homeOperation("parameters")
	if cmd == nil || !m.busy {
		t.Fatal("Edit parameters did not request the shared workspace")
	}
	m.Update(cmd())
	if b.previewRequest != (m.packProfileRequest(key)) || m.form == nil || m.form.SectionTitle() != "Overview" {
		t.Fatalf("wrong profile target or missing shared editor: %+v section=%q form=%v", b.previewRequest, func() string {
			if m.form == nil {
				return ""
			}
			return m.form.SectionTitle()
		}(), m.form != nil)
	}
}

func TestCapabilitySetupUsesExplicitPackProfile(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(m.Init()())
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{Ref: config.ProfileRef{PackID: "team-source", CapabilityID: "plain", Name: "target-a"}}, "Overview")
	if cmd == nil {
		t.Fatal("opening an explicit pack profile did not start setup")
	}
	m.Update(cmd())
	want := viewmodel.SetupRequest{Ref: config.ProfileRef{PackID: "team-source", CapabilityID: "plain", Name: "target-a"}}
	if b.previewRequest != want {
		t.Fatalf("capability setup did not pass selected ProfileRef: got %+v, want %+v", b.previewRequest, want)
	}
}

func TestSetupFormNamesCapabilityAndEnvironmentTarget(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(setupPreviewMsg{preview: viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "cluster-inspector", Target: "target-a"},
		PackageName:   "Cluster Inspector",
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token"}}},
		Inputs:        []viewmodel.SetupInput{{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret"}, Editable: true}},
		Destinations:  []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", Selected: true}},
	}})
	view := m.View().Content
	for _, want := range []string{"New setup · Cluster Inspector", "Authentication", "Agents"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup form does not show %q:\n%s", want, view)
		}
	}
	if m.pendingSetup == nil || m.pendingSetup.Key.Environment != "" || m.pendingSetup.Key.Target != "target-a" {
		t.Fatalf("setup preview did not retain the profile identity: %+v", m.pendingSetup)
	}
}

func TestExclusiveCredentialsShowMethodAndInactiveBranch(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "inspect", Environment: "company", Target: "production"},
		PackageName:   "Inspector",
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "credential"}, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", Selected: true}},
	})
	view := m.View().Content
	for _, want := range []string{"Authentication", "Agents"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup missing %q:\n%s", want, view)
		}
	}
	if m.pendingSetup == nil || m.pendingSetup.Key.Environment != "company" || m.pendingSetup.Key.Target != "production" {
		t.Fatalf("setup lost the selected environment target: %+v", m.pendingSetup)
	}
	m.form.SelectSection("Authentication")
	view = m.View().Content
	for _, want := range []string{"Token", "Source kubeconfig"} {
		if !strings.Contains(view, want) {
			t.Fatalf("authentication field %q was not visible:\n%s", want, view)
		}
	}
	m.form.SelectSection("Agents")
	if !strings.Contains(m.View().Content, "Codex") {
		t.Fatalf("setup Destinations section omitted Codex:\n%s", m.View().Content)
	}
}

func TestNoEnvironmentSetupTitleHasNoBlankSegment(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.openSetupForm(viewmodel.SetupPreview{Key: state.Key{Source: "one", Package: "inspect", Target: "default"}, PackageName: "Inspector", Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}}})
	view := m.View().Content
	if strings.Contains(view, "·  / default") || strings.Contains(view, "Environment:") || !strings.Contains(view, "Install · Inspector") || !strings.Contains(view, "Agents") {
		t.Fatalf("no-environment title/context is unclear: %s", view)
	}
}

func TestSetupFormRendersCurrentMCPRegistrationName(t *testing.T) {
	const activeName = "grafana-inspector-home-target-a-a7a19beabe523073"
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:            state.Key{Source: "team", Package: "grafana-inspector", Environment: "sample-env", Target: "target-a"},
		PackageName:    "Grafana Inspector",
		MCP:            true,
		MCPDefinitions: []catalog.MCP{{Name: "grafana-inspector", RegistrationNameInput: "registration_name"}},
		HasManifestUI:  true,
		Sections:       []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"registration_name"}}},
		Inputs: []viewmodel.SetupInput{{
			Definition: catalog.Input{Name: "registration_name", Label: "MCP registration name", Type: "string", Hint: "User-editable name shown by MCP clients for this server. Currently registered as: " + activeName},
			Value:      activeName, HasValue: true, Provenance: "registration", Editable: true,
		}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	m.form.SelectSection("Connection")
	form := ansi.Strip(m.form.View().Content)
	if !strings.Contains(form, "MCP registration name") {
		t.Fatalf("registration form omitted its label:\n%s", form)
	}
	compactRightPane := func(content string) string {
		var right []string
		for _, line := range strings.Split(content, "\n") {
			if separator := strings.Index(line, "│"); separator >= 0 {
				right = append(right, line[separator+len("│"):])
			}
		}
		compact := strings.Join(strings.Fields(strings.Join(right, "")), "")
		return strings.ReplaceAll(compact, "║", "")
	}
	if !strings.Contains(compactRightPane(form), activeName) {
		t.Fatalf("registration form did not render the complete wrapped name %q:\n%s", activeName, form)
	}
	screen := ansi.Strip(m.View().Content)
	if !strings.Contains(compactRightPane(screen), activeName) {
		t.Fatalf("visible setup screen omitted complete active registration name %q:\n%s", activeName, screen)
	}
}

func TestSwitchingAuthenticationClearsPreviouslyPrefilledCredential(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.openSetupForm(viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "inspect", Target: "default"},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Type: "secret", ExclusiveGroup: "credential"}, Value: "old-token", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	setupSection(m, 0)                           // Authentication.
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // Focus Source kubeconfig.
	_, _ = m.form.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	_, _ = m.form.Update(tea.KeyPressMsg{Code: 'x', Text: "/tmp/new-kubeconfig"})
	_, _ = m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("switching authentication blocked Save: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || b.installRequest.Inputs["token"] != "" {
		t.Fatalf("inactive prefilled token was submitted: %+v", b.installRequest)
	}
}

func TestBothPrefilledCredentialsKeepOnlySelectedMethod(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.openSetupForm(viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "inspect", Target: "default"},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Type: "secret", ExclusiveGroup: "credential"}, Value: "old-token", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Value: "/tmp/existing-kubeconfig", HasValue: true, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("prefilled exclusive credentials blocked Save: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || b.installRequest.Inputs["token"] != "" || b.installRequest.Inputs["kubeconfig"] != "/tmp/existing-kubeconfig" {
		t.Fatalf("inactive prefilled credential was submitted: %+v", b.installRequest)
	}
}

func TestFixedTargetCredentialDisablesOtherMethod(t *testing.T) {
	b := &setupBackendFixture{}
	m := NewContext(context.Background(), b)
	m.openSetupForm(viewmodel.SetupPreview{
		Key:           state.Key{Source: "team-source", Package: "inspect", Target: "default"},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "credential"}, Value: "fixed-token", HasValue: true, Provenance: "target", Editable: false},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "credential"}, Value: "/tmp/old-kubeconfig", HasValue: true, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Selected: true}},
	})
	view := m.View().Content
	if strings.Contains(view, "Authentication") || strings.Contains(view, "Token") || strings.Contains(view, "Source kubeconfig") {
		t.Fatalf("fixed credential left an editable authentication method: %s", view)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("fixed credential blocked Save: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || b.installRequest.Inputs["kubeconfig"] != "" {
		t.Fatalf("fixed credential's sibling was submitted: %+v", b.installRequest)
	}
	if _, submitted := b.installRequest.Inputs["token"]; submitted {
		t.Fatalf("fixed target credential was submitted: %+v", b.installRequest.Inputs)
	}
}

// chooserSetupBackend remains a fixture alias for old workspace-route tests;
// it models setup behavior but offers no environment-target chooser API.
type chooserSetupBackend = setupBackendFixture
