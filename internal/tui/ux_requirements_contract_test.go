package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXChooserSelectionReplacesChooserWithExactWorkspace(t *testing.T) {
	m, _ := homeFixture()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.backend = chooserSetupBackend{}
	m.environmentSnapshot = &viewmodel.EnvironmentSnapshot{SourceID: "one", Targets: []viewmodel.EnvironmentTarget{
		{SourceID: "one", Environment: "dev", PackageID: "inspect", Name: "local", Path: "/tmp/dev/inspect/local.toml"},
		{SourceID: "one", Environment: "prod", PackageID: "inspect", Name: "live", Path: "/tmp/prod/inspect/live.toml"},
	}}
	parent := ansi.Strip(m.homeView().Content)
	parentL1, parentL2, parentFocus := m.home.Capabilities.ID, m.home.Context.Index, m.home.Focus
	m.homeOperation("choose-preset")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, preview := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if preview == nil {
		t.Fatal("selecting a target did not request its actual setup preview")
	}
	msg := preview()
	if _, ok := msg.(setupPreviewMsg); !ok {
		t.Fatalf("target selection returned %T, want setupPreviewMsg", msg)
	}
	m.Update(msg)
	if m.home.Modal != nil || m.form == nil || m.pendingSetup == nil {
		t.Fatalf("chooser was not replaced by its workspace: modal=%+v form=%v preview=%+v", m.home.Modal, m.form != nil, m.pendingSetup)
	}
	want := state.Key{Source: "one", Package: "inspect", Environment: "prod", Target: "live"}
	if m.pendingSetup.Key != want {
		t.Fatalf("workspace opened the wrong target: got %+v want %+v", m.pendingSetup.Key, want)
	}
	if m.home.Capabilities.ID != parentL1 || m.home.Context.Index != parentL2 || m.home.Focus != parentFocus {
		t.Fatalf("opening the workspace changed its L1/L2 parent selection: before=(%q,%d,%v) after=(%q,%d,%v)", parentL1, parentL2, parentFocus, m.home.Capabilities.ID, m.home.Context.Index, m.home.Focus)
	}
	x, y, width, height, ok := m.setupOverlayBounds()
	if !ok || x <= 0 || y <= 0 || width >= m.width || height >= m.height || x+width > m.width || y+height > m.height {
		t.Fatalf("paired workspace is not inset within the parent: bounds=(%d,%d %dx%d) parent=%dx%d ok=%t", x, y, width, height, m.width, m.height, ok)
	}
	rendered := ansi.Strip(m.View().Content)
	for _, parentContent := range []string{"AACT · Another Agent Capability Toolkit", "Capabilities", "Selected capability · Inspector"} {
		if !strings.Contains(parent, parentContent) || !strings.Contains(rendered, parentContent) {
			t.Errorf("rendered workspace did not retain parent content %q:\n%s", parentContent, rendered)
		}
	}
	if !strings.Contains(rendered, "Workspace sections") || strings.Contains(rendered, "L3") || strings.Contains(rendered, "L4") || strings.Contains(rendered, "FOCUSED") || !strings.Contains(rendered, "│ ── Connection") {
		t.Fatalf("selected target did not render paired section and detail panes:\n%s", rendered)
	}
}

func TestUXDeclaredCredentialHintAndSourcePathStaySeparate(t *testing.T) {
	key := state.Key{Source: "fixture", Package: "cluster-inspector", Environment: "home", Target: "local"}
	for _, credentialState := range []string{"missing", "present"} {
		t.Run(credentialState, func(t *testing.T) {
			m := NewContext(t.Context(), &setupBackendFixture{})
			m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
			m.workspace = &workspaceState{Key: key, Active: true, Section: "Credentials"}
			m.openSetupForm(viewmodel.SetupPreview{
				Key: key, PackageName: "Cluster Inspector", HasManifestUI: true,
				Sections: []catalog.Section{{ID: "credentials", Title: "Credentials", Fields: []string{"token", "kubeconfig"}}},
				Inputs: []viewmodel.SetupInput{
					{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "credential-source"}, Value: "saved-token", HasValue: true, Editable: true},
					{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "credential-source", Hint: "Credential observation · " + credentialState}, Value: "/tmp/source-kubeconfig.yaml", HasValue: true, Provenance: "saved", Editable: true},
				},
			})
			m.form.SelectSection("Credentials")
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "Credential observation") || !strings.Contains(view, credentialState) {
				t.Fatalf("declared credential hint %q is not visible:\n%s", credentialState, view)
			}
			if !strings.Contains(view, "Source kubeconfig") || !strings.Contains(view, "/tmp/source-kubeconfig.yaml") {
				t.Fatalf("saved source path is not shown as an editable input:\n%s", view)
			}
		})
	}
}

func TestUXPackageDefaultHasConciseFieldOriginAndDetailedInformation(t *testing.T) {
	key := state.Key{Source: "fixture", Package: "demo", Environment: "dev", Target: "local"}
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 220, Height: 35})
	m.workspace = &workspaceState{Key: key, Active: true, Section: "Connection"}
	m.openSetupForm(viewmodel.SetupPreview{
		Key: key, PackageName: "Demo",
		Inputs: []viewmodel.SetupInput{{
			Definition: catalog.Input{Name: "endpoint", Label: "Endpoint", Type: "string"},
			Value:      "https://fixture.invalid", HasValue: true, Provenance: "package", ProvenancePath: "/source/tree/with/a/long/path/demo/package.toml", Editable: true,
		}},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"endpoint"}}},
	})
	m.form.SelectSection("Connection")
	m.form.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	connection := ansi.Strip(m.form.View().Content)
	if !strings.Contains(connection, "Endpoint [package]") || !strings.Contains(connection, "Package default") {
		t.Fatalf("field does not show its concise package-origin cue:\n%s", connection)
	}
	if strings.Contains(connection, "/source/tree/with/a/long/path/demo/package.toml") {
		t.Fatalf("normal field view repeats the full provenance path instead of keeping it in Information:\n%s", connection)
	}
	m.form.SelectSection("Information")
	information := ansi.Strip(m.form.View().Content)
	var detailText strings.Builder
	for _, line := range strings.Split(information, "\n") {
		if divider := strings.Index(line, "│"); divider >= 0 {
			detailText.WriteString(strings.NewReplacer("║", "", "·", "", " ", "").Replace(line[divider+len("│"):]))
		}
	}
	if !strings.Contains(detailText.String(), "Endpoint:") || !strings.Contains(detailText.String(), "/source/tree/with/a/long/path/demo/package.toml") {
		t.Fatalf("Information does not retain the exact value origin:\n%s", information)
	}
}
