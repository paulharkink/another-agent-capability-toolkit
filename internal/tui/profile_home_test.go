package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"strings"
	"testing"
)

type packProfileBackend struct {
	setupBackendFixture
	profiles map[string]viewmodel.CapabilityProfileSnapshot
	created  []config.InstanceRef
}

func (b *packProfileBackend) UICapabilityProfiles(context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error) {
	return b.profiles, nil
}
func (b *packProfileBackend) UICreateProfile(_ context.Context, ref config.InstanceRef) error {
	b.created = append(b.created, ref)
	return nil
}
func (b *packProfileBackend) UISetupPreview(_ context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.previewRequest = q
	return viewmodel.SetupPreview{Key: state.Key{Source: q.Ref.PackID, Package: q.Ref.CapabilityID, Target: q.Ref.Name}, PackRoot: "/pack", PackageName: q.Ref.CapabilityID}, nil
}
func profileHomeFixture(t *testing.T) (*Model, *packProfileBackend) {
	t.Helper()
	m, msg := homeFixture()
	b := &packProfileBackend{profiles: map[string]viewmodel.CapabilityProfileSnapshot{"inspect": {Profiles: []viewmodel.CapabilityProfile{{Ref: config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "ota"}, Key: state.Key{Source: "one", Package: "inspect", Target: "ota"}, ConfigStatus: "configured", MCPs: []viewmodel.RuntimeStatus{{MCPID: "server", Status: "stopped", Ownership: "local"}}}, {Ref: config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "prod"}, Key: state.Key{Source: "one", Package: "inspect", Target: "prod"}, ConfigStatus: "needs-input"}}}, "plain": {}}}
	m.backend = b
	msg.capabilityInstances = b.profiles
	m.Update(msg)
	return m, b
}
func TestProfileHomeImmediateProfilesAndLayerNavigation(t *testing.T) {
	m, b := profileHomeFixture(t)
	rows := m.contextRows()
	if len(rows) != 4 || rows[0].Kind != "profile" || !strings.Contains(rows[0].Label, "ota") || !strings.Contains(rows[0].Label, "stopped") {
		t.Fatalf("%+v", rows)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || m.home.Focus != ProfilesPane || m.home.Modal != nil {
		t.Fatal("left Enter bypassed right pane")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("profile Enter did not load configuration")
	}
	m.Update(cmd())
	if b.previewRequest.Ref.Name != "ota" || b.previewRequest.Environment != "" || b.previewRequest.Target != "" {
		t.Fatalf("%+v", b.previewRequest)
	}
	if m.form == nil {
		t.Fatal("profile form absent")
	}
}

func TestPackWorkspaceDoesNotTreatConfigSummaryAsRuntime(t *testing.T) {
	m, _ := profileHomeFixture(t)
	row := m.profiles()[0]
	runtime := m.instanceForWorkspace(row.Key)
	if runtime == nil || runtime.RuntimeStatus != "stopped" || runtime.Ownership != "local" {
		t.Fatalf("configuration row used as runtime: %+v", runtime)
	}
	m.home.Focus = ProfilesPane
	detail := m.selectedDetail()
	if strings.Contains(detail, " /  / ") || strings.Contains(detail, "owner: ") {
		t.Fatalf("legacy empty scope shown: %s", detail)
	}
}

func TestPackWorkspaceMergesOnlyExactSavedRegistrationFacts(t *testing.T) {
	m, _ := profileHomeFixture(t)
	first := state.Key{Source: "one", Package: "inspect", Target: "ota"}
	other := state.Key{Source: "one", Package: "inspect", Target: "prod"}
	m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{
		{Key: first, Name: "ota", RuntimeStatus: "never-started", Ownership: "local", URL: "http://127.0.0.1:8765/ota", RegisteredAgents: []string{"codex"}, CanStart: true},
		{Key: other, Name: "prod", RuntimeStatus: "never-started", Ownership: "local", URL: "http://127.0.0.1:8765/prod", RegisteredAgents: []string{"claude"}},
	}}
	got := m.instanceForWorkspace(first)
	if got == nil || got.URL != "http://127.0.0.1:8765/ota" || strings.Join(got.RegisteredAgents, ",") != "codex" {
		t.Fatalf("exact saved registration facts were lost: %+v", got)
	}
	if got.RuntimeStatus != "never-started" || got.Ownership != "local" || !got.CanStart {
		t.Fatalf("saved registration was mistaken for a live runtime: %+v", got)
	}
	if strings.Contains(strings.Join(got.RegisteredAgents, ","), "claude") || strings.Contains(got.URL, "/prod") {
		t.Fatalf("another profile's registration was attributed to this one: %+v", got)
	}
}
func TestProfileHomeSkillOnlyAndNoSyntheticProfile(t *testing.T) {
	m, b := profileHomeFixture(t)
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = ""
	m.reconcileHome()
	rows := m.contextRows()
	if len(rows) != 2 || rows[0].Kind != "configure" || rows[0].Label != "Install skill\u2026" {
		t.Fatalf("zero-input skill must offer direct installation without a synthetic instance: %+v", rows)
	}
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 0
	cmd := m.openContextRow()
	if cmd == nil {
		t.Fatal("skill-only action did not dispatch direct installation")
	}
	m.Update(cmd())
	if b.skillOnlyRequest == nil || b.skillOnlyRequest.PackageID != "plain" || m.form != nil || m.workspace != nil {
		t.Fatalf("skill-only action created an instance workspace instead of installing directly: request=%+v form=%v workspace=%+v", b.skillOnlyRequest, m.form != nil, m.workspace)
	}
	v := m.View().Content
	for _, bad := range []string{"Environment directory", "Related MCP instances", "Without an environment", "Set up another target"} {
		if strings.Contains(v, bad) {
			t.Fatal("obsolete UI: " + bad)
		}
	}
}
func TestProfileCreationCancelAndAccept(t *testing.T) {
	m, b := profileHomeFixture(t)
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 2
	m.openContextRow()
	if m.form == nil {
		t.Fatal("create form missing")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(b.created) != 0 {
		t.Fatal("cancel created profile")
	}
	m.openContextRow()
	if m.form == nil {
		t.Fatal("create form missing after cancellation")
	}
	m.form.ApplyValues(map[string]any{"name": "extra"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("accept did not dispatch")
	}
	m.Update(cmd())
	if len(b.created) != 1 || b.created[0].Name != "extra" {
		t.Fatalf("%v", b.created)
	}
}

func TestProfileModeCanRenderLegacyStateOnlyCapabilityRows(t *testing.T) {
	m, _ := profileHomeFixture(t)
	m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{{Key: state.Key{Source: "legacy", Package: "old", Target: "prod"}, Name: "prod", RuntimeStatus: "stopped"}}}
	m.home.Capabilities.Index = len(m.catalog)
	m.home.Capabilities.ID = "legacy\x00old"
	_ = m.homeView()
}

func TestProfileHomeLabelsUnmanagedComponentInventory(t *testing.T) {
	component := viewmodel.ComponentStatus{AgentID: "opencode", Kind: "skill", Name: "guide", Status: "installed", Managed: false}
	if got := instanceComponentStatusText(component); got != "OpenCode · skill · guide · installed · unmanaged" {
		t.Fatalf("unmanaged status = %q", got)
	}
}

func TestProfileHomeLabelsInstalledSkillAndRegisteredMCP(t *testing.T) {
	for _, tc := range []struct {
		component viewmodel.ComponentStatus
		want      string
	}{
		{viewmodel.ComponentStatus{AgentID: "opencode", Kind: "skill", Name: "field-guide", Status: "installed", Managed: true}, "OpenCode · skill · field-guide · installed"},
		{viewmodel.ComponentStatus{AgentID: "opencode", Kind: "mcp", Name: "map-server", Status: "installed", Managed: true}, "OpenCode · mcp · map-server · registered"},
	} {
		if got := instanceComponentStatusText(tc.component); got != tc.want {
			t.Errorf("component status = %q, want %q", got, tc.want)
		}
	}
}

func TestProfileHomeSkipsMissingConfigurationDetails(t *testing.T) {
	if got := instanceConfigurationDetails(ProfileRow{}); len(got) != 0 {
		t.Fatalf("missing configuration details = %+v", got)
	}
}

type savedOnlyTransitionBackend struct {
	*packProfileBackend
	snapshot viewmodel.InstanceSnapshot
}

func (b *savedOnlyTransitionBackend) UICatalog(context.Context) ([]catalog.Package, error) {
	return nil, nil
}

func (b *savedOnlyTransitionBackend) UIProfileSnapshot(context.Context) (viewmodel.InstanceSnapshot, error) {
	return b.snapshot, nil
}

func TestDiscardingWorkspaceIntoSavedOnlyCapabilityRendersLegacyRows(t *testing.T) {
	m, backend := profileHomeFixture(t)
	key := state.Key{Source: "home", Package: "cluster-inspector", Target: "pms15"}
	backendAfterDiscard := &savedOnlyTransitionBackend{
		packProfileBackend: backend,
		snapshot:           viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{{Key: key, Name: "pms15", RuntimeStatus: "stopped"}}},
	}
	m.backend = backendAfterDiscard
	m.home.Capabilities.Index = 0
	m.home.Capabilities.ID = "one\x00inspect"
	m.form = forms.NewForm(t.Context(), []catalog.Input{{Name: "project", Label: "Project", Type: "string"}}, map[string]any{"project": "before"})
	if err := m.form.ApplyValues(map[string]any{"project": "after"}); err != nil {
		t.Fatal(err)
	}
	m.workspace = &workspaceState{
		InstanceRef:  config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "ota"},
		Key:          state.Key{Source: "one", Package: "inspect", Target: "ota"},
		InvokingView: "Workspace",
		Active:       true,
	}

	_, _ = m.Update(forms.ExitRequestMsg{Reason: "back"})
	if m.unsavedExit == nil {
		t.Fatal("dirty workspace did not ask before discarding")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp}) // Discard changes.
	_, reload := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if reload == nil {
		t.Fatal("discard did not reload the saved capability rows")
	}
	m.Update(reload())

	selected, ok := m.selectedCapability()
	if !ok || selected.CatalogIndex >= 0 || selected.Package != key.Package {
		t.Fatalf("post-discard selection = %+v, want saved-only capability %q", selected, key.Package)
	}
	view := m.View().Content
	if !strings.Contains(view, "Capability Pack unavailable") || !strings.Contains(view, "pms15") {
		t.Fatalf("saved-only capability was not rendered through legacy rows:\n%s", view)
	}
	if strings.Contains(view, "Configuration profiles") {
		t.Fatalf("saved-only capability entered configuration-profile rendering:\n%s", view)
	}
}

