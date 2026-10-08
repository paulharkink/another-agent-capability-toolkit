package app

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"os"
	"path/filepath"
	"testing"
)

// Characterize the existing policy machinery with the new profile loader.
// Task 11 will consume the same resolved inputs without Target selection.
func TestProfileFixedDefaultCompatibility(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "guidance")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("[inputs]\nenabled = false\nlabel = 'inherited'\n[aact.input_policy]\nenabled = 'fixed'\nlabel = 'default'\n")
	path := filepath.Join(dir, "ota.toml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	defs := []catalog.Input{{Name: "enabled", Type: "boolean", Default: true}, {Name: "label", Type: "string", Default: "manifest"}}
	profile, err := config.LoadProfile(config.Pack{ID: "company", ProfileRoot: root, Catalog: []catalog.Package{{ID: "guidance", Inputs: defs}}}, "guidance", "ota")
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := fixedTargetInputs(defs, config.Target{Path: profile.Path, InputPolicy: profile.InputPolicy}, profile.Raw)
	if err != nil {
		t.Fatal(err)
	}
	values, err := forms.ResolvePartial(defs, profile.Raw, map[string]any{"label": "saved", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	values = withFixed(values, fixed)
	if values["enabled"] != false || values["label"] != "saved" {
		t.Fatalf("policy precedence: %#v", values)
	}
	inherited, err := forms.ResolvePartial(defs, profile.Raw)
	if err != nil || inherited["label"] != "inherited" || inherited["enabled"] != false {
		t.Fatalf("reset inheritance: %#v %v", inherited, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(data) {
		t.Fatal("profile was rewritten")
	}
}
