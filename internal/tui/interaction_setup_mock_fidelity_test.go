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

func TestInteractionClusterSetupMatchesMockSectionsAndAuthCues(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.catalog = []catalog.Package{{ID: "cluster-inspector", MCP: &catalog.MCP{}}}
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 36})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:            state.Key{Source: "team-source", Package: "cluster-inspector", Environment: "home", Target: "pms15"},
		PackageName:    "Cluster Inspector",
		MCP:            true,
		MCPDefinitions: []catalog.MCP{{Name: "cluster-inspector"}},
		HasManifestUI:  true,
		Sections: []catalog.Section{
			{ID: "connection", Title: "Connection", Fields: []string{"host", "local_port", "api_server"}},
			{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}},
			{ID: "databases", Title: "Databases", Fields: []string{"connections"}},
		},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "host", Label: "Listen address", Type: "string"}, Value: "127.0.0.1", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "local_port", Label: "Listen port", Type: "integer"}, Value: int64(8765), HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "api_server", Label: "API server", Type: "string"}, Value: "https://cluster.test", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Value: "saved-token", HasValue: true, Provenance: "saved", Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Hint: "Import source; not a live path · AACT uses managed credential material at runtime", Type: "file", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "connections", Label: "Read-only database queries", Type: "multichoice", Options: []catalog.Choice{{Value: "plane", Label: "Plane"}}}, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", Selected: true}},
	})

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "L3") || strings.Contains(view, "L4") || strings.Contains(view, "FOCUSED") || !strings.Contains(view, "Sections") {
		t.Fatalf("section navigation heading should use its configured title:\n%s", view)
	}
	sections := []string{"Connection", "Authentication", "Databases", "Agents"}
	leftItems := []string{}
	for _, line := range strings.Split(view, "\n") {
		left, _, found := strings.Cut(line, "│")
		if !found {
			continue
		}
		left = strings.TrimSpace(strings.TrimLeft(left, " ║>›"))
		for _, section := range sections {
			if left == section || left == "> "+section {
				leftItems = append(leftItems, strings.TrimPrefix(left, "> "))
			}
		}
	}
	if strings.Join(leftItems, ",") != strings.Join(sections, ",") {
		t.Fatalf("setup section list order should be %v, got %v:\n%s", sections, leftItems, view)
	}
	if !strings.Contains(view, "> Connection") {
		t.Fatalf("Cluster Inspector setup should initially select the first declared section:\n%s", view)
	}
	setupKey(m, tea.KeyRight, "") // Open the first declared Connection section.
	view = m.View().Content
	for _, label := range []string{"Listen address", "Listen port"} {
		if !strings.Contains(view, label) {
			t.Fatalf("Connection detail missing mock label %q:\n%s", label, view)
		}
	}
	setupKey(m, tea.KeyLeft, "")
	setupKey(m, tea.KeyDown, "")
	setupKey(m, tea.KeyRight, "") // Authentication.
	view = m.View().Content
	if strings.Contains(view, "Inputs") {
		t.Fatalf("Cluster Inspector mock layout should not expose a generic Inputs section:\n%s", view)
	}
	if !strings.Contains(view, "Active method") || !strings.Contains(view, "Type a value to switch to Source kubeconfig") || !strings.Contains(view, "Import source; not a live path") {
		t.Fatalf("authentication details should identify the active method and how to switch methods:\n%s", view)
	}
}

func TestSetupShowsReadableProvenanceWithoutStateHashPath(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "plain"},
		Inputs: []viewmodel.SetupInput{{
			Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string"},
			Value:      "/repos/team", HasValue: true, Editable: true,
			Provenance: "saved", ProvenancePath: "/state/aact/94d92a040f21e497.json",
		}},
	})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	view := m.View().Content
	if !strings.Contains(view, "Saved override · editable") || strings.Contains(view, "94d92a040f21e497.json") {
		t.Fatalf("setup should explain editable saved value without exposing a hash filename:\n%s", view)
	}
}

func TestClusterAuthWithNoValueDoesNotClaimASelectedMethod(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.catalog = []catalog.Package{{ID: "cluster-inspector", MCP: &catalog.MCP{}}}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:            state.Key{Source: "team-source", Package: "cluster-inspector"},
		MCP:            true,
		MCPDefinitions: []catalog.MCP{{Name: "cluster-inspector"}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Hint: "Import source; not a live path · AACT uses managed credential material at runtime", Type: "file", ExclusiveGroup: "auth"}, Editable: true},
		},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}}},
	})
	view := m.View().Content
	if strings.Contains(view, "switch to Token") || strings.Contains(view, "switch to Source kubeconfig") || !strings.Contains(view, "Enter a value for Token") || !strings.Contains(view, "Enter a value for Source kubeconfig") || !strings.Contains(view, "Import source") {
		t.Fatalf("empty authentication methods should invite a choice, not imply one is active:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 't', Text: "test-token"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view = m.View().Content
	if !strings.Contains(view, "Active method") || !strings.Contains(view, "Type a value to switch to Source kubeconfig") {
		t.Fatalf("authentication cues did not react to typing a token:\n%s", view)
	}
}

func TestEmptySavedKubeconfigDoesNotLookConfigured(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.catalog = []catalog.Package{{ID: "cluster-inspector", MCP: &catalog.MCP{}}}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:            state.Key{Source: "team-source", Package: "cluster-inspector"},
		MCP:            true,
		MCPDefinitions: []catalog.MCP{{Name: "cluster-inspector"}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Hint: "Import source; not a live path · AACT uses managed credential material at runtime", Type: "file", ExclusiveGroup: "auth"}, HasValue: true, Value: "", Provenance: "saved", Editable: true},
		},
		HasManifestUI: true,
		Sections:      []catalog.Section{{ID: "authentication", Title: "Authentication", Fields: []string{"token", "kubeconfig"}}},
	})
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Source kubeconfig [saved]") || !strings.Contains(view, "Enter a value for Source kubeconfig") || !strings.Contains(view, "Import source") {
		t.Fatalf("blank saved kubeconfig should appear as an empty method:\n%s", view)
	}
}
