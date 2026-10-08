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
	snapshot     viewmodel.ProfileSnapshot
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
func (b *profileBackend) UIProfileSnapshot(context.Context) (viewmodel.ProfileSnapshot, error) {
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
func (b *capabilityProfileBackend) UIProfileSnapshot(ctx context.Context) (viewmodel.ProfileSnapshot, error) {
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
	request := viewmodel.SetupRequest{Ref: config.ProfileRef{PackID: "team-source", CapabilityID: "plain", Name: target}}
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
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	press(m, tea.KeyRight, "")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyDown, "")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Runtime child connection action was not keyboard reachable:\n%s", m.View().Content)
	}
	_, routed := m.Update(cmd())
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
	b := &profileBackend{snapshot: viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{
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
	cmd := m.openTargetWorkspace(m.packProfileRequest(profile.Key), "Overview")
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
		m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
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
	if rows[1].Profile.Ownership != "other-aact" {
		t.Fatalf("foreign ownership was lost: %#v", rows[1].Profile)
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
			_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if cmd == nil || !m.busy {
				t.Fatal("Save did not schedule complete binding")
			}
			if !m.busy || m.form != nil {
				t.Error("binding save did not transition into its foreground operation")
			}
			m.Update(runTeaCmd(t, m, cmd))
			request := backend.setup.installRequest
			if request == nil {
				t.Fatal("typed capability install backend not called")
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
	if !strings.Contains(m.View().Content, "Runtime") || !strings.Contains(m.View().Content, "Check plain connection") {
		t.Fatalf("known endpoint with unknown observed transport should remain diagnosable:\n%s", m.View().Content)
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
func TestRegistrationDeselectionAppliesEmptyDesiredSet(t *testing.T) {
	m, _ := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openRegistrationWorkspaceAction(t, m, false)
	backend := m.backend.(*capabilityProfileBackend)
	if got := m.form.Values()[m.pendingSetupField]; !containsStringFromValue(got, "claude") {
		t.Fatal("saved Claude binding was not selected before editing")
	}
	press(m, tea.KeyDown, "") // Claude row.
	press(m, tea.KeySpace, " ")
	if strings.Contains(m.View().Content, "All —") {
		t.Fatal("MCP binding exposed the global All destination")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || m.form != nil || !m.busy {
		t.Fatal("empty desired selection must submit complete capability unbinding")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.setup.installRequest == nil || len(backend.setup.installRequest.DestinationIDs) != 0 {
		t.Fatalf("wrong complete desired set: %#v", backend.setup.installRequest)
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
	press(m, tea.KeyDown, "")
	press(m, tea.KeySpace, " ")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("selected removal was not submitted")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.setup.installRequest == nil || containsString(backend.setup.installRequest.DestinationIDs, "claude") {
		t.Fatalf("complete desired set kept the removed Claude binding: %+v", backend.setup.installRequest)
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
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("prefilled registration selection could not submit")
	}
	m.Update(runTeaCmd(t, m, cmd))
	backend := m.backend.(*capabilityProfileBackend)
	if backend.setup.installRequest == nil || !reflect.DeepEqual(backend.setup.installRequest.DestinationIDs, []string{"claude", "opencode:old"}) {
		t.Fatalf("existing binding lost: %#v", backend.setup.installRequest)
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
		m.form.SelectSectionID(sectionRuntimeID)
		m.form.FocusSection()
		press(m, tea.KeyRight, "")
		press(m, tea.KeyDown, "")
		press(m, tea.KeyDown, "")
		_, cmd := m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("no connection check action")
		}
		_, run := m.Update(cmd())
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
	if len(rows) != 2 {
		t.Fatalf("foreign capability missing: %#v", rows)
	}
	m.selectPane(CapabilitiesPane, 1)
	if rows[1].Source != "another-source" || rows[1].CatalogIndex >= 0 {
		t.Fatalf("wrong synthetic identity: %#v", rows[1])
	}
	if !strings.Contains(m.View().Content, "another-source") {
		t.Fatal("foreign source label is missing")
	}
	id := m.home.Capabilities.ID
	for _, action := range []string{"parameters", "i", "a", "s"} {
		cmd := m.homeOperation(action)
		if cmd != nil || m.form != nil || m.busy || !strings.Contains(m.output, "active catalog") {
			t.Fatalf("foreign capability %s attempted local package operation", action)
		}
	}
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 0)
	foreignKey := b.snapshot.Profiles[2].Key
	profileAdapter := &registrationWorkspaceBackend{Backend: b, profileBackend: b, setupBackendFixture: &setupBackendFixture{}}
	m.backend = profileAdapter
	selected := false
	for index, row := range m.contextRows() {
		if row.Kind == "profile" && row.Key == foreignKey {
			m.selectContext(index)
			selected = true
			break
		}
	}
	if !selected {
		t.Fatal("foreign profile did not remain selectable")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form == nil || !m.workspace.ObservedOnly || !strings.Contains(strings.ToLower(m.View().Content), "locate capability pack") {
		t.Fatalf("unknown catalog source incorrectly exposed local capability binding controls: workspace=%+v\n%s", m.workspace, m.View().Content)
	}
	press(m, tea.KeyEscape, "")
	b.snapshot.Profiles[2].Name = "Remote renamed"
	m.Update(m.load()())
	if m.home.Capabilities.ID != id {
		t.Fatal("synthetic capability identity changed on refresh")
	}
}

func TestLegacyMCPViewDoesNotAdvertiseUninstall(t *testing.T) {
	m := fixtureModel(t)
	m.view = "MCPs"
	if strings.Contains(m.View().Content, "u uninstall") || strings.Contains(m.View().Content, "u unregister") {
		t.Fatal("legacy MCP view advertises destructive shortcut")
	}
}
