package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
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
	removed  []config.InstanceRef
	reset    []config.InstanceRef
}

func (b *packProfileBackend) UICapabilityProfiles(context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error) {
	return b.profiles, nil
}
func (b *packProfileBackend) UICreateProfile(_ context.Context, ref config.InstanceRef) error {
	b.created = append(b.created, ref)
	return nil
}
func (b *packProfileBackend) UIRemoveInstance(_ context.Context, ref config.InstanceRef) (viewmodel.OperationResult, error) {
	b.removed = append(b.removed, ref)
	return viewmodel.OperationResult{}, nil
}
func (b *packProfileBackend) UIResetInstanceToPreset(_ context.Context, ref config.InstanceRef) (viewmodel.OperationResult, error) {
	b.reset = append(b.reset, ref)
	return viewmodel.OperationResult{Message: "Reset instance ota to pack preset; external registrations unchanged; unknown runtime may remain"}, nil
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
	m, _ := profileHomeFixture(t)
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = ""
	m.reconcileHome()
	rows := m.contextRows()
	if len(rows) != 3 || !strings.Contains(rows[0].Label, "Skill · Not installed") || rows[1].Kind != "skill-access" {
		t.Fatalf("zero-input skill must report state and offer explicit access management: %+v", rows)
	}
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 1
	cmd := m.openContextRow()
	if cmd != nil || m.home.Modal == nil || m.home.Modal.Kind != "actions" {
		t.Fatal("skill access row did not open targeted install/uninstall actions")
	}
	labels := []string{}
	for _, item := range m.homeMenuItems() {
		labels = append(labels, item.Label)
	}
	menu := strings.Join(labels, "\n")
	if !strings.Contains(menu, "Install skill for agents") || !strings.Contains(menu, "Uninstall skill from agents") {
		t.Fatalf("skill access menu omitted targeted install/uninstall actions: %s", menu)
	}
	if m.form != nil || m.workspace != nil {
		t.Fatalf("skill-only access opened a synthetic instance workspace: form=%v workspace=%+v", m.form != nil, m.workspace)
	}
	v := m.View().Content
	for _, bad := range []string{"Environment directory", "Related MCP instances", "Without an environment", "Set up another target"} {
		if strings.Contains(v, bad) {
			t.Fatal("obsolete UI: " + bad)
		}
	}
}

func TestSkillOnlyContextReportsInstalledStateAndKeepsTargetedActions(t *testing.T) {
	m, _ := profileHomeFixture(t)
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = "plain"
	m.inventory = []state.Installation{{AgentID: "codex", Component: "skill", Key: state.Key{Source: "one", Package: "plain"}}}
	m.reconcileHome()

	rows := m.contextRows()
	if len(rows) < 2 || !strings.Contains(rows[0].Label, "Installed") {
		t.Fatalf("L2 must report the actual installed skill state, got %#v", rows)
	}
	var manageIndex int = -1
	for i, row := range rows {
		if row.Kind == "skill-access" {
			manageIndex = i
			break
		}
	}
	if manageIndex < 0 {
		t.Fatalf("L2 has no skill access action: %#v", rows)
	}
	m.selectContext(manageIndex)
	if m.openContextRow() != nil || m.home.Modal == nil || m.home.Modal.Kind != "actions" {
		t.Fatalf("Manage skill access did not open the targeted install/uninstall actions: %#v", m.home.Modal)
	}
	labels := []string{}
	for _, item := range m.homeMenuItems() {
		labels = append(labels, item.Label)
	}
	joined := strings.Join(labels, "\n")
	if !strings.Contains(joined, "Install skill for agents") || !strings.Contains(joined, "Uninstall skill from agents") {
		t.Fatalf("targeted actions missing after opening L2 skill access: %s", joined)
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

func TestDeleteAndBackspaceOpenSafeInstanceConfirmation(t *testing.T) {
	for _, key := range []string{"delete", "backspace"} {
		t.Run(key, func(t *testing.T) {
			m, _ := profileHomeFixture(t)
			m.home.Focus = ProfilesPane
			m.home.Context.Index = 0
			m.homeKey(key)
			if m.home.Modal == nil || m.home.Modal.Kind != "delete-confirm" {
				t.Fatalf("%s did not open delete confirmation", key)
			}
			if m.home.Modal.Selected != 0 {
				t.Fatalf("confirmation default selection = %d, want Cancel", m.home.Modal.Selected)
			}
			view := ansi.Strip(m.View().Content)
			for _, want := range []string{"Delete instance ota?", "Cancel", "Delete"} {
				if !strings.Contains(view, want) {
					t.Fatalf("confirmation lacks %q:\n%s", want, view)
				}
			}
		})
	}
}

func TestDeleteKeyEventsReachInstanceConfirmation(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDelete}, {Code: tea.KeyBackspace}} {
		m, _ := profileHomeFixture(t)
		m.home.Focus = ProfilesPane
		m.home.Context.Index = 0
		m.Update(key)
		if m.home.Modal == nil || m.home.Modal.Kind != "delete-confirm" {
			t.Fatalf("key event %q did not open delete confirmation", key.String())
		}
	}
}

