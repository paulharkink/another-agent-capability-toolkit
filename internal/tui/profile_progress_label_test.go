package tui

import (
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestSetupTargetLabelUsesProfileTerminologyForProfileKey(t *testing.T) {
	got := setupTargetLabel(state.Key{Source: "example-company", Package: "guidance", Target: "ota"})
	if got != "example-company / guidance · Profile ota" {
		t.Fatalf("setupTargetLabel() = %q", got)
	}
}
