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
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if m.form.SectionTitle() != "Agent access" || !strings.Contains(m.View().Content, "claude") {
		t.Fatalf("foreign runtime blocked local destinations in Agent access: %s", m.View().Content)
	}
	if strings.Contains(m.View().Content, "Start") || strings.Contains(m.View().Content, "Stop") {
		t.Fatalf("foreign runtime showed local runtime control: %s", m.View().Content)
	}
	install := activateAgentAccessActionByKeyboard(t, m, "Install selected")
	if install == nil {
		t.Fatalf("Agent access Install selected did not invoke setup install: %s", m.View().Content)
	}
	_, install = m.Update(install())
	if install == nil {
		t.Fatal("keyboard activation did not dispatch the install operation")
	}
	m.Update(runTeaCmd(t, m, install))
	if backend.installRequest == nil || backend.installRequest.SetupRequest.Ref.Name != "foreign" || !containsString(backend.installRequest.DestinationIDs, "codex") {
		t.Fatalf("Agent access install did not bind destinations on selected target: %+v", backend.installRequest)
	}
}

func TestUXAgentAccessInstallMouseUsesExactSelectedInstance(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	clickFormChoice(t, m, "[ ] Codex")
	if !containsStringFromValue(m.form.Values()[m.pendingSetupField], "codex") {
		t.Fatalf("mouse did not select the exact Codex destination: %#v", m.form.Values()[m.pendingSetupField])
	}
	msg := clickFormAction(t, m, "Install selected")
	action, ok := msg.(forms.ActionMsg)
	if !ok || action.SectionID != sectionAgentsID || action.ID != "install-selected" {
		t.Fatalf("mouse click returned action %#v", msg)
	}
	_, install := m.Update(action)
	if install == nil {
		t.Fatalf("mouse activation did not dispatch install for selected instance:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(runTeaCmd(t, m, install))
	if backend.installRequest == nil || backend.installRequest.SetupRequest.Ref.Name != "foreign" || !containsString(backend.installRequest.DestinationIDs, "codex") {
		t.Fatalf("mouse install lost selected instance or exact agent target: %+v", backend.installRequest)
	}
	if backend.profileBackend.request != nil {
		t.Fatal("Agent access install unexpectedly changed the MCP runtime")
	}
}

func activateAgentAccessActionByKeyboard(t *testing.T, m *Model, label string) tea.Cmd {
	return activateAgentAccessActionByKeyboardWithSelection(t, m, label, true)
}

func activateAgentAccessActionByKeyboardWithSelection(t *testing.T, m *Model, label string, selectFirst bool) tea.Cmd {
	t.Helper()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if selectFirst {
		m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	}
	for attempts := 0; attempts < 20; attempts++ {
		if strings.Contains(ansi.Strip(m.form.View().Content), "> [ "+label+" ]") {
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return cmd
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatalf("keyboard could not reach %q after bounded navigation:\n%s", label, ansi.Strip(m.form.View().Content))
	return nil
}

func clickFormChoice(t *testing.T, m *Model, choice string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for y, line := range lines {
		if x := strings.Index(line, choice); x >= 0 {
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			return
		}
	}
	t.Fatalf("visible destination choice %q not found:\n%s", choice, strings.Join(lines, "\n"))
}

func clickFormAction(t *testing.T, m *Model, label string) tea.Msg {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	needle := "[ " + label + " ]"
	for y, line := range lines {
		if x := strings.Index(line, needle); x >= 0 {
			_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if cmd == nil {
				t.Fatalf("mouse click on %q did not activate the visible action:\n%s", label, ansi.Strip(m.View().Content))
			}
			return cmd()
		}
	}
	t.Fatalf("visible action %q not found in full TUI view:\n%s", label, strings.Join(lines, "\n"))
	return nil
}

type registrationWorkspaceBackend struct {
	Backend
	profileBackend *profileBackend
	*setupBackendFixture
	inputSaveRequest  *viewmodel.SetupInstallRequest
	runtimeAction     string
	runtimeKey        state.Key
	removedRuntimeKey state.Key
	removedRuntimeID  string
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
func (b *registrationWorkspaceBackend) UIRemoveRuntimeRecord(_ context.Context, key state.Key, id string) (string, error) {
	b.removedRuntimeKey, b.removedRuntimeID = key, id
	return "removed exact runtime " + id, nil
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
	selectWorkspaceSectionByKeyboard(t, m, sectionAgentsID)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	return m, backend
}

func selectWorkspaceSectionByKeyboard(t *testing.T, m *Model, sectionID string) {
	t.Helper()
	if m.form == nil {
		t.Fatal("workspace form is not open")
	}
	m.form.FocusSection()
	for attempts := 0; attempts < 20; attempts++ {
		if m.form.SectionID() == sectionID {
			return
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatalf("keyboard could not select workspace section %q:\n%s", sectionID, ansi.Strip(m.View().Content))
}

func activateWorkspaceActionByKeyboard(t *testing.T, m *Model, label string) tea.Msg {
	t.Helper()
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	needle := "> [ " + label + " ]"
	for attempts := 0; attempts < 32; attempts++ {
		if strings.Contains(ansi.Strip(m.form.View().Content), needle) {
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatalf("Enter on visible action %q did not activate it:\n%s", label, ansi.Strip(m.View().Content))
			}
			return cmd()
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatalf("keyboard could not reach visible action %q:\n%s", label, ansi.Strip(m.View().Content))
	return nil
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

func TestUXRuntimeRecordActionRemovesOnlySelectedMCPAndRuntimeID(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	parentKey := m.workspace.Key
	childKey := parentKey
	childKey.MCP = "plain"
	instance := viewmodel.CapabilityInstance{Key: parentKey, MCPs: []viewmodel.RuntimeStatus{{
		MCPID: "plain", Key: childKey, Status: "multiple", Ownership: "local",
		RuntimeRecords: []viewmodel.RuntimeRecord{
			{ID: "container-a", Name: "aact-plain-a", Status: "missing", Ownership: "local"},
			{ID: "container-b", Name: "aact-plain-b", Status: "missing", Ownership: "local"},
		},
	}}}
	if m.capabilityInstances == nil {
		m.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{}
	}
	m.capabilityInstances[parentKey.Package] = viewmodel.CapabilityInstanceSnapshot{Instances: []viewmodel.CapabilityInstance{instance}}
	m.workspace.Section = "Runtime"
	m.workspace.SectionID = sectionRuntimeID
	m.configureWorkspaceForm(*m.workspace.Preview)
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	for _, want := range []string{"container-a", "container-b", "Forget missing runtime container-a"} {
		if !strings.Contains(ansi.Strip(m.View().Content), want) {
			t.Fatalf("Runtime screen omitted %q:\n%s", want, ansi.Strip(m.View().Content))
		}
	}
	message := clickFormAction(t, m, "Forget missing runtime container-a")
	if _, ok := message.(forms.ActionMsg); !ok {
		t.Fatalf("mouse click returned %T, want visible form action", message)
	}
	_, cmd := m.Update(message)
	if cmd == nil {
		t.Fatal("visible exact runtime action did not dispatch removal")
	}
	result := cmd()
	if _, ok := result.(operationMsg); !ok {
		t.Fatalf("exact runtime action returned %T, want operation result", result)
	}
	if backend.removedRuntimeKey != childKey || backend.removedRuntimeID != "container-a" {
		t.Fatalf("runtime action targeted key=%+v ID=%q; want exact child=%+v ID=container-a", backend.removedRuntimeKey, backend.removedRuntimeID, childKey)
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
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	msg := clickFormAction(t, m, "Uninstall selected")
	if action, ok := msg.(forms.ActionMsg); !ok || action.SectionID != sectionAgentsID || action.ID != "uninstall-selected" {
		t.Fatalf("mouse click did not activate exact Uninstall selected action: %#v", msg)
	}
	_, cmd := m.Update(msg)
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

func TestUXAgentAccessUninstallKeyboardUsesExactSelectedInstance(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	preview := *m.workspace.Preview
	preview.Configured = true
	preview.Destinations[0].Selected = true
	m.configureWorkspaceForm(preview)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	cmd := activateAgentAccessActionByKeyboardWithSelection(t, m, "Uninstall selected", false)
	if cmd == nil {
		t.Fatalf("keyboard did not activate Uninstall selected: %s", m.View().Content)
	}
	_, cmd = m.Update(cmd())
	if cmd == nil {
		t.Fatal("keyboard uninstall did not dispatch backend operation")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || b.installRequest.SetupRequest.Ref.Name != "foreign" || len(b.installRequest.DestinationIDs) != 0 {
		t.Fatalf("keyboard uninstall did not submit empty desired set for exact instance: %+v", b.installRequest)
	}
	if b.profileBackend.request != nil {
		t.Fatal("Agent access uninstall changed runtime")
	}
}

func TestUXAgentAccessUninstallMouseUsesExactSelectedInstance(t *testing.T) {
	m, b := openRegistrationActionForUX(t)
	preview := *m.workspace.Preview
	preview.Configured = true
	preview.Destinations[0].Selected = true
	m.configureWorkspaceForm(preview)
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	msg := clickFormAction(t, m, "Uninstall selected")
	action, ok := msg.(forms.ActionMsg)
	if !ok || action.SectionID != sectionAgentsID || action.ID != "uninstall-selected" {
		t.Fatalf("mouse click did not activate Uninstall selected: %#v", msg)
	}
	_, cmd := m.Update(action)
	if cmd == nil {
		t.Fatalf("mouse uninstall did not dispatch backend operation: %s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.installRequest == nil || b.installRequest.SetupRequest.Ref.Name != "foreign" || len(b.installRequest.DestinationIDs) != 0 {
		t.Fatalf("mouse uninstall did not submit empty desired set for exact instance: %+v", b.installRequest)
	}
	if b.profileBackend.request != nil {
		t.Fatal("Agent access uninstall changed runtime")
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
	for _, want := range []string{"[ Install selected ]", "[ Uninstall selected ]"} {
		if !strings.Contains(v, want) {
			t.Fatalf("Agent access action %q not visible: %s", want, v)
		}
	}
	if strings.Contains(v, "[ Save ]") || strings.Contains(v, "[ Cancel ]") {
		t.Fatalf("Inputs Save/Cancel leaked into Agent access: %s", v)
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
	selectWorkspaceSectionByKeyboard(t, m, sectionInputsID)
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
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	view := m.form.View().Content
	for _, want := range []string{"Agent access", "Install selected", "Uninstall selected", "does not start Docker"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Agent access stage missing %q:\n%s", want, view)
		}
	}
	cmd := activateAgentAccessActionByKeyboard(t, m, "Install selected")
	if cmd == nil {
		t.Fatal("Install selected did not start installation")
	}
	_, cmd = m.Update(cmd())
	if cmd == nil {
		t.Fatal("Install selected keyboard action did not dispatch its backend operation")
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
