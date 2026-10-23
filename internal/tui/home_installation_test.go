package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestHomeShowsRecordedInstallationStatusWithoutBatchControls(t *testing.T) {
	m, msg := homeFixture()
	msg.catalog[0].Skill = &catalog.Skill{Name: "inspect"}
	msg.inventory = []state.Installation{
		{Key: state.Key{Source: "one", Package: "inspect", Environment: "sample-env", Target: "prod"}, AgentID: "codex", Component: "skill"},
		{Key: state.Key{Source: "one", Package: "plain"}, AgentID: "all", Component: "skill"},
		{Key: state.Key{Source: "other", Package: "empty"}, Component: "mcp"},
		{Key: state.Key{Source: "one", Package: "empty"}, Component: "runtime"},
	}
	m.Update(msg)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Inspector", "Plain", "Empty", "AACT records: Unknown"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"[ ]", "[x]", "Apply marked", "Marked capabilities", "Space Mark", "Partial", "Installed", "Not installed"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("home still shows %q:\n%s", unwanted, view)
		}
	}
	before := view
	press(m, 0, " ")
	if got := ansi.Strip(m.View().Content); got != before {
		t.Fatal("Space changed the capability list after batch removal")
	}
	m.openHomeMenu("actions")
	if got := strings.Join(m.menuEntries(), "\n"); strings.Contains(got, "mark") || strings.Contains(got, "batch") {
		t.Fatalf("actions still contain batch controls: %s", got)
	}
}

func TestHomeShowsUnavailableWhenInventoryCannotBeRead(t *testing.T) {
	m, msg := homeFixture()
	msg.inventoryError = errors.New("ledger read failed")
	m.Update(msg)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Unknown") || !strings.Contains(view, "ledger read failed") {
		t.Fatalf("inventory error hidden or presented as not installed:\n%s", view)
	}
}

func TestMultiMCPInstallationDetailsRequireEveryDeclaredChild(t *testing.T) {
	m, msg := homeFixture()
	msg.inventory = []state.Installation{{Key: state.Key{Source: "one", Package: "inspect", Target: "production", MCP: "alpha"}, AgentID: "codex", Component: "mcp"}}
	m.Update(msg)
	status, details := m.installationDetails(CapabilityRow{Source: "one", Package: "inspect", MCP: true, MCPNames: []string{"alpha", "beta"}})
	if status != "Unknown" || len(details) != 1 || !strings.Contains(details[0], "not been observed") {
		t.Fatalf("ledger-only multi-MCP registrations implied installed state: status=%q details=%v", status, details)
	}
}

func TestInstallationStatusRequiresOneCompleteTargetAndDestinationBinding(t *testing.T) {
	m, msg := homeFixture()
	c := CapabilityRow{Source: "one", Package: "inspect", Skill: true, MCP: true, MCPNames: []string{"alpha", "beta"}}
	partialProfile := state.Key{Source: "one", Package: "inspect", Target: "prod"}
	completeProfile := state.Key{Source: "one", Package: "inspect", Target: "local"}
	msg.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{"inspect": {Instances: []viewmodel.CapabilityInstance{
		{Key: partialProfile, Ref: config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "prod"}, Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "inspect", Status: "installed"}, {AgentID: "claude", Kind: "mcp", Name: "alpha", Status: "installed"}}},
		{Key: completeProfile, Ref: config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "local"}, Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "inspect", Status: "installed"}, {AgentID: "codex", Kind: "mcp", Name: "alpha", Status: "installed"}, {AgentID: "codex", Kind: "mcp", Name: "beta", Status: "installed"}}},
	}}}
	msg.inventory = []state.Installation{
		{Key: partialProfile, AgentID: "codex", Component: "skill"},
		{Key: state.Key{Source: partialProfile.Source, Package: partialProfile.Package, Target: partialProfile.Target, MCP: "alpha"}, AgentID: "claude", Component: "mcp"},
		{Key: state.Key{Source: partialProfile.Source, Package: partialProfile.Package, Target: partialProfile.Target, MCP: "beta"}, AgentID: "claude", Component: "mcp"},
	}
	m.Update(msg)
	if status, _ := m.installationDetails(c); status != "Installed" {
		t.Fatalf("an observed Agent registration did not mark the capability installed: %q", status)
	}

	msg.inventory = append(msg.inventory,
		state.Installation{Key: completeProfile, AgentID: "codex", AgentHome: "/home/codex", Component: "skill"},
		state.Installation{Key: state.Key{Source: completeProfile.Source, Package: completeProfile.Package, Target: completeProfile.Target, MCP: "alpha"}, AgentID: "codex", AgentHome: "/home/codex", Component: "mcp"},
		state.Installation{Key: state.Key{Source: completeProfile.Source, Package: completeProfile.Package, Target: completeProfile.Target, MCP: "beta"}, AgentID: "codex", AgentHome: "/home/codex", Component: "mcp"},
	)
	m.Update(msg)
	if status, _ := m.installationDetails(c); status != "Installed" {
		t.Fatalf("complete multi-MCP destination binding was not recognized: %q", status)
	}
}

func TestRunningMCPWithoutAgentRegistrationIsNotReportedInstalled(t *testing.T) {
	m, msg := homeFixture()
	key := state.Key{Source: "one", Package: "inspect", Target: "production"}
	msg.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{"inspect": {Instances: []viewmodel.CapabilityInstance{{
		Key: key, Ref: config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "production"}, MCPs: []viewmodel.RuntimeStatus{{MCPID: "inspect", Status: "running"}}, Components: []viewmodel.ComponentStatus{},
	}}}}
	msg.inventory = []state.Installation{{Key: state.Key{Source: key.Source, Package: key.Package, Target: key.Target, MCP: "inspect"}, AgentID: "codex", Component: "mcp"}}
	m.Update(msg)
	status, details := m.installationDetails(CapabilityRow{Source: "one", Package: "inspect", MCP: true, MCPNames: []string{"inspect"}})
	if status != "Unknown" || len(details) != 1 || !strings.Contains(details[0], "Agent registration observation") {
		t.Fatalf("runtime/ledger state without an Agent observation was reported as installed: %q %v", status, details)
	}
}

func TestCapabilityInstalledStatusUsesAnyAchievedInstanceRegistration(t *testing.T) {
	m, msg := homeFixture()
	key := state.Key{Source: "one", Package: "inspect", Target: "saved-instance"}
	msg.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{
		"inspect": {Instances: []viewmodel.CapabilityInstance{{
			Ref: config.InstanceRef{PackID: "one", CapabilityID: "inspect", Name: "saved-instance"}, Key: key,
			CompletionScopeKnown: true, CompletionScopeRelevant: true,
			SelectedComponents:   []viewmodel.SelectedComponent{{Kind: "skill", Name: "inspect"}},
			EligibleDestinations: []string{"claude"},
			Components: []viewmodel.ComponentStatus{
				{AgentID: "claude", Kind: "skill", Name: "inspect", Status: "absent"},
				{AgentID: "codex", Kind: "skill", Name: "inspect", Status: "installed", Managed: true},
			},
		}}},
	}
	msg.inventory = []state.Installation{{Key: key, AgentID: "codex", Component: "skill"}}
	m.Update(msg)
	status, _ := m.installationDetails(CapabilityRow{Source: "one", Package: "inspect", Skill: true})
	if status != "Installed" {
		t.Fatalf("registered Pack Instance must make the Capability Installed regardless of pending selection: %q", status)
	}
}
