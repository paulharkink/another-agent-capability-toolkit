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
		Key:         state.Key{Source: "team-source", Package: "cluster-inspector", Environment: "home", Target: "pms15"},
		PackageName: "Cluster Inspector",
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "host", Label: "Host", Type: "string"}, Value: "127.0.0.1", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "local_port", Label: "Local port", Type: "integer"}, Value: int64(8765), HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "api_server", Label: "API server", Type: "string"}, Value: "https://cluster.test", HasValue: true, Editable: true},
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Value: "saved-token", HasValue: true, Provenance: "saved", Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "connections", Label: "Read-only database queries", Type: "multichoice", Options: []catalog.Choice{{Value: "plane", Label: "Plane"}}}, Editable: true},
		},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/home/test/.codex", Selected: true}},
	})

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "L3 Sections · FOCUSED") {
		t.Fatalf("section navigation heading should match the mock:\n%s", view)
	}
	sections := []string{"Connection", "Authentication", "Databases", "Destinations"}
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
	if !strings.Contains(view, "> Authentication") {
		t.Fatalf("Cluster Inspector setup should initially select Authentication:\n%s", view)
	}
	setupKey(m, tea.KeyUp, "")
	view = m.View().Content
	for _, label := range []string{"Listen address", "Listen port"} {
		if !strings.Contains(view, label) {
			t.Fatalf("Connection detail missing mock label %q:\n%s", label, view)
		}
	}
	setupKey(m, tea.KeyDown, "")
	view = m.View().Content
	if strings.Contains(view, "Inputs") {
		t.Fatalf("Cluster Inspector mock layout should not expose a generic Inputs section:\n%s", view)
	}
	if !strings.Contains(view, "Active method") || !strings.Contains(view, "Type a path to switch to kubeconfig") {
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
		Key: state.Key{Source: "team-source", Package: "cluster-inspector"},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"}, Editable: true},
		},
	})
	view := m.View().Content
	if strings.Contains(view, "switch to Token") || strings.Contains(view, "switch to kubeconfig") || !strings.Contains(view, "Enter a Token") || !strings.Contains(view, "Enter a source kubeconfig path") {
		t.Fatalf("empty authentication methods should invite a choice, not imply one is active:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 't', Text: "test-token"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view = m.View().Content
	if !strings.Contains(view, "Active method") || !strings.Contains(view, "Type a path to switch to kubeconfig") {
		t.Fatalf("authentication cues did not react to typing a token:\n%s", view)
	}
}

func TestEmptySavedKubeconfigDoesNotLookConfigured(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.catalog = []catalog.Package{{ID: "cluster-inspector", MCP: &catalog.MCP{}}}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.openSetupForm(viewmodel.SetupPreview{
		Key: state.Key{Source: "team-source", Package: "cluster-inspector"},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"}, Editable: true},
			{Definition: catalog.Input{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"}, HasValue: true, Value: "", Provenance: "saved", Editable: true},
		},
	})
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Source kubeconfig [saved]") || !strings.Contains(view, "Enter a source kubeconfig path") {
		t.Fatalf("blank saved kubeconfig should appear as an empty method:\n%s", view)
	}
}
