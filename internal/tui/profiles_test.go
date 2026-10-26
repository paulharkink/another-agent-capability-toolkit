package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type profileBackend struct {
	snapshotErr error
	fixtureBackend
	snapshot     viewmodel.InstanceSnapshot
	request      *viewmodel.RegistrationRequest
	legacyCalled bool
	result       viewmodel.OperationResult
	err          error
	checkedURL   string
	checkedType  string
	connection   viewmodel.ConnectionObservation
}

func (b *profileBackend) UICatalog(context.Context) ([]catalog.Package, error) {
	return []catalog.Package{{ID: "plain", Name: "Plain", Dir: "/catalog/plain", MCP: &catalog.MCP{Transport: "streamable-http"}, Skill: &catalog.Skill{Name: "companion"}}}, nil
}
func (b *profileBackend) UIMCPs(context.Context) ([]mcp.Instance, error) {
	b.legacyCalled = true
	return nil, errors.New("legacy inventory must not be used")
}
func (b *profileBackend) UIProfileSnapshot(context.Context) (viewmodel.InstanceSnapshot, error) {
	return b.snapshot, b.snapshotErr
}
func (b *profileBackend) UIConfigureRegistrations(_ context.Context, q viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	b.request = &q
	return b.result, b.err
}
func (b *profileBackend) CheckConnection(_ context.Context, url, transport string) viewmodel.ConnectionObservation {
	b.checkedURL, b.checkedType = url, transport
	if b.connection.URL != "" {
		return b.connection
	}
	return viewmodel.ConnectionObservation{URL: url, Reachable: true, CheckedAt: time.Now().UTC()}
}

// capabilityProfileBackend exercises the complete manifest-backed setup form
// for profile actions. Registrations are part of the full capability save.
type capabilityProfileBackend struct {
	Backend
	profile *profileBackend
	setup   *setupBackendFixture
	preview viewmodel.SetupPreview
}

func (b *capabilityProfileBackend) UICatalog(ctx context.Context) ([]catalog.Package, error) {
	return b.profile.UICatalog(ctx)
}
func (b *capabilityProfileBackend) UIMCPs(ctx context.Context) ([]mcp.Instance, error) {
	return b.profile.UIMCPs(ctx)
}
func (b *capabilityProfileBackend) UIProfileSnapshot(ctx context.Context) (viewmodel.InstanceSnapshot, error) {
	return b.profile.UIProfileSnapshot(ctx)
}
func (b *capabilityProfileBackend) CheckConnection(ctx context.Context, url, transport string) viewmodel.ConnectionObservation {
	return b.profile.CheckConnection(ctx, url, transport)
}
func (b *capabilityProfileBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	preview := b.preview
	preview.Key = state.Key{Source: request.Ref.PackID, Package: request.Ref.CapabilityID, Target: request.Ref.Name}
	return preview, nil
}
func (b *capabilityProfileBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setup.UIInstall(ctx, request)
}
func (b *capabilityProfileBackend) UIInstallAgents(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setup.UIInstallAgents(ctx, request)
}

