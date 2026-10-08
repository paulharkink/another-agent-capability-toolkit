package tui

import (
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestMCPConfigureUsesSourceOnlyProfileCreation(t *testing.T) {
	m, _ := homeFixture()
	m.backend = &chooserSetupBackend{}
	m.selectPane(CapabilitiesPane, 0)
	cmd := m.homeOperation("parameters")
	if cmd != nil {
		t.Fatal("opening profile creation unexpectedly started an operation")
	}
	if m.creatingProfile == nil || m.creatingProfile.PackID != "one" || m.creatingProfile.CapabilityID != "inspect" {
		c, ok := m.selectedCapability()
		t.Fatalf("capability setup did not open source-only profile creation: ref=%+v capability=%+v ok=%t focus=%v output=%q", m.creatingProfile, c, ok, m.home.Focus, m.output)
	}
	if m.home.Modal != nil {
		t.Fatalf("profile creation left an environment/target chooser open: %+v", m.home.Modal)
	}
}

func TestLegacyEnvironmentTargetDoesNotOpenWorkspaceWithoutProfileReference(t *testing.T) {
	m, _ := homeFixture()
	m.backend = &chooserSetupBackend{}
	m.selectPane(CapabilitiesPane, 0)
	m.inventory = []state.Installation{{Key: state.Key{Source: "one", Package: "inspect", Environment: "staging", Target: "canary"}, AgentID: "codex", Component: "mcp"}}
	rows := m.contextRows()
	var targetIndex = -1
	for i, row := range rows {
		if row.Kind == "target" {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		t.Fatalf("legacy saved row was not projected for profile navigation: focus=%v caps=%+v rows=%+v", m.home.Focus, m.capabilities(), rows)
	}
	m.selectContext(targetIndex)
	cmd := m.openContextRow()
	if cmd == nil {
		t.Fatal("legacy saved row did not project to a ProfileRef workspace request")
	}
	m.Update(cmd())
	if m.workspace == nil || m.workspace.Reference.PackID != "one" || m.workspace.Reference.CapabilityID != "inspect" || m.workspace.Reference.Name != "canary" || m.workspace.Key.Environment != "" {
		t.Fatalf("legacy row was not projected into pack/capability/profile identity: workspace=%+v", m.workspace)
	}
}
