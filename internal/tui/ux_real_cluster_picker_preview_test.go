package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestRealClusterInspectorPreviewExposesKubeconfigPicker(t *testing.T) {
	root := os.Getenv("AACT_UX_REAL_SOURCE_ROOT")
	bundle := os.Getenv("AACT_BUNDLED_ROOT")
	if root == "" || bundle == "" {
		t.Skip("set AACT_UX_REAL_SOURCE_ROOT and AACT_BUNDLED_ROOT to trace a local real preview")
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, err := config.Discover(root, filepath.Join(root, "aact.toml"), bundle, store.Root())
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(source, store, app.Options{})
	preview, err := svc.UISetupPreview(context.Background(), viewmodel.SetupRequest{SourceID: source.ID, PackageID: "cluster-inspector", Environment: "home", Target: "pms15"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, input := range preview.Inputs {
		if input.Definition.Name == "kubeconfig" && input.Definition.Type == "file" && input.Editable {
			found = true
		}
	}
	if !found || preview.TargetPath == "" || preview.TargetTOML == "" {
		t.Fatalf("real preview missing expected kubeconfig input or target")
	}
	m := NewContext(context.Background(), svc)
	m.openSetupForm(preview)
	if m.form == nil || !m.form.HasSection("Authentication") {
		t.Fatal("real setup preview did not expose Authentication section")
	}
	definitions := m.form.Definitions()
	found = false
	for _, def := range definitions {
		if def.Name == "kubeconfig" && def.Type == "file" {
			found = true
		}
	}
	if !found {
		t.Fatal("real setup form lost source kubeconfig file field")
	}
	m.form.SelectSection("Authentication")
	m.form.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.form.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(ansi.Strip(m.form.View().Content), "[Browse · b]") {
		t.Fatal("real setup form did not render the adjacent kubeconfig Browse action")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if cmd != nil || !m.form.PickerActive() {
		t.Fatalf("real setup preview kubeconfig field did not expose its embedded picker action; focus=%d section=%s", m.form.FocusArea(), m.form.SectionTitle())
	}
}
