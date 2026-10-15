package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	if m.creatingInstance == nil || m.creatingInstance.PackID != "one" || m.creatingInstance.CapabilityID != "inspect" {
		c, ok := m.selectedCapability()
		t.Fatalf("capability setup did not open source-only profile creation: ref=%+v capability=%+v ok=%t focus=%v output=%q", m.creatingInstance, c, ok, m.home.Focus, m.output)
	}
	if m.home.Modal != nil {
		t.Fatalf("profile creation left an environment/target chooser open: %+v", m.home.Modal)
	}
}

func TestNewInstancePromptUsesStyledHomeOverlay(t *testing.T) {
	m, _ := homeFixture()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	c, ok := m.selectedCapability()
	if !ok {
		t.Fatal("fixture has no selected capability")
	}
	m.openInstanceCreation(c)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Capabilities", "Capability Pack:", "New instance", "Instance name"} {
		if !strings.Contains(view, want) {
			t.Fatalf("New Instance prompt is missing styled home context or title %q:\n%s", want, view)
		}
	}
}

func TestLegacyEnvironmentTargetDoesNotOpenWorkspaceWithoutInstanceReference(t *testing.T) {
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
		t.Fatal("legacy saved row did not project to a InstanceRef workspace request")
	}
	m.Update(cmd())
	if m.workspace == nil || m.workspace.InstanceRef.PackID != "one" || m.workspace.InstanceRef.CapabilityID != "inspect" || m.workspace.InstanceRef.Name != "canary" || m.workspace.Key.Environment != "" {
		t.Fatalf("legacy row was not projected into pack/capability/profile identity: workspace=%+v", m.workspace)
	}
}
