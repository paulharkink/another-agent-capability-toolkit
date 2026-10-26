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
	if len(rows) != 5 || rows[0].Kind != "profile" || !strings.Contains(rows[0].Label, "ota") || !strings.Contains(rows[0].Label, "stopped") || rows[1].Kind != "mcp-child" || rows[1].MCPID != "server" {
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
func TestProfileHomePlainSkillHasOneDefaultInstance(t *testing.T) {
	m, backend := profileHomeFixture(t)
	backend.profiles["plain"] = viewmodel.CapabilityProfileSnapshot{Profiles: []viewmodel.CapabilityProfile{{
		Ref: config.InstanceRef{PackID: "one", CapabilityID: "plain", Name: "default"},
		Key: state.Key{Source: "one", Package: "plain", Target: "default"}, ConfigStatus: "configured",
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "plain", Status: "absent"}},
	}}}
	m.capabilityInstances = backend.profiles
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = m.capabilities()[1].ID
	m.reconcileHome()
	rows := m.contextRows()
	if len(rows) != 2 || rows[0].Kind != "profile" || rows[0].Key.Target != "default" || !strings.Contains(rows[0].Label, "Not installed") {
		t.Fatalf("plain skill should show its ordinary default Capability Instance: %+v", rows)
	}
	for _, row := range rows {
		if row.Kind == "create-profile" {
			t.Fatal("zero-input skill exposed a second instance action")
		}
	}
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 0
	cmd := m.openContextRow()
	if cmd == nil {
		t.Fatal("opening default skill did not load its Agent Access workspace")
	}
	m.Update(cmd())
	if backend.previewRequest.Ref.Name != "default" || m.workspace == nil || m.workspace.SectionID != sectionAgentsID {
		t.Fatalf("default skill Instance did not open Agent Access: ref=%+v workspace=%+v", backend.previewRequest.Ref, m.workspace)
	}
}

func TestPlainSkillUsesDefaultInstanceAndOpensAgentAccessStage(t *testing.T) {
	m, backend := profileHomeFixture(t)
	backend.profiles["plain"] = viewmodel.CapabilityInstanceSnapshot{Instances: []viewmodel.CapabilityInstance{{
		Ref: config.InstanceRef{PackID: "one", CapabilityID: "plain", Name: "default"},
		Key: state.Key{Source: "one", Package: "plain", Target: "default"}, ConfigStatus: "configured",
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "plain", Status: "absent"}},
	}}}
	m.capabilityInstances = backend.profiles
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = m.capabilities()[1].ID
	m.reconcileHome()
	rows := m.contextRows()
	if len(rows) < 2 || rows[0].Kind != "profile" || rows[0].Key.Target != "default" || !strings.Contains(rows[0].Label, "Not installed") {
		t.Fatalf("plain skill should be represented by its default Capability Instance: %+v", rows)
	}
	m.home.Focus = ProfilesPane
	m.home.Context.Index = 0
	cmd := m.openContextRow()
	if cmd == nil {
		t.Fatal("opening default skill Instance did not start setup loading")
	}
	m.Update(cmd())
	if backend.previewRequest.Ref.Name != "default" || backend.previewRequest.Ref.PackID != "one" {
		t.Fatalf("Agent Access did not use the Pack-owned default Instance: %+v", backend.previewRequest.Ref)
	}
	if m.workspace == nil || m.workspace.SectionID != sectionAgentsID {
		t.Fatalf("zero-input skill opened a configuration stage instead of Agent Access: %+v", m.workspace)
	}
	for _, row := range rows {
		if row.Kind == "create-profile" {
			t.Fatal("zero-input skill offered a duplicate Instance")
		}
	}
}

