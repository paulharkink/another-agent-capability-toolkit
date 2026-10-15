package tui

import (
	"context"
	"github.com/charmbracelet/x/ansi"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXForeignEndpointCanRegisterWithoutRuntimeControl(t *testing.T) {
	m, profileBackend := typedProfileFixture()
	backend := &registrationWorkspaceBackend{Backend: profileBackend, profileBackend: profileBackend, setupBackendFixture: &setupBackendFixture{}}
	m.backend = backend
	m.focusPane(ProfilesPane)
	foreign := state.Key{Source: "team-source", Package: "plain", Target: "foreign"}
	for index, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key == foreign {
			m.selectContext(index)
			break
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the foreign target did not open its workspace")
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || m.workspace.Instance == nil {
		t.Fatalf("foreign target workspace did not load: %s", m.View().Content)
	}
	if m.workspace.Instance.Ownership != "other-aact" || !strings.Contains(m.View().Content, "http://127.0.0.1:8765/mcp") {
		t.Fatalf("foreign endpoint/ownership facts missing from Overview: %s", m.View().Content)
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	if m.form.SectionTitle() != "Agent access" || !strings.Contains(m.View().Content, "claude") {
		t.Fatalf("foreign runtime blocked local destinations in Agent access: %s", m.View().Content)
	}
	press(m, tea.KeyRight, " ")
	press(m, tea.KeySpace, " ")
	if strings.Contains(m.View().Content, "Start") || strings.Contains(m.View().Content, "Stop") {
		t.Fatalf("foreign runtime showed local runtime control: %s", m.View().Content)
	}
	_, install := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if install == nil {
		t.Fatalf("Agent access Install selected did not invoke setup install: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, install))
	if backend.installRequest == nil || backend.installRequest.SetupRequest.Ref.Name != "foreign" || !containsString(backend.installRequest.DestinationIDs, "codex") {
		t.Fatalf("Agent access install did not bind destinations on selected target: %+v", backend.installRequest)
	}
}

type registrationWorkspaceBackend struct {
	Backend
	profileBackend *profileBackend
	*setupBackendFixture
	inputSaveRequest *viewmodel.SetupInstallRequest
	runtimeAction    string
	runtimeKey       state.Key
}

func (b *registrationWorkspaceBackend) UIProfileSnapshot(ctx context.Context) (viewmodel.InstanceSnapshot, error) {
	return b.profileBackend.UIProfileSnapshot(ctx)
}
func (b *registrationWorkspaceBackend) UIConfigureRegistrations(ctx context.Context, request viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	return b.profileBackend.UIConfigureRegistrations(ctx, request)
}
func (b *registrationWorkspaceBackend) CheckConnection(ctx context.Context, url, transport string) viewmodel.ConnectionObservation {
	return b.profileBackend.CheckConnection(ctx, url, transport)
}
func (b *registrationWorkspaceBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	return viewmodel.SetupPreview{Key: setupProfileKey(request), PackageName: "Plain", MCP: true, HasManifestUI: true,
		Sections: []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"endpoint"}}},
		Inputs:   []viewmodel.SetupInput{{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint URI", Type: "string"}, Value: "http://127.0.0.1:8765/mcp", HasValue: true, Editable: true}}, MCPDefinitions: []catalog.MCP{{Name: "plain", Transport: "streamable-http"}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex/skills", ConfigPath: "/home/test/.codex/config.toml", Detection: "installed"}, {ID: "claude", Path: "/home/test/.claude/skills", ConfigPath: "/home/test/.claude.json", Detection: "installed"}}}, nil
}
func (b *registrationWorkspaceBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setupBackendFixture.UIInstall(ctx, request)
}
func (b *registrationWorkspaceBackend) UISaveInputs(_ context.Context, request viewmodel.SetupInstallRequest) error {
	copy := request
	b.inputSaveRequest = &copy
	return nil
}
func (b *registrationWorkspaceBackend) UIInstanceRun(_ context.Context, action string, key state.Key) (string, error) {
	b.runtimeAction, b.runtimeKey = action, key
	return "started", nil
}

func openRegistrationActionForUX(t *testing.T) (*Model, *registrationWorkspaceBackend) {
	t.Helper()
	m, profile := typedProfileFixture()
	backend := &registrationWorkspaceBackend{Backend: profile, profileBackend: profile, setupBackendFixture: &setupBackendFixture{}}
	m.backend = backend
	m.focusPane(ProfilesPane)
	foreign := state.Key{Source: "team-source", Package: "plain", Target: "foreign"}
	for i, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key == foreign {
			m.selectContext(i)
			break
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("profile Enter did not open workspace")
	}
	m.Update(cmd())
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	return m, backend
}

func TestUXRegistrationEndpointAndAgentDetailsAreIndependent(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	m.agentManagement = []viewmodel.AgentManagementRow{{ID: "codex", Name: "Codex", Detection: "installed", Home: "/tmp/codex-home", EffectiveConfigPath: "/tmp/codex-home/.codex/config.toml", WriteConfigPath: "/tmp/codex-home/.codex/config.toml"}}
	if strings.Contains(m.View().Content, "Detection: installed") || strings.Contains(m.View().Content, "Config path:") {
		t.Fatalf("Agents section repeats detection or destination details already shown in Destinations: %s", m.View().Content)
	}
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	v := m.View().Content
	for _, want := range []string{"Runtime", "plain", "Check plain connection"} {
		if !strings.Contains(v, want) {
			t.Fatalf("runtime child action lacks %q: %s", want, v)
		}
	}
	if b.profileBackend.checkedURL != "" {
		t.Fatal("opening capability performed endpoint check")
	}
}
func TestUXRegistrationOptionsExcludeGenericAndAll(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	v := m.View().Content
	for _, bad := range []string{"generic:legacy", "generic-mcp", "[all]"} {
		if strings.Contains(v, bad) {
			t.Fatalf("named destination UI exposed %q: %s", bad, v)
		}
	}
	if !strings.Contains(v, "codex") || !strings.Contains(v, "claude") {
		t.Fatalf("named local destinations missing: %s", v)
	}
}
func TestUXAgentsSectionUsesUnifiedCapabilityBinding(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	v := m.View().Content
	if !strings.Contains(v, "Agents") {
		t.Fatalf("complete capability binding form missing: %s", v)
	}
	for _, bad := range []string{"Remove local registrations", "Remove selected registrations"} {
		if strings.Contains(v, bad) {
			t.Fatalf("obsolete overlay remains: %s", v)
		}
	}
}

func TestUXRuntimeSeparatesLivenessAndContainerAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, authentication string
	}{
		{name: "stopped", runtime: "stopped"},
		{name: "authenticated", runtime: "running", authentication: "AuthenticationComplete"},
		{name: "unauthenticated", runtime: "running", authentication: "AuthenticationFailed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := openRegistrationActionForUX(t)
			if m.workspace.Instance == nil {
				t.Fatal("fixture must include the selected runtime observation")
			}
			m.workspace.Instance.RuntimeStatus = tc.runtime
			switch tc.authentication {
			case "AuthenticationComplete":
				m.workspace.Instance.AuthenticationStatus = viewmodel.AuthenticationComplete
			case "AuthenticationFailed":
				m.workspace.Instance.AuthenticationStatus = viewmodel.AuthenticationFailed
			}
			m.configureWorkspaceForm(*m.workspace.Preview)
			m.form.SelectSectionID(sectionRuntimeID)
			m.form.FocusSection()
			m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
			view := ansi.Strip(m.View().Content)
			wantLiveness := "Status: Stopped"
			if tc.runtime == "running" {
				wantLiveness = "Status: Running"
			}
			if !strings.Contains(view, wantLiveness) {
				t.Fatalf("Runtime view omitted independent liveness label %q:\n%s", wantLiveness, view)
			}
			if !strings.Contains(view, "status: "+tc.runtime) {
				t.Fatalf("Runtime view omitted liveness %q:\n%s", tc.runtime, view)
			}
			if tc.authentication == "" {
				if strings.Contains(view, "Authentication:") {
					t.Fatalf("Runtime view invented authentication status without a health observation:\n%s", view)
				}
			} else if !strings.Contains(view, "Authentication: "+tc.authentication) {
				t.Fatalf("Runtime view omitted container-reported authentication %q:\n%s", tc.authentication, view)
			}
			if tc.authentication != "" {
				line := ""
				for _, candidate := range strings.Split(view, "\n") {
					if strings.Contains(candidate, "status: running · ownership:") {
						line = candidate
					}
				}
				if line == "" || strings.Contains(line, "Authentication:") {
					t.Fatalf("runtime liveness and container authentication must be presented separately:\n%s", line)
				}
			}
		})
	}
}
func TestUXEmptyDesiredSetRemovesRecordedBindings(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	// The binding-removal path is a local AACT installation effect. Keep this
	// fixture independent of the separate foreign-runtime attach guard.
	if m.workspace.Instance != nil {
		m.workspace.Instance.Ownership = "local"
	}
	// Model an existing exact agent binding, then explicitly remove that checked
	// destination through Agent access. Save is intentionally not a registration
	// operation in the staged workspace.
	preview := *m.workspace.Preview
	preview.Configured = true
	preview.Destinations[0].Selected = true
	m.configureWorkspaceForm(preview)
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "uninstall-selected"})
	if cmd == nil {
		t.Fatalf("Uninstall selected did not submit removal of the exact installed binding: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || len(b.installRequest.DestinationIDs) != 0 {
		t.Fatalf("exact uninstallation did not submit the empty desired binding set: request=%+v output=%q view=%s", b.installRequest, m.output, m.View().Content)
	}
	if b.profileBackend.request != nil {
		t.Fatal("legacy registration API was used")
	}
}
func TestUXAgentsBindingDoesNotStopRuntime(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	v := m.View().Content
	for _, bad := range []string{"Start MCP", "Stop MCP"} {
		if strings.Contains(v, bad) {
			t.Fatalf("Agents form offers runtime control %q", bad)
		}
	}
	if b.installRequest != nil {
		t.Fatal("opening form altered binding state")
	}
}
func TestUXPartialRegistrationFailureShowsErrorAndUnchecksFailedAgent(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	b.installResult = &viewmodel.OperationResult{Message: "Binding update completed with errors", Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}}, Errors: []string{`claude: refusing foreign MCP registration "plain-dev"`}}
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyDown, "")
	press(m, tea.KeySpace, " ")
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if cmd == nil {
		t.Fatal("Install selected did not submit capability")
	}
	m.Update(runTeaCmd(t, m, cmd))
	v := m.View().Content
	if !strings.Contains(v, "codex: mcp configured") || !strings.Contains(v, "refusing foreign MCP registration") {
		t.Fatalf("result omitted effects/error: %s", v)
	}
	if b.installRequest == nil || len(b.installRequest.DestinationIDs) != 2 {
		t.Fatalf("binding draft not submitted once: %+v", b.installRequest)
	}
}
func TestUXRegistrationButtonsExposeSeparateInstallAndUninstall(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	v := m.View().Content
	for _, want := range []string{"[ Install selected ]", "[ Uninstall selected ]", "[ Cancel ]"} {
		if !strings.Contains(v, want) {
			t.Fatalf("Agent access action %q not visible: %s", want, v)
		}
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if cmd == nil {
		t.Fatal("Install selected did not submit")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil {
		t.Fatal("Install selected did not submit")
	}
	if m.workspace == nil || !m.workspace.Active {
		t.Fatal("save left the capability workspace")
	}
}

func TestUXInputsStageShowsSaveAndCancelAndSaveDoesNotInstall(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	m.form.SelectSectionID("tool:inputs")
	m.form.FocusSection()
	view := m.form.View().Content
	for _, want := range []string{"Inputs", "Save", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Inputs stage must visibly offer %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Save and apply") {
		t.Fatalf("Inputs stage still combines saving and applying:\n%s", view)
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: "tool:inputs", ID: "save-inputs"})
	if cmd == nil {
		t.Fatal("Save inputs did not issue its save-only operation")
	}
	m.Update(cmd())
	if b.inputSaveRequest == nil {
		t.Fatal("Save inputs did not call save-only backend")
	}
	if b.installRequest != nil {
		t.Fatal("saving inputs installed agent configuration")
	}
}

func TestUXAgentAccessInstallsWithoutStartingRuntime(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	view := m.form.View().Content
	for _, want := range []string{"Agent access", "Install selected", "Uninstall selected", "does not start Docker"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Agent access stage missing %q:\n%s", want, view)
		}
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if cmd == nil {
		t.Fatal("Install selected did not start installation")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil {
		t.Fatal("Install selected did not call UIInstall")
	}
	if b.profileBackend.request != nil {
		t.Fatal("Install selected started or changed the MCP runtime")
	}
}

func TestUXRuntimeStartRequiresSavedInputsAndInstalledAgentAccess(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	m.workspace.InputsSaved = false
	m.workspace.Installed = false
	m.configureWorkspaceForm(*m.workspace.Preview)
	m.form.FocusSection()
	for attempts := 0; m.form.SectionID() != sectionRuntimeID && attempts < 4; attempts++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.form.SectionID() != sectionRuntimeID {
		t.Fatalf("keyboard did not select Runtime: %q", m.form.SectionID())
	}
	view := m.form.View().Content
	if !strings.Contains(view, "Start plain") || !strings.Contains(view, "Save inputs and in") || !strings.Contains(view, "stall this capability for an agent first") {
		t.Fatalf("Runtime stage lacks gated Start explanation:\n%s", view)
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil {
		t.Fatal("runtime start was issued before inputs and agent installation")
	}
	if !strings.Contains(m.output, "Save inputs and install this capability for an agent before starting the runtime.") {
		t.Fatalf("missing actionable gate message: %q", m.output)
	}
	m.workspace.InputsSaved, m.workspace.Installed = true, true
	m.configureWorkspaceForm(*m.workspace.Preview)
	m.form.SelectSectionID(sectionRuntimeID)
	if strings.Contains(m.form.View().Content, "Save inputs and install this capability for an agent first") {
		t.Fatal("Start remained gated after prerequisites were satisfied")
	}
}

func TestUXRuntimeStartIsBlockedWhenSavedInputsHaveChanged(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	m.workspace.InputsSaved, m.workspace.Installed = true, true
	if m.workspace.Instance == nil {
		t.Fatal("fixture lacks runtime instance")
	}
	m.workspace.Instance.Ownership = "local"
	m.workspace.Instance.CanStart = true
	m.workspace.Instance.RuntimeStatus = "stopped"
	if err := m.form.ApplyValues(map[string]any{"endpoint": "http://changed.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	if !strings.Contains(m.form.View().Content, "Save inputs") {
		t.Fatalf("Runtime did not show the changed-input gate:\n%s", m.form.View().Content)
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil || backend.runtimeAction != "" {
		t.Fatalf("Start ran with an unsaved input draft: cmd=%t action=%q output=%q", cmd != nil, backend.runtimeAction, m.output)
	}
	if !strings.Contains(m.output, "Save inputs") {
		t.Fatalf("Start did not explain how to clear its gate: %q", m.output)
	}
	if !m.form.HasUnsavedChanges() {
		t.Fatal("fixture input edit was not marked dirty")
	}
	save := m.saveSetupInputs(m.form.Values())
	if save == nil {
		t.Fatal("changed inputs could not be saved")
	}
	msg := save().(setupInputsSavedMsg)
	if !msg.inputsChanged {
		t.Fatal("save command did not preserve the dirty-input state")
	}
	m.Update(msg)
	if !m.workspace.InputsSaved || m.workspace.Installed {
		t.Fatalf("saving changed inputs must mark Inputs saved and require agent reinstall: saved=%t installed=%t", m.workspace.InputsSaved, m.workspace.Installed)
	}
	m.form.SelectSectionID(sectionRuntimeID)
	_, cmd = m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil || backend.runtimeAction != "" {
		t.Fatalf("runtime Start became available before reinstall: cmd=%t action=%q", cmd != nil, backend.runtimeAction)
	}
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	m.form.SelectSectionID(sectionAgentsID)
	_, cmd = m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if cmd == nil {
		t.Fatalf("agent reinstall action unavailable after saving inputs: %q", m.output)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if !m.workspace.Installed || backend.installRequest == nil || backend.installRequest.SetupRequest.Ref.Name != "foreign" || !reflect.DeepEqual(backend.installRequest.DestinationIDs, []string{"codex"}) {
		t.Fatalf("reinstall did not restore exact target readiness: installed=%t request=%+v", m.workspace.Installed, backend.installRequest)
	}
	m.form.SelectSectionID(sectionRuntimeID)
	_, cmd = m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd == nil {
		t.Fatalf("Start remained blocked after saving and reinstalling: %q", m.output)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.runtimeAction != "start" || backend.runtimeKey.Target != "foreign" || backend.runtimeKey.MCP != "plain" {
		t.Fatalf("Start did not target the selected instance exactly: action=%q key=%+v", backend.runtimeAction, backend.runtimeKey)
	}
}

func TestUXRuntimeStartsAfterInputsSavedAndAgentInstall(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	m.workspace.InputsSaved, m.workspace.Installed = true, true
	if m.workspace.Instance == nil {
		t.Fatal("fixture lacks runtime instance")
	}
	m.workspace.Instance.Ownership = "local"
	m.workspace.Instance.CanStart = true
	m.workspace.Instance.RuntimeStatus = "stopped"
	m.form.SelectSectionID(sectionRuntimeID)
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd == nil {
		t.Fatalf("Start remained unavailable after prerequisites: %q", m.output)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.runtimeAction != "start" || backend.runtimeKey.Package != "plain" || backend.runtimeKey.Target != "foreign" || backend.runtimeKey.MCP != "plain" {
		t.Fatalf("Start did not target the selected instance/MCP exactly: action=%q key=%+v", backend.runtimeAction, backend.runtimeKey)
	}
}

func TestUXApprovedThreeStageScreensRenderVisibleControls(t *testing.T) {
	t.Run("Inputs", func(t *testing.T) {
		m, _ := openRegistrationActionForUX(t)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		m.Update(tea.KeyPressMsg{Code: tea.KeyUp}) // Navigate from Agent access to Inputs.
		if m.form.SectionID() != sectionInputsID {
			t.Fatalf("keyboard did not select Inputs: %q", m.form.SectionID())
		}
		view := ansi.Strip(m.View().Content)
		for _, want := range []string{"Plain", "Instance: foreign", "Inputs", "Endpoint URI", "Save stores these inputs", "[ Save ]", "[ Cancel ]"} {
			if !strings.Contains(view, want) {
				t.Fatalf("rendered Inputs screen missing %q:\n%s", want, view)
			}
		}
		for _, forbidden := range []string{"Install selected", "Start plain", "Save and apply"} {
			if strings.Contains(view, forbidden) {
				t.Fatalf("Inputs screen exposes later-stage action %q:\n%s", forbidden, view)
			}
		}
		t.Logf("RENDERED INPUTS - 120x36\n%s", view)
	})
	t.Run("Agent access", func(t *testing.T) {
		m, _ := openRegistrationActionForUX(t)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		m.form.SelectSectionID(sectionAgentsID)
		m.form.FocusSection()
		view := ansi.Strip(m.View().Content)
		for _, want := range []string{"Plain", "Agent access", "Select agents to install this capability for", "codex", "claude", "Install selected", "Uninstall selected", "does not start Docker"} {
			if !strings.Contains(view, want) {
				t.Fatalf("rendered Agent access screen missing %q:\n%s", want, view)
			}
		}
		if strings.Contains(view, "Start plain") {
			t.Fatalf("Agent access screen exposes Runtime Start:\n%s", view)
		}
		t.Logf("RENDERED AGENT ACCESS - 120x36\n%s", view)
	})
	t.Run("Runtime", func(t *testing.T) {
		m, _ := openRegistrationActionForUX(t)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		preview := *m.workspace.Preview
		preview.Configured = true
		m.workspace.Installed = true
		if m.workspace.Instance != nil {
			m.workspace.Instance.Ownership = "local"
			m.workspace.Instance.CanStart = true
			m.workspace.Instance.RuntimeStatus = "stopped"
		}
		m.configureWorkspaceForm(preview)
		m.form.SelectSectionID(sectionRuntimeID)
		m.form.FocusSection()
		view := ansi.Strip(m.View().Content)
		for _, want := range []string{"Runtime", "Status: Stopped", "Inputs: Saved", "Agent install: Complete", "This starts the Docker instance", "does not edit agent", "Start plain"} {
			if !strings.Contains(view, want) {
				t.Fatalf("rendered Runtime screen missing %q:\n%s", want, view)
			}
		}
		t.Logf("RENDERED RUNTIME - 120x36\n%s", view)
	})
}
