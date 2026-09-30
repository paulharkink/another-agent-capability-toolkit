package tui

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"testing"
)

func TestSharedEditorContract(t *testing.T) {
	var _ func(context.Context, []catalog.Input, map[string]any) (map[string]any, error) = Edit
	editor := forms.NewEditor([]catalog.Input{{Name: "team", Type: "string"}}, map[string]any{"team": "prefilled"})
	if e := editor.Apply("team", "edited"); e != nil {
		t.Fatal(e)
	}
	got, e := editor.Commit()
	if e != nil || got["team"] != "edited" {
		t.Fatalf("%v %v", got, e)
	}
}