func TestPlainSkillInstalledStateRequiresAgentObservation(t *testing.T) {
	m, backend := profileHomeFixture(t)
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = "plain"
	m.inventory = []state.Installation{{AgentID: "codex", Component: "skill", Key: state.Key{Source: "one", Package: "plain", Target: "default"}}}
	backend.profiles["plain"] = viewmodel.CapabilityProfileSnapshot{Profiles: []viewmodel.CapabilityProfile{{
		Ref: config.InstanceRef{PackID: "one", CapabilityID: "plain", Name: "default"},
		Key: state.Key{Source: "one", Package: "plain", Target: "default"}, ConfigStatus: "configured",
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "plain", Status: "installed"}},
	}}}
	m.capabilityInstances = backend.profiles
	m.reconcileHome()
	rows := m.contextRows()
	if len(rows) == 0 || !strings.Contains(rows[0].Label, "Installed") {
		t.Fatalf("observed Agent registration did not mark capability installed: %+v", rows)
	}
	backend.profiles["plain"] = viewmodel.CapabilityProfileSnapshot{Profiles: []viewmodel.CapabilityProfile{{
		Ref: config.InstanceRef{PackID: "one", CapabilityID: "plain", Name: "default"}, Key: state.Key{Source: "one", Package: "plain", Target: "default"},
	}}}
	m.capabilityInstances = backend.profiles
	m.reconcileHome()
	rows = m.contextRows()
	if len(rows) == 0 || !strings.Contains(rows[0].Label, "Unknown") {
		t.Fatalf("stale ledger entry was presented as observed registration: %+v", rows)
	}
}

func TestProfileCreationCancelAndAccept(t *testing.T) {
	m, b := profileHomeFixture(t)
	for i, row := range m.contextRows() {
		if row.Kind == "create-profile" {
			m.selectContext(i)
			break
		}
	}
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

func TestInstanceContextShowsEveryDeclaredMCPAndLinkedRuntimeRecord(t *testing.T) {
	m, backend := profileHomeFixture(t)
	snapshot := backend.profiles["inspect"]
	instances := capabilityInstances(snapshot)
	childKey := state.Key{Source: "one", Package: "inspect", Target: "ota", MCP: "server"}
	instances[0].MCPs = []viewmodel.RuntimeStatus{{
		MCPID: "server", Key: childKey, Status: "multiple", Ownership: "local",
		RuntimeRecords: []viewmodel.RuntimeRecord{
			{ID: "container-a", Name: "aact-server-a", Status: "missing", Ownership: "local", LastAction: "start"},
			{ID: "container-b", Name: "aact-server-b", Status: "missing", Ownership: "local", LastAction: "restart"},
		},
	}}
	snapshot.Instances, snapshot.Profiles = instances, instances
	backend.profiles["inspect"] = snapshot
	m.capabilityInstances["inspect"] = snapshot

	rows := m.contextRows()
	var child, first, second *contextRow
	for i := range rows {
		row := &rows[i]
		switch row.Kind {
		case "mcp-child":
			if row.MCPID == "server" {
				child = row
			}
		case "runtime-record":
			if row.RuntimeID == "container-a" {
				first = row
			}
			if row.RuntimeID == "container-b" {
				second = row
			}
		}
	}
	if child == nil || first == nil || second == nil {
		t.Fatalf("declared MCP and both exact linked runtime records must be visible: %+v", rows)
	}
	if first.ID == second.ID || first.Key != childKey || second.Key != childKey || first.ParentKey.Target != "ota" || second.ParentKey.Target != "ota" {
		t.Fatalf("runtime rows lost exact child/parent identity: first=%+v second=%+v child=%+v", first, second, child)
	}
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 32})
	for _, want := range []string{"MCP runtime · server · multiple", "container-a", "container-b"} {
		if !strings.Contains(m.homeView().Content, want) {
			t.Fatalf("instance pane omitted %q:\n%s", want, m.homeView().Content)
		}
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

func TestDiscardingWorkspaceDoesNotLeakAnotherPackSavedRows(t *testing.T) {
	m, backend := profileHomeFixture(t)
	key := state.Key{Source: "home", Package: "cluster-inspector", Target: "test-cluster"}
	backendAfterDiscard := &savedOnlyTransitionBackend{
		packProfileBackend: backend,
		snapshot:           viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{{Key: key, Name: "test-cluster", RuntimeStatus: "stopped"}}},
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

	view := m.View().Content
	if strings.Contains(view, key.Package) || strings.Contains(view, "test-cluster") {
		t.Fatalf("another Pack's saved-only capability leaked after workspace discard:\n%s", view)
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
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 32})
	view := m.homeView().Content
	for _, want := range []string{"2 instances", "MCP runtime · server · running", "AuthenticationFailed"} {
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
