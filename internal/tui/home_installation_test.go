package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestHomeShowsRecordedInstallationStatusWithoutBatchControls(t *testing.T) {
	m, msg := homeFixture()
	msg.catalog[0].Skill = &catalog.Skill{Name: "inspect"}
	msg.inventory = []state.Installation{
		{Key: state.Key{Source: "one", Package: "inspect", Environment: "home", Target: "prod"}, AgentID: "codex", Component: "skill"},
		{Key: state.Key{Source: "one", Package: "plain"}, AgentID: "all", Component: "skill"},
		{Key: state.Key{Source: "other", Package: "empty"}, Component: "mcp"},
		{Key: state.Key{Source: "one", Package: "empty"}, Component: "runtime"},
	}
	m.Update(msg)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Inspector", "Partial", "Plain", "Installed", "Empty", "Not installed", "AACT records"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"[ ]", "[x]", "Apply marked", "Marked capabilities", "Space Mark"} {
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
