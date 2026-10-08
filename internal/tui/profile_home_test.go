package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"strings"
	"testing"
)

type packProfileBackend struct {
	setupBackendFixture
	profiles map[string]viewmodel.CapabilityProfileSnapshot
	created  []config.ProfileRef
}

func (b *packProfileBackend) UICapabilityProfiles(context.Context) (map[string]viewmodel.CapabilityProfileSnapshot, error) {
	return b.profiles, nil
}
func (b *packProfileBackend) UICreateProfile(_ context.Context, ref config.ProfileRef) error {
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
	b := &packProfileBackend{profiles: map[string]viewmodel.CapabilityProfileSnapshot{"inspect": {Profiles: []viewmodel.CapabilityProfile{{Ref: config.ProfileRef{PackID: "one", CapabilityID: "inspect", Name: "ota"}, Key: state.Key{Source: "one", Package: "inspect", Target: "ota"}, ConfigStatus: "configured", MCPs: []viewmodel.RuntimeStatus{{MCPID: "server", Status: "stopped", Ownership: "local"}}}, {Ref: config.ProfileRef{PackID: "one", CapabilityID: "inspect", Name: "prod"}, Key: state.Key{Source: "one", Package: "inspect", Target: "prod"}, ConfigStatus: "needs-input"}}}, "plain": {}}}
	m.backend = b
	msg.capabilityProfiles = b.profiles
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
	runtime := m.profileForWorkspace(row.Key)
	if runtime == nil || runtime.RuntimeStatus != "stopped" || runtime.Ownership != "local" {
		t.Fatalf("configuration row used as runtime: %+v", runtime)
	}
	m.home.Focus = ProfilesPane
	detail := m.selectedDetail()
	if strings.Contains(detail, " /  / ") || strings.Contains(detail, "owner: ") {
		t.Fatalf("legacy empty scope shown: %s", detail)
	}
}
func TestProfileHomeSkillOnlyAndNoSyntheticProfile(t *testing.T) {
	m, _ := profileHomeFixture(t)
	m.home.Capabilities.Index = 1
	m.home.Capabilities.ID = ""
	m.reconcileHome()
	rows := m.contextRows()
	if len(rows) != 2 || rows[0].Kind != "create-profile" {
		t.Fatalf("%+v", rows)
	}
	v := m.View().Content
	for _, bad := range []string{"Environment directory", "Related MCP profiles", "Without an environment", "Set up another target"} {
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
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "legacy", Package: "old", Target: "prod"}, Name: "prod", RuntimeStatus: "stopped"}}}
	m.home.Capabilities.Index = len(m.catalog)
	m.home.Capabilities.ID = "legacy\x00old"
	_ = m.homeView()
}

func TestProfileHomeLabelsUnmanagedComponentInventory(t *testing.T) {
	component := viewmodel.ComponentStatus{AgentID: "opencode", Kind: "skill", Name: "guide", Status: "installed", Managed: false}
	if got := profileComponentStatusText(component); got != "opencode · skill · guide · installed · unmanaged" {
		t.Fatalf("unmanaged status = %q", got)
	}
}

func TestProfileHomeSkipsMissingConfigurationDetails(t *testing.T) {
	if got := profileConfigurationDetails(ProfileRow{}); len(got) != 0 {
		t.Fatalf("missing configuration details = %+v", got)
	}
}
