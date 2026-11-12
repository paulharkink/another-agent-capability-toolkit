package app

import (
	"context"
	"slices"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
)

func TestInteractivePresentationKeepsFullValidationAndHiddenSelections(t *testing.T) {
	defs := []catalog.Input{
		{Name: "auth_method", Type: "choice", Required: true, Options: []catalog.Choice{{Value: "oauth"}, {Value: "personal"}}},
		{Name: "private_note", Type: "string", Required: true},
		{Name: "features", Type: "multichoice", Multiple: true, Options: []catalog.Choice{{Value: "visible_a"}, {Value: "internal"}, {Value: "visible_b"}}},
	}
	values := map[string]any{"auth_method": "personal", "private_note": "preserved", "features": []string{"internal", "visible_a"}}
	ui := config.PackageUI{HideInputs: []string{"private_note"}, HideChoices: map[string][]string{"auth_method": {"personal"}, "features": {"internal"}}}
	editor := func(ctx context.Context, fullDefs []catalog.Input, prefill map[string]any, hiddenInputs []string, hiddenChoices map[string][]string) (map[string]any, error) {
		if len(fullDefs) != 3 || !slices.Contains(hiddenInputs, "private_note") || !slices.Contains(hiddenChoices["auth_method"], "personal") {
			t.Fatalf("interactive editor did not receive full definitions and separate presentation metadata: defs=%+v hidden=%+v choices=%+v", fullDefs, hiddenInputs, hiddenChoices)
		}
		// A real form returns its complete value map, including hidden selections.
		return map[string]any{"auth_method": "personal", "private_note": "preserved", "features": []string{"internal", "visible_b"}}, nil
	}
	if err := editProfileInputs(context.Background(), nil, editor, defs, values, nil, ui); err != nil {
		t.Fatal(err)
	}
	if values["auth_method"] != "personal" || values["private_note"] != "preserved" {
		t.Fatalf("presentation coerced or dropped hidden values: %#v", values)
	}
	features, _ := values["features"].([]string)
	if !slices.Contains(features, "internal") || slices.Contains(features, "visible_a") {
		t.Fatalf("interactive choice edit did not preserve hidden multichoice selection: %#v", features)
	}
	if err := forms.Validate(defs, values); err != nil {
		t.Fatalf("hidden values/options failed original validation: %v", err)
	}
}

func TestLegacyEditorKeepsFullChoiceDefinitionsForHiddenManualValues(t *testing.T) {
	defs := []catalog.Input{{Name: "auth_method", Type: "choice", Required: true, Options: []catalog.Choice{{Value: "oauth"}, {Value: "personal"}}}}
	values := map[string]any{"auth_method": "oauth"}
	legacyEditor := func(_ context.Context, received []catalog.Input, prefill map[string]any) (map[string]any, error) {
		if len(received) != 1 || len(received[0].Options) != 2 {
			t.Fatalf("legacy editor received narrowed validation definitions: %+v", received)
		}
		return map[string]any{"auth_method": "personal"}, nil
	}
	ui := config.PackageUI{HideChoices: map[string][]string{"auth_method": {"personal"}}}
	if err := editProfileInputs(context.Background(), legacyEditor, nil, defs, values, nil, ui); err != nil {
		t.Fatal(err)
	}
	if values["auth_method"] != "personal" {
		t.Fatalf("legacy editor's manually supplied hidden choice was lost: %#v", values)
	}
	if err := forms.Validate(defs, values); err != nil {
		t.Fatalf("hidden manual choice failed original validation: %v", err)
	}
}
