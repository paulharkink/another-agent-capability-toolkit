package tui

import (
	"context"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXForeignEndpointCanRegisterWithoutRuntimeControl(t *testing.T) {
	m, profileBackend := typedProfileFixture()
	backend := &registrationWorkspaceBackend{Backend: profileBackend, profileBackend: profileBackend, setupBackendFixture: &setupBackendFixture{}}
	m.backend = backend
	m.Update(m.load()())
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
		row, ok := m.selectedContextRow()
		t.Fatalf("Enter on selected foreign target did not open its workspace: context=%+v selected=%t focus=%v output=%q view=%s", row, ok, m.home.Focus, m.output, ansi.Strip(m.View().Content))
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || m.workspace.Instance == nil {
		t.Fatalf("foreign target workspace did not load: %s", m.View().Content)
	}
	m.workspace.InputsSaved = true
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
	previewOverride    *viewmodel.SetupPreview
	inputSaveRequest   *viewmodel.SetupInstallRequest
	runtimeAction      string
	runtimeKey         state.Key
	removedRuntimeKey  state.Key
	removedRuntimeID   string
	capabilitySnapshot map[string]viewmodel.CapabilityInstanceSnapshot
}

func (b *registrationWorkspaceBackend) UIProfileSnapshot(ctx context.Context) (viewmodel.InstanceSnapshot, error) {
	return b.profileBackend.UIProfileSnapshot(ctx)
}
func (b *registrationWorkspaceBackend) UICapabilityInstances(context.Context) (map[string]viewmodel.CapabilityInstanceSnapshot, error) {
	if b.capabilitySnapshot != nil {
		return b.capabilitySnapshot, nil
	}
	// Most journeys seed the older runtime-observation fixture. Expose those
	// same exact targets through the current capability-instance interface so
	// workspace loads exercise the production snapshot path.
	instances := make(map[string][]viewmodel.CapabilityInstance)
	for _, observed := range b.profileBackend.snapshot.Profiles {
		instance := viewmodel.CapabilityInstance{
			Ref: config.InstanceRef{PackID: observed.Key.Source, CapabilityID: observed.Key.Package, Name: observed.Key.Target},
			Key: observed.Key,
			MCPs: []viewmodel.RuntimeStatus{{
				MCPID: "plain", Status: observed.RuntimeStatus, Ownership: observed.Ownership,
				URL: observed.URL, Error: observed.StartDisabledReason, AuthenticationStatus: observed.AuthenticationStatus,
			}},
		}
		for _, agentID := range observed.RegisteredAgents {
			instance.Components = append(instance.Components, viewmodel.ComponentStatus{
				AgentID: agentID, Kind: "mcp", Name: "plain", Status: "installed", Managed: true,
			})
		}
		instances[observed.Key.Package] = append(instances[observed.Key.Package], instance)
	}
	snapshots := make(map[string]viewmodel.CapabilityInstanceSnapshot, len(instances))
	for packageID, rows := range instances {
		snapshots[packageID] = viewmodel.CapabilityInstanceSnapshot{Instances: rows}
	}
	return snapshots, nil
}

// setObservedRuntimeFacts changes the backend observation and reloads the
// capability snapshot consumed by Runtime. Editing only workspace.Instance
// does not change the rendered child status or its ownership gate.
func setObservedRuntimeFacts(m *Model, b *registrationWorkspaceBackend, status, ownership, authentication string) {
	for i := range b.profileBackend.snapshot.Profiles {
		if b.profileBackend.snapshot.Profiles[i].Key != m.workspace.Key {
			continue
		}
		b.profileBackend.snapshot.Profiles[i].RuntimeStatus = status
		b.profileBackend.snapshot.Profiles[i].Ownership = ownership
		b.profileBackend.snapshot.Profiles[i].AuthenticationStatus = authentication
		if ownership == "local" {
			b.profileBackend.snapshot.Profiles[i].StartDisabledReason = ""
			b.profileBackend.snapshot.Profiles[i].StopDisabledReason = ""
		}
	}
	b.capabilitySnapshot = nil
	snapshot, err := b.UICapabilityInstances(context.Background())
	if err != nil {
		panic(err)
	}
	m.capabilityInstances = snapshot
	for i := range b.profileBackend.snapshot.Profiles {
		if b.profileBackend.snapshot.Profiles[i].Key == m.workspace.Key {
			copy := b.profileBackend.snapshot.Profiles[i]
			m.workspace.Instance = &copy
			break
		}
	}
}

func clearObservedAgentRegistrations(m *Model, b *registrationWorkspaceBackend) {
	for i := range b.profileBackend.snapshot.Profiles {
		if b.profileBackend.snapshot.Profiles[i].Key == m.workspace.Key {
			b.profileBackend.snapshot.Profiles[i].RegisteredAgents = nil
		}
	}
	b.capabilitySnapshot = nil
	snapshot, _ := b.UICapabilityInstances(context.Background())
	m.capabilityInstances = snapshot
	m.workspace.Installed = m.targetHasInstallRecord(m.workspace.Key)
	m.workspace.RegisteredMCPs = m.targetRegisteredMCPs(m.workspace.Key)
}
func (*registrationWorkspaceBackend) UICreateInstance(context.Context, config.InstanceRef) error {
	// Workspace journey fixtures already contain their selected instance; none
	// of these tests exercises instance creation.
	return nil
}
func (b *registrationWorkspaceBackend) UIConfigureRegistrations(ctx context.Context, request viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	return b.profileBackend.UIConfigureRegistrations(ctx, request)
}
func (b *registrationWorkspaceBackend) CheckConnection(ctx context.Context, url, transport string) viewmodel.ConnectionObservation {
	return b.profileBackend.CheckConnection(ctx, url, transport)
}
func (b *registrationWorkspaceBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = request
	if b.previewOverride != nil {
		preview := *b.previewOverride
		preview.Key = setupProfileKey(request)
		return preview, nil
	}
	return viewmodel.SetupPreview{Key: setupProfileKey(request), PackageName: "Plain", MCP: true, HasManifestUI: true,
		Sections: []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"endpoint"}}},
		Inputs:   []viewmodel.SetupInput{{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint URI", Type: "string"}, Value: "http://127.0.0.1:8765/mcp", HasValue: true, Editable: true}}, MCPDefinitions: []catalog.MCP{{Name: "plain", Transport: "streamable-http"}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex/skills", ConfigPath: "/home/test/.codex/config.toml", Detection: "installed"}, {ID: "claude", Path: "/home/test/.claude/skills", ConfigPath: "/home/test/.claude.json", Detection: "installed"}}}, nil
}
func (b *registrationWorkspaceBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setupBackendFixture.UIInstall(ctx, request)
}
func (b *registrationWorkspaceBackend) UIInstallAgents(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setupBackendFixture.UIInstallAgents(ctx, request)
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
	return openRegistrationActionForUXWithPreview(t, nil)
}

func openRegistrationActionForUXWithPreview(t *testing.T, previewOverride *viewmodel.SetupPreview) (*Model, *registrationWorkspaceBackend) {
	t.Helper()
	m, profile := typedProfileFixture()
	backend := &registrationWorkspaceBackend{Backend: profile, profileBackend: profile, setupBackendFixture: &setupBackendFixture{}, previewOverride: previewOverride}
	m.backend = backend
	m.Update(m.load()())
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
	// Most Agent access journeys begin after the user has saved Inputs.
	preview := *m.workspace.Preview
	preview.Configured = true
	m.configureWorkspaceForm(preview)
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
			m, backend := openRegistrationActionForUX(t)
			if m.workspace.Instance == nil {
				t.Fatal("fixture must include the selected runtime observation")
			}
			authenticationStatus := ""
			switch tc.authentication {
			case "AuthenticationComplete":
				authenticationStatus = viewmodel.AuthenticationComplete
			case "AuthenticationFailed":
				authenticationStatus = viewmodel.AuthenticationFailed
			}
			setObservedRuntimeFacts(m, backend, tc.runtime, "local", authenticationStatus)
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

type uninstallRetryBackend struct {
	*registrationWorkspaceBackend
	calls []viewmodel.SetupInstallRequest
}

func (b *uninstallRetryBackend) UIInstall(_ context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.calls = append(b.calls, request)
	if len(b.calls) == 1 {
		return viewmodel.OperationResult{Step: "uninstall", Errors: []string{"permission denied"}}, errors.New("permission denied")
	}
	return viewmodel.OperationResult{Saved: true, Message: "Uninstalled"}, nil
}
func (b *uninstallRetryBackend) UIInstallAgents(_ context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.calls = append(b.calls, request)
	if len(b.calls) == 1 {
		return viewmodel.OperationResult{Step: "uninstall", Errors: []string{"permission denied"}}, errors.New("permission denied")
	}
	return viewmodel.OperationResult{Saved: true, Message: "Uninstalled"}, nil
}

func TestRetryingFailedFinalAgentUninstallPreservesUninstallIntent(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	retrying := &uninstallRetryBackend{registrationWorkspaceBackend: backend}
	m.backend = retrying
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
	_, uninstall := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "uninstall-selected"})
	if uninstall == nil {
		t.Fatal("explicit uninstall did not start")
	}
	m.Update(runTeaCmd(t, m, uninstall))
	if m.result == nil || !m.result.CanRetry || m.result.Action != "uninstall" || len(retrying.calls) != 1 || !retrying.calls[0].Uninstall || retrying.calls[0].DestinationIDs == nil || len(retrying.calls[0].DestinationIDs) != 0 {
		t.Fatalf("failed final uninstall was not retained as retryable uninstall: result=%+v calls=%+v", m.result, retrying.calls)
	}
	retry := m.retryResult()
	if retry == nil {
		t.Fatal("failed uninstall did not expose a retry operation")
	}
	m.Update(runTeaCmd(t, m, retry))
	if len(retrying.calls) != 2 || !retrying.calls[1].Uninstall || retrying.calls[1].DestinationIDs == nil || len(retrying.calls[1].DestinationIDs) != 0 || retrying.calls[1].SetupRequest.Ref.Name != "foreign" {
		t.Fatalf("retry changed explicit Uninstall intent or target: %+v", retrying.calls)
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
	m, backend := openRegistrationActionForUX(t)
	clearObservedAgentRegistrations(m, backend)
	setObservedRuntimeFacts(m, backend, "stopped", "local", "")
	if m.workspace.Instance != nil {
		m.workspace.Instance.Ownership, m.workspace.Instance.CanStart, m.workspace.Instance.RuntimeStatus = "local", true, "stopped"
	}
	m.workspace.InputsSaved = false
	m.workspace.Installed = false
	preview := *m.workspace.Preview
	preview.Configured = false
	m.configureWorkspaceForm(preview)
	m.workspace.InputsSaved = false
	m.form.FocusSection()
	for attempts := 0; m.form.SectionID() != sectionRuntimeID && attempts < 4; attempts++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.form.SectionID() != sectionRuntimeID {
		t.Fatalf("keyboard did not select Runtime: %q", m.form.SectionID())
	}
	view := m.form.View().Content
	for _, want := range []string{"Start plain", "Inputs: Not saved", "Agent install: Not installed", "Complete inputs first"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Runtime stage lacks gated Start fact %q:\n%s", want, view)
		}
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil {
		t.Fatal("runtime start was issued before inputs and agent installation")
	}
	if !strings.Contains(m.output, "Save valid inputs first") {
		t.Fatalf("missing specific unsaved-input blocker: %q", m.output)
	}
	save := m.saveSetupInputs(m.form.Values())
	if save == nil {
		t.Fatal("Inputs Save was unavailable while Start was gated")
	}
	m.Update(save())
	m.form.SelectSectionID(sectionRuntimeID)
	if m.workspace.Installed {
		t.Fatal("Inputs Save incorrectly marked the agent installed")
	}
	_, cmd = m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil || !strings.Contains(m.output, "Install this capability for an agent first") {
		t.Fatalf("Start did not identify the missing agent registration after Inputs Save: cmd=%t output=%q", cmd != nil, m.output)
	}
	backend.capabilitySnapshot = map[string]viewmodel.CapabilityInstanceSnapshot{"plain": {Instances: []viewmodel.CapabilityInstance{{
		Key:        m.workspace.Key,
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "mcp", Name: "plain", Status: "installed"}},
	}}}}
	m.capabilityInstances = backend.capabilitySnapshot
	m.workspace.Installed = m.targetHasInstallRecord(m.workspace.Key)
	m.workspace.RegisteredMCPs = map[string]bool{"plain": true}
	m.workspace.Preview.Configured = true
	m.configureWorkspaceForm(*m.workspace.Preview)
	m.workspace.InputsSaved = true
	m.form.SelectSectionID(sectionRuntimeID)
	if strings.Contains(m.form.View().Content, "Save inputs and install this capability for an agent first") {
		t.Fatal("Start remained gated after prerequisites were satisfied")
	}
	_, cmd = m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd == nil {
		t.Fatalf("observed MCP registration with saved inputs did not enable Start: %q", m.output)
	}
}

