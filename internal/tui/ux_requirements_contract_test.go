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

func TestUXDeclaredCredentialHintAndSourcePathStaySeparate(t *testing.T) {
	key := state.Key{Source: "fixture", Package: "cluster-inspector", Environment: "sample-env", Target: "local"}
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