func TestInstanceHomeShowsCountRuntimeAndAuthenticationSeparately(t *testing.T) {
	m, b := profileHomeFixture(t)
	snapshot := b.profiles["inspect"]
	instances := capabilityInstances(snapshot)
	instances[0].MCPs = []viewmodel.RuntimeStatus{{MCPID: "server", Status: "running", AuthenticationStatus: viewmodel.AuthenticationFailed}}
	snapshot.Instances = instances
	snapshot.Profiles = instances
	b.profiles["inspect"] = snapshot
	m.capabilityInstances["inspect"] = snapshot
	view := m.homeView().Content
	for _, want := range []string{"2 instances", "server running", "AuthenticationFailed"} {
		if !strings.Contains(view, want) {
			t.Errorf("instance view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Configuration profiles") || strings.Contains(view, "configuration profiles") {
		t.Errorf("selected-instance UI still uses profile terminology:\n%s", view)
	}
}

func TestInstanceActionMenuOffersExactDeleteForSelectedInstance(t *testing.T) {
	m, _ := profileHomeFixture(t)
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 0
	m.homeKey("a")
	if m.home.Modal == nil || m.home.Modal.Kind != "actions" {
		t.Fatal("A did not open instance actions")
	}
	items := m.homeMenuItems()
	for _, item := range items {
		if item.Action == "delete-instance" && strings.Contains(item.Label, "ota") {
			return
		}
	}
	t.Fatalf("selected instance has no exact delete action: %+v", items)
}