func TestDeleteInstanceConfirmationCancelAndExactConfirm(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		m, b := profileHomeFixture(t)
		m.home.Focus = ProfilesPane
		m.home.Context.Index = 0
		m.homeKey("delete")
		m.modalKey("enter")
		if len(b.removed) != 0 || m.home.Modal != nil {
			t.Fatalf("safe cancel changed instance state: removed=%+v modal=%+v", b.removed, m.home.Modal)
		}
	})
	t.Run("confirm exact selected instance", func(t *testing.T) {
		m, b := profileHomeFixture(t)
		m.home.Focus = ProfilesPane
		m.home.Context.Index = 0
		m.homeKey("delete")
		m.modalKey("down")
		cmd := m.modalKey("enter")
		if cmd == nil {
			t.Fatal("confirm did not invoke removal")
		}
		msg := cmd().(instanceRemovedMsg)
		if msg.err != nil || msg.resetPreset || msg.ref != (config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "ota"}) || len(b.removed) != 1 || b.removed[0] != msg.ref || len(b.reset) != 0 {
			t.Fatalf("removal did not target exact selected instance: msg=%+v removed=%+v reset=%+v", msg, b.removed, b.reset)
		}
	})
	t.Run("actions menu uses same confirmation", func(t *testing.T) {
		m, _ := profileHomeFixture(t)
		m.home.Focus = ProfilesPane
		m.home.Context.Index = 0
		m.homeKey("a")
		items := m.homeMenuItems()
		for i, item := range items {
			if item.Action == "delete-instance" {
				m.home.Modal.Selected = i
				m.modalKey("enter")
				if m.home.Modal == nil || m.home.Modal.Kind != "delete-confirm" {
					t.Fatal("Actions menu delete bypassed confirmation")
				}
				return
			}
		}
		t.Fatal("Actions menu lacks delete")
	})
}

func TestPackPresetDeleteShortcutOffersResetAndKeepsPreset(t *testing.T) {
	m, b := profileHomeFixture(t)
	preset := b.profiles["inspect"].Profiles[0]
	preset.Origin = "pack"
	profiles := b.profiles["inspect"].Profiles
	profiles[0] = preset
	b.profiles["inspect"] = viewmodel.CapabilityProfileSnapshot{Profiles: profiles}
	m.capabilityInstances = b.profiles
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 0
	m.homeKey("delete")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Reset instance ota to pack preset?", "Cancel", "Reset to preset"} {
		if !strings.Contains(view, want) {
			t.Fatalf("pack preset confirmation missing %q:\n%s", want, view)
		}
	}
	m.modalKey("down")
	cmd := m.modalKey("enter")
	if cmd == nil {
		t.Fatal("reset confirmation did not invoke exact instance reset")
	}
	msg := cmd().(instanceRemovedMsg)
	if msg.err != nil || !msg.resetPreset || len(b.reset) != 1 || b.reset[0] != (config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "ota"}) {
		t.Fatalf("pack preset reset did not target exact instance: msg=%+v reset=%+v removed=%+v", msg, b.reset, b.removed)
	}
	m.Update(msg)
	if !strings.Contains(m.output, "pack preset") || !strings.Contains(m.output, "external registrations unchanged") || !strings.Contains(m.output, "runtime may remain") {
		t.Fatalf("reset result did not explain preset restoration and external recovery: %q", m.output)
	}
}