func capabilityProfilePreview() viewmodel.SetupPreview {
	return viewmodel.SetupPreview{
		PackageName: "Plain", HasManifestUI: true,
		Sections: []catalog.Section{{ID: "endpoint", Title: "Endpoint", Fields: []string{"endpoint"}}},
		Inputs:   []viewmodel.SetupInput{{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint URI", Type: "string"}, Value: "http://127.0.0.1:8765/mcp", HasValue: true, Editable: true}},
		MCP:      true, MCPDefinitions: []catalog.MCP{{Name: "plain", Transport: "streamable-http"}},
		Destinations: []viewmodel.SetupDestination{
			{ID: "codex", ConfigPath: "/home/test/.codex/config.toml"},
			{ID: "claude", ConfigPath: "/home/test/.claude.json", Selected: true},
		},
	}
}

func openCapabilityProfileWorkspace(t *testing.T, m *Model, profile *profileBackend, target, section string) *capabilityProfileBackend {
	t.Helper()
	setup := &setupBackendFixture{}
	backend := &capabilityProfileBackend{Backend: profile, profile: profile, setup: setup, preview: capabilityProfilePreview()}
	for _, observed := range profile.snapshot.Profiles {
		if observed.Key.Target != target {
			continue
		}
		known := map[string]bool{"codex": true, "claude": true}
		for _, id := range observed.RegisteredAgents {
			if !known[id] {
				backend.preview.Destinations = append(backend.preview.Destinations, viewmodel.SetupDestination{ID: id, ConfigPath: "/home/test/" + id + ".json", Selected: true})
			}
		}
		break
	}
	m.backend = backend
	m.Update(m.load()())
	request := viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: "team-source", CapabilityID: "plain", Name: target}}
	cmd := m.openTargetWorkspace(request, section)
	if cmd == nil {
		t.Fatalf("could not open complete capability workspace for target %q", target)
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || m.workspace.Key.Target != target {
		t.Fatalf("complete capability workspace failed to load: %s", m.View().Content)
	}
	return backend
}

func TestCheckConnectionActionObservesSelectedEndpoint(t *testing.T) {
	m, b := typedProfileFixture()
	backend := openCapabilityProfileWorkspace(t, m, b, "foreign", "Runtime")
	selectWorkspaceSectionByKeyboard(t, m, sectionRuntimeID)
	message := activateWorkspaceActionByKeyboard(t, m, "Check plain connection")
	_, routed := m.Update(message)
	if routed == nil {
		t.Fatal("Runtime child connection action did not dispatch a check")
	}
	m.Update(runTeaCmd(t, m, routed))
	if b.checkedURL != "http://127.0.0.1:8765/mcp" || b.checkedType != "streamable-http" || b.request != nil {
		t.Fatalf("wrong connection observation: url=%q transport=%q legacy registration=%+v", b.checkedURL, b.checkedType, b.request)
	}
	if m.result == nil || !strings.Contains(strings.Join(m.result.Rows, "\n"), "reachable") {
		t.Fatalf("connection observation was not surfaced as a foreground result: %+v", m.result)
	}
	_ = backend
}
func typedProfileFixture() (*Model, *profileBackend) {
	b := &profileBackend{snapshot: viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{
		{Key: state.Key{Source: "team-source", Package: "plain", Target: "saved"}, Name: "saved", RuntimeStatus: "never-started", Ownership: "local", CanStart: true},
		{Key: state.Key{Source: "team-source", Package: "plain", Target: "foreign"}, Name: "foreign", RuntimeStatus: "running", Ownership: "other-aact", URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", RegisteredAgents: []string{"claude"}, CanConfigureRegistrations: true, StartDisabledReason: "Owned by another installation", StopDisabledReason: "Owned by another installation"},
		{Key: state.Key{Source: "another-source", Package: "plain", Target: "unrelated"}, RuntimeStatus: "running"},
	}}}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	return m, b
}
func openProfileAction(t *testing.T, m *Model, label string, mouse bool) tea.Cmd {
	t.Helper()
	_ = mouse // Profile actions now use the target workspace's visible controls.
	rows := m.profiles()
	if len(rows) == 0 {
		t.Fatal("profile target list is empty")
	}
	index := m.home.Profiles.Index
	if index < 0 || index >= len(rows) {
		index = 0
	}
	profile := rows[index]
	b, ok := m.backend.(*profileBackend)
	if !ok {
		t.Fatalf("expected profile fixture backend, got %T", m.backend)
	}
	m.backend = &registrationWorkspaceBackend{Backend: b, profileBackend: b, setupBackendFixture: &setupBackendFixture{}}
	m.selectPane(ProfilesPane, index)
	cmd := m.openTargetWorkspace(m.packInstanceRequest(profile.Key), "Overview")
	if cmd == nil {
		t.Fatalf("target workspace did not open for exact key %+v", profile.Key)
	}
	m.Update(cmd())
	if m.workspace == nil || m.workspace.Key != profile.Key {
		t.Fatalf("workspace changed selected profile identity: got %+v want %+v", m.workspace, profile.Key)
	}
	switch label {
	case "Configure agent registrations":
		openCapabilityProfileWorkspace(t, m, b, profile.Key.Target, "Agents")
		return nil
	case "Check connection":
		openCapabilityProfileWorkspace(t, m, b, profile.Key.Target, "Runtime")
		m.form.SelectSectionID(sectionRuntimeID)
		m.form.FocusSection()
		press(m, tea.KeyRight, "")
		press(m, tea.KeyDown, "")
		press(m, tea.KeyDown, "")
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		return cmd
	case "View details":
		m.form.SelectSectionID(sectionInformationID)
		m.form.FocusSection()
		return nil
	case "Start":
		_, cmd := m.workspaceOverviewAction("s")
		return cmd
	case "Stop", "Restart":
		_, cmd := m.workspaceOverviewAction("x")
		return cmd
	default:
		t.Fatalf("unmapped target workspace action %q", label)
		return nil
	}
}

func openRegistrationWorkspaceAction(t *testing.T, m *Model, removing bool) {
	t.Helper()
	profileBackend, ok := m.backend.(*profileBackend)
	if !ok {
		t.Fatalf("expected profile fixture backend, got %T", m.backend)
	}
	_ = removing // Complete-capability binding uses one desired destination set.
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openCapabilityProfileWorkspace(t, m, profileBackend, "foreign", "Agents")
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	m.workspace.InputsSaved = true
	press(m, tea.KeyRight, "")
}
func TestTypedProfilesIncludeSavedAndForeignRows(t *testing.T) {
	m, b := typedProfileFixture()
	if b.legacyCalled {
		t.Error("typed backend also queried legacy MCP inventory")
	}
	rows := m.profiles()
	if len(rows) != 2 {
		t.Fatalf("expected only related saved and foreign profiles, got %#v", rows)
	}
	if rows[1].Instance.Ownership != "other-aact" {
		t.Fatalf("foreign ownership was lost: %#v", rows[1].Instance)
	}
	view := m.View().Content
	for _, word := range []string{"never-started", "running"} {
		if !strings.Contains(view, word) {
			t.Errorf("missing %s: %s", word, view)
		}
	}
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	id := m.home.Profiles.ID
	b.snapshot.Profiles[1].Name = "renamed runtime"
	b.snapshot.ObservationStale = true
	b.snapshot.DockerError = "daemon unavailable"
	b.snapshot.ObservedAt = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	m.Update(m.load()())
	if m.home.Profiles.ID != id {
		t.Error("display name changed stable profile identity")
	}
	view = m.View().Content
	if !strings.Contains(view, "daemon unavailable") || !strings.Contains(view, "2026-10-02") {
		t.Errorf("missing stale observation evidence: %s", view)
	}
}
func TestConfigureRegistrationsUsesTypedRequest(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyboard", true: "mouse"}[mouse], func(t *testing.T) {
			m, b := typedProfileFixture()
			openRegistrationWorkspaceAction(t, m, false)
			backend := m.backend.(*capabilityProfileBackend)
			if got := m.form.Values()[m.pendingSetupField]; !containsStringFromValue(got, "claude") {
				t.Fatalf("existing Claude destination not selected: %#v", got)
			}
			if strings.Contains(m.View().Content, "All —") {
				t.Fatalf("MCP binding exposes the global All destination:\n%s", m.View().Content)
			}
			// Add Codex while keeping the previously selected Claude binding.
			press(m, tea.KeySpace, " ")
			backend.setup.installResult = &viewmodel.OperationResult{Message: "Binding update completed with errors", Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}}, Errors: []string{"claude: endpoint rejected"}}
			var action tea.Msg
			if mouse {
				action = clickFormAction(t, m, "Install selected")
			} else {
				action = activateWorkspaceActionByKeyboard(t, m, "Install selected")
			}
			_, cmd := m.Update(action)
			if cmd == nil || !m.busy {
				t.Fatal("Install selected did not schedule the complete binding")
			}
			if !m.busy {
				t.Error("Agent access install did not start its foreground operation")
			}
			m.Update(runTeaCmd(t, m, cmd))
			request := backend.setup.installAgentsRequest
			if request == nil {
				t.Fatal("typed agent install backend not called")
			}
			want := b.snapshot.Profiles[1]
			if request.SetupRequest.Ref.PackID != want.Key.Source || request.SetupRequest.Ref.CapabilityID != want.Key.Package || request.SetupRequest.Ref.Name != want.Key.Target || !reflect.DeepEqual(request.DestinationIDs, []string{"claude", "codex"}) {
				t.Fatalf("wrong complete binding request: got %#v want target=%q destinations=%v", request, want.Key.Target, []string{"claude", "codex"})
			}
			if !strings.Contains(m.output, "codex: mcp configured") || !strings.Contains(m.output, "claude: endpoint rejected") {
				t.Fatalf("missing per-agent results: %s", m.output)
			}
			if m.home.Focus != ProfilesPane {
				t.Error("operation lost profile pane focus")
			}
		})
	}
}
func TestTypedProfileActionsRespectDisabledReasons(t *testing.T) {
	for _, action := range []string{"Restart", "Stop"} {
		m, _ := typedProfileFixture()
		m.focusPane(ProfilesPane)
		m.selectPane(ProfilesPane, 1)
		cmd := openProfileAction(t, m, action, false)
		if cmd != nil || m.busy || !strings.Contains(m.output, "Owned by another installation") {
			t.Fatalf("foreign action enabled or reason lost: %s", m.output)
		}
	}
	m, b := typedProfileFixture()
	b.snapshot.Profiles[1].Transport = ""
	m.Update(m.load()())
	openCapabilityProfileWorkspace(t, m, b, "foreign", "Runtime")
	selectWorkspaceSectionByKeyboard(t, m, sectionRuntimeID)
	check := activateWorkspaceActionByKeyboard(t, m, "Check plain connection")
	_, cmd := m.Update(check)
	if cmd == nil {
		t.Fatalf("known endpoint with unknown observed transport should remain diagnosable:\n%s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, cmd))
	if b.checkedURL != "http://127.0.0.1:8765/mcp" {
		t.Fatalf("connection check lost the known endpoint: %q", b.checkedURL)
	}
}
func TestHomeLegacyUninstallShortcutIsDisabled(t *testing.T) {
	m := fixtureModel(t)
	focusFixtureProfile(m)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if cmd != nil || m.form != nil || m.pending.action == "uninstall" {
		t.Fatal("hidden profile shortcut opened full uninstall")
	}
}

func TestRegistrationCancelRestoresActions(t *testing.T) {
	m, b := typedProfileFixture()
	openRegistrationWorkspaceAction(t, m, false)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil || m.form == nil || m.workspace == nil || !m.workspace.Active || b.request != nil {
		t.Fatal("Escape from the Agents details did not return to the workspace section list")
	}
}
func TestRegistrationUninstallRemovesSelectedAgent(t *testing.T) {
	m, _ := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openRegistrationWorkspaceAction(t, m, false)
	backend := m.backend.(*capabilityProfileBackend)
	if got := m.form.Values()[m.pendingSetupField]; !containsStringFromValue(got, "claude") {
		t.Fatal("saved Claude binding was not selected before editing")
	}
	if strings.Contains(m.View().Content, "All —") {
		t.Fatal("MCP binding exposed the global All destination")
	}
	action := activateWorkspaceActionByKeyboard(t, m, "Uninstall selected")
	_, cmd := m.Update(action)
	if cmd == nil || !m.busy {
		t.Fatal("Uninstall selected must submit removal of the checked agent")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.setup.installAgentsRequest == nil || !backend.setup.installAgentsRequest.Uninstall || len(backend.setup.installAgentsRequest.DestinationIDs) != 0 {
		t.Fatalf("wrong exact uninstall request: %#v", backend.setup.installAgentsRequest)
	}
}

func TestRemoveRegistrationsStartsUnselectedAndRemovesOnlySelectedAgents(t *testing.T) {
	m, _ := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openRegistrationWorkspaceAction(t, m, true)
	backend := m.backend.(*capabilityProfileBackend)
	if got := m.form.Values()[m.pendingSetupField]; !containsStringFromValue(got, "claude") {
		t.Fatal("recorded Claude binding was not selected before editing")
	}
	action := clickFormAction(t, m, "Uninstall selected")
	_, cmd := m.Update(action)
	if cmd == nil {
		t.Fatal("selected removal was not submitted")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.setup.installAgentsRequest == nil || !backend.setup.installAgentsRequest.Uninstall || containsString(backend.setup.installAgentsRequest.DestinationIDs, "claude") {
		t.Fatalf("selected removal did not remove the Claude binding: %+v", backend.setup.installAgentsRequest)
	}
}
func TestRegistrationPreservesUnavailableRegisteredAgent(t *testing.T) {
	m, b := typedProfileFixture()
	b.snapshot.Profiles[1].RegisteredAgents = append(b.snapshot.Profiles[1].RegisteredAgents, "opencode:old")
	m.Update(m.load()())
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openRegistrationWorkspaceAction(t, m, false)
	if got := m.form.Values()[m.pendingSetupField]; !containsStringFromValue(got, "opencode:old") || !strings.Contains(m.View().Content, "opencode:old") {
		t.Fatal("unavailable existing binding was silently removed from the desired selection")
	}
	action := activateWorkspaceActionByKeyboard(t, m, "Install selected")
	_, cmd := m.Update(action)
	if cmd == nil {
		t.Fatal("prefilled registration selection could not install")
	}
	m.Update(runTeaCmd(t, m, cmd))
	backend := m.backend.(*capabilityProfileBackend)
	if backend.setup.installAgentsRequest == nil || !reflect.DeepEqual(backend.setup.installAgentsRequest.DestinationIDs, []string{"claude", "opencode:old"}) {
		t.Fatalf("existing binding lost: %#v", backend.setup.installAgentsRequest)
	}
}
func TestRegistrationConnectionObservationIsVisible(t *testing.T) {
	for _, reachable := range []bool{true, false} {
		m, b := typedProfileFixture()
		m.focusPane(ProfilesPane)
		m.selectPane(ProfilesPane, 1)
		b.connection = viewmodel.ConnectionObservation{URL: b.snapshot.Profiles[1].URL, Reachable: reachable, Error: "connection refused"}
		if reachable {
			b.connection.Error = ""
		}
		openCapabilityProfileWorkspace(t, m, b, "foreign", "Runtime")
		selectWorkspaceSectionByKeyboard(t, m, sectionRuntimeID)
		message := activateWorkspaceActionByKeyboard(t, m, "Check plain connection")
		_, run := m.Update(message)
		if run == nil {
			t.Fatal("connection check did not start an observation")
		}
		m.Update(runTeaCmd(t, m, run))
		want := "connection refused"
		if reachable {
			want = "reachable"
		}
		if m.result == nil || !strings.Contains(strings.Join(m.result.Rows, "\n"), want) {
			t.Fatalf("connection result missing: %+v", m.result)
		}
	}
}
func TestProfileDetailsSeparateLiveStatusFromLocalLastAction(t *testing.T) {
	m, b := typedProfileFixture()
	p := &b.snapshot.Profiles[1]
	p.RuntimeStatus = "conflict"
	p.LocalLastAction = "stop"
	p.LocalLastActionAt = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	p.RegistrationDisabledReason = "Multiple runtime endpoints"
	p.CanConfigureRegistrations = false
	m.Update(m.load()())
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openProfileAction(t, m, "View details", false)
	for _, word := range []string{"conflict", "Local last action: stop", "2026-10-02", "Multiple runtime endpoints"} {
		if !strings.Contains(m.View().Content, word) {
			t.Errorf("missing detail %s: %s", word, m.View().Content)
		}
	}
}
func TestSnapshotFailurePreservesRowsAndDisablesRuntime(t *testing.T) {
	m, b := typedProfileFixture()
	b.snapshotErr = errors.New("profile store unavailable")
	m.Update(m.load()())
	if len(m.profiles()) != 2 {
		t.Fatal("failed refresh erased known profiles")
	}
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 0)
	cmd := openProfileAction(t, m, "Start", false)
	if cmd != nil || m.busy || !strings.Contains(m.output, "profile store unavailable") {
		t.Fatalf("failed refresh left runtime actions enabled: %s", m.output)
	}
}

func TestForeignSourceCapabilityRemainsSelectableAndRegistrable(t *testing.T) {
	m, b := typedProfileFixture()
	foreign := &b.snapshot.Profiles[2]
	foreign.Ownership = "other-aact"
	foreign.Name = "Remote inspector"
	foreign.URL = "http://127.0.0.1:8080/mcp"
	foreign.Transport = "streamable-http"
	foreign.CanConfigureRegistrations = true
	m.Update(m.load()())
	rows := m.capabilities()
	for _, row := range rows {
		if row.Source != "team-source" {
			t.Fatalf("inactive Pack capability leaked into the selected Pack catalog: %#v", row)
		}
	}
	for _, row := range m.contextRows() {
		if row.Key.Source == "another-source" {
			t.Fatalf("inactive Pack runtime leaked into selected Pack navigation: %#v", row)
		}
	}
}

func TestLegacyMCPViewDoesNotAdvertiseUninstall(t *testing.T) {
	m := fixtureModel(t)
	m.view = "MCPs"
	if strings.Contains(m.View().Content, "u uninstall") || strings.Contains(m.View().Content, "u unregister") {
		t.Fatal("legacy MCP view advertises destructive shortcut")
	}
}
