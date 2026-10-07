package app

import (
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func TestEditableInputsKeepFixedVisibilityContextWithoutSubmittingController(t *testing.T) {
	defs := []catalog.Input{
		{Name: "mode", Type: "string"},
		{Name: "credential", Type: "string", Required: true, VisibleWhen: map[string]any{"mode": "remote"}},
	}
	visible, values := editableWithFixedContext(defs, map[string]any{"mode": "remote"}, map[string]any{"mode": "remote"})
	if len(visible) != 1 || visible[0].Name != "credential" || len(visible[0].VisibleWhen) != 0 {
		t.Fatalf("fixed controller condition was not simplified: %+v", visible)
	}
	if _, submitted := values["mode"]; submitted {
		t.Fatalf("fixed controller leaked into editable values: %#v", values)
	}
	visible, _ = editableWithFixedContext(defs, map[string]any{"mode": "local"}, map[string]any{"mode": "local"})
	if len(visible) != 0 {
		t.Fatalf("field dependent on a fixed mismatch was shown: %+v", visible)
	}
}