func TestRuntimeLedgerAloneDoesNotEnableMCPStart(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	clearObservedAgentRegistrations(m, backend)
	key := m.workspace.Key
	m.inventory = []state.Installation{{Key: key, AgentID: "codex", Component: "runtime", Mode: "managed"}}
	request := viewmodel.SetupRequest{Ref: m.workspace.InstanceRef}
	cmd := m.openTargetWorkspace(request, "Runtime")
	if cmd == nil {
		t.Fatal("reopening workspace did not load setup preview")
	}
	m.Update(cmd())
	if m.workspace.Installed {
		t.Fatal("runtime ledger row was treated as Agent registration")
	}
	if strings.Contains(ansi.Strip(m.form.View().Content), "Start plain ]") {
		t.Fatalf("runtime-only ledger row enabled Start:\n%s", ansi.Strip(m.form.View().Content))
	}
	_, cmd = m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil {
		t.Fatal("runtime-only ledger row dispatched Start")
	}
}

func TestDirtyInputsBlockAgentInstallAndUninstallUntilSaved(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	for i := range backend.profileBackend.snapshot.Profiles {
		if backend.profileBackend.snapshot.Profiles[i].Key.Target == m.workspace.Key.Target {
			backend.profileBackend.snapshot.Profiles[i].RegisteredAgents = []string{"codex"}
		}
	}
	m.workspace.Installed = true
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.form.ApplyValues(map[string]any{"endpoint": "http://unsaved.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	m.form.SelectSectionID(sectionAgentsID)
	// A normal UI update must refresh the selectable action state after editing.
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.form.SelectSectionID(sectionAgentsID)
	view := ansi.Strip(m.form.View().Content)
	if !strings.Contains(view, "Save changes first") {
		t.Fatalf("Agent Access did not explain its disabled state while Inputs are dirty:\n%s", view)
	}
	_, install := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if install != nil || backend.installRequest != nil || backend.inputSaveRequest != nil {
		t.Fatalf("Install proceeded while Inputs were dirty: cmd=%t install=%+v input-save=%+v", install != nil, backend.installRequest, backend.inputSaveRequest)
	}
	if !m.form.HasUnsavedChanges() || backend.installRequest != nil || backend.inputSaveRequest != nil {
		t.Fatalf("rejected Install changed the input draft or saved state: dirty=%t install=%+v save=%+v", m.form.HasUnsavedChanges(), backend.installRequest, backend.inputSaveRequest)
	}
	_, uninstall := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "uninstall-selected"})
	if uninstall == nil {
		t.Fatalf("Uninstall was unavailable while Inputs were dirty: %q", m.output)
	}
	if !m.form.HasUnsavedChanges() || backend.inputSaveRequest != nil {
		t.Fatalf("Uninstall saved or discarded the unsaved Inputs draft: dirty=%t save=%+v", m.form.HasUnsavedChanges(), backend.inputSaveRequest)
	}
	m.Update(runTeaCmd(t, m, uninstall))
	if backend.installAgentsRequest == nil || !backend.installAgentsRequest.Uninstall || backend.inputSaveRequest != nil {
		t.Fatalf("Uninstall did not preserve its exact intent while Inputs were dirty: request=%+v save=%+v", backend.installAgentsRequest, backend.inputSaveRequest)
	}
	save := m.saveSetupInputs(m.form.Values())
	if save == nil {
		t.Fatal("Inputs Save was unavailable")
	}
	m.Update(save())
	if backend.inputSaveRequest == nil || backend.inputSaveRequest.Inputs["endpoint"] != "http://unsaved.example/mcp" {
		t.Fatalf("explicit Inputs Save did not persist the edited endpoint: %+v", backend.inputSaveRequest)
	}
	if err := m.form.ApplyValues(map[string]any{m.pendingSetupField: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if cmd == nil {
		t.Fatalf("Install remained blocked after explicit Save: %q", m.output)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.installRequest == nil || backend.installRequest.Inputs["endpoint"] != "http://unsaved.example/mcp" {
		t.Fatalf("saved inputs were not used by subsequent Install: %+v", backend.installRequest)
	}
}

func TestUXRuntimeStartIsBlockedWhenSavedInputsHaveChanged(t *testing.T) {
	preview := &viewmodel.SetupPreview{PackageName: "Plain", MCP: true, HasManifestUI: true, Configured: true,
		Sections:          []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"port"}}},
		Inputs:            []viewmodel.SetupInput{{Definition: catalog.Input{Name: "port", Label: "Local port", Type: "integer", Default: 8765}, Value: 8765, HasValue: true, Editable: true}},
		MCPDefinitions:    []catalog.MCP{{Name: "plain", Transport: "streamable-http", HostPortInput: "port", EndpointPath: "/mcp", TokenInput: "token"}},
		MCPDefinitionKeys: map[string]state.Key{"plain": {Source: "team-source", Package: "plain", Target: "foreign", MCPDefinition: "plain"}},
		Destinations:      []viewmodel.SetupDestination{{ID: "codex", ConfigPath: "/home/test/.codex/config.toml", Detection: "installed"}}}
	m, backend := openRegistrationActionForUXWithPreview(t, preview)
	backend.capabilitySnapshot = map[string]viewmodel.CapabilityInstanceSnapshot{"plain": {Instances: []viewmodel.CapabilityInstance{{
		Ref: m.workspace.InstanceRef, Key: m.workspace.Key,
		CompletionScopeKnown: true, CompletionScopeRelevant: true,
		SelectedComponents:   []viewmodel.SelectedComponent{{Kind: "mcp", Name: "plain"}},
		SelectedDestinations: []string{"codex"}, EligibleDestinations: []string{"codex"},
		MCPs:       []viewmodel.RuntimeStatus{{MCPID: "plain", Status: "stopped", Ownership: "local", URL: "http://localhost:8765/mcp"}},
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "mcp", Name: "plain", Status: "installed", Managed: true}},
	}}}}
	m.Update(m.load()())
	m.inventory = []state.Installation{{Key: state.Key{Source: m.workspace.Key.Source, Package: m.workspace.Key.Package, Environment: m.workspace.Key.Environment, Target: m.workspace.Key.Target, MCPDefinition: "plain"}, AgentID: "codex", Component: "mcp", RegistrationName: "plain", URL: "http://127.0.0.1:8765/mcp"}}
	m.workspace.InputsSaved = true
	m.workspace.Installed = m.targetHasInstallRecord(m.workspace.Key)
	m.workspace.RegisteredMCPs = m.targetRegisteredMCPs(m.workspace.Key)
	if m.workspace.Instance == nil {
		t.Fatal("fixture lacks runtime instance")
	}
	m.workspace.Instance.Ownership = "local"
	m.workspace.Instance.CanStart = true
	m.workspace.Instance.RuntimeStatus = "stopped"
	if reason := m.mcpRegistrationAddressReason("plain"); reason != "" {
		t.Fatalf("equivalent loopback spelling should preserve the existing binding: %q", reason)
	}
	if sameMCPRegistrationURL("http://[::1]:8765/mcp", "http://localhost:8765/mcp") {
		t.Fatal("IPv6 loopback must not be assumed equivalent to the local IPv4 binding")
	}
	if err := m.form.ApplyValues(map[string]any{"port": 8766}); err != nil {
		t.Fatal(err)
	}
	// A normal form event refreshes the Runtime action reason from the dirty
	// Inputs draft; ApplyValues above seeds the changed value deterministically.
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	if !strings.Contains(m.form.View().Content, "Save changes first") {
		t.Fatalf("Runtime did not show the changed-input gate:\n%s", m.form.View().Content)
	}
	_, cmd := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:plain"})
	if cmd != nil || backend.runtimeAction != "" {
		t.Fatalf("Start ran with an unsaved input draft: cmd=%t action=%q output=%q", cmd != nil, backend.runtimeAction, m.output)
	}
	if !strings.Contains(m.output, "Save changed inputs first") {
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
	if !m.workspace.InputsSaved || !m.workspace.Installed || !m.workspaceMCPRegistered("plain") {
		t.Fatalf("saving changed inputs must preserve the observed installation fact: saved=%t installed=%t registered=%t", m.workspace.InputsSaved, m.workspace.Installed, m.workspaceMCPRegistered("plain"))
	}
	m.form.SelectSectionID(sectionRuntimeID)
	if reason := m.runtimeStartDisabledReason("plain", InstanceRow{Key: m.workspace.Key, Instance: m.workspace.Instance}); !strings.Contains(reason, "http://127.0.0.1:8765/mcp") || !strings.Contains(reason, "http://localhost:8766/mcp") || !strings.Contains(reason, "Install") {
		t.Fatalf("Runtime did not explain the stale local MCP address and required Install: %q", reason)
	}
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
	_, refresh := m.Update(runTeaCmd(t, m, cmd))
	if refresh != nil {
		m.Update(refresh())
	}
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

func TestDefaultedInputWithoutSavedOverrideIsNotDirty(t *testing.T) {
	m, _ := openRegistrationActionForUX(t)
	preview := *m.workspace.Preview
	preview.Inputs = append(preview.Inputs, viewmodel.SetupInput{
		Definition: catalog.Input{Name: "local_port", Label: "Local port", Type: "integer", Default: 18765},
		Editable:   true,
		HasValue:   false,
	})
	m.workspace.Preview = &preview
	m.pendingSetup = &preview
	if m.hasUnsavedSetupInputs() {
		t.Fatal("a definition default without a saved answer was treated as an unsaved edit")
	}
}

func TestUXRuntimeStartsAfterInputsSavedAndAgentInstall(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	setObservedRuntimeFacts(m, backend, "stopped", "local", "")
	m.workspace.InputsSaved, m.workspace.Installed = true, true
	m.workspace.RegisteredMCPs = map[string]bool{"plain": true}
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
		m, backend := openRegistrationActionForUX(t)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		preview := *m.workspace.Preview
		preview.Configured = true
		m.workspace.Installed = true
		setObservedRuntimeFacts(m, backend, "stopped", "local", "")
		if m.workspace.Instance != nil {
			m.workspace.Instance.CanStart = true
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
