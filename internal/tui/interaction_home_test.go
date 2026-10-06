package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func pressAndRun(m *Model, key rune) {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key})
	if cmd != nil {
		m.Update(cmd())
	}
}

func startHomeSetup(m *Model) {
	cmd := m.homeOperation("parameters")
	if m.home.Modal != nil && m.home.Modal.Kind == "target-chooser" {
		cmd = m.modalKey("enter")
	}
	if cmd != nil {
		m.Update(cmd())
	}
}

func focusHomeContext(t *testing.T, m *Model) {
	t.Helper()
	press(m, tea.KeyEnter, "")
	if m.home.Focus != ProfilesPane || m.home.Modal != nil {
		t.Fatalf("Enter should move focus from layer 1 into layer 2 only: focus=%v modal=%#v", m.home.Focus, m.home.Modal)
	}
}

func TestHomeEnterAndF2FocusLayerTwoBeforeOpeningAnAction(t *testing.T) {
	for _, test := range []struct {
		name string
		key  rune
	}{{"Enter", tea.KeyEnter}, {"F2", tea.KeyF2}} {
		t.Run(test.name, func(t *testing.T) {
			m, _ := homeFixture()
			press(m, test.key, "")
			if m.home.Focus != ProfilesPane {
				t.Fatalf("first %s should focus layer 2, got %v", test.name, m.home.Focus)
			}
			if m.home.Modal != nil {
				t.Fatalf("first %s opened a deeper overlay: %#v", test.name, m.home.Modal)
			}
			for _, want := range []string{"Set up another target", "View capability details", "Related MCP profiles", "MCP · profile"} {
				if !strings.Contains(m.View().Content, want) {
					t.Errorf("layer 2 missing %q:\n%s", want, m.View().Content)
				}
			}
		})
	}
}

func TestHomeLayerTwoEnterOpensSetupDetailsOrProfileActions(t *testing.T) {
	t.Run("setup", func(t *testing.T) {
		m, _ := homeFixture()
		setup := &setupBackendFixture{}
		m.backend = homeSetupBackend{Backend: m.backend, setupBackendFixture: setup}
		focusHomeContext(t, m)
		pressAndRun(m, tea.KeyEnter) // Set up another target opens the chooser.
		if m.home.Modal == nil || m.home.Modal.Kind != "target-chooser" {
			t.Fatal("setup action did not open the target chooser")
		}
		pressAndRun(m, tea.KeyEnter) // Without a preset.
		if m.form == nil {
			t.Fatal("choosing Without a preset did not open the setup form")
		}
	})

	t.Run("capability details", func(t *testing.T) {
		m, _ := homeFixture()
		focusHomeContext(t, m)
		press(m, tea.KeyDown, "") // View capability details.
		press(m, tea.KeyEnter, "")
		if m.home.Modal == nil || m.home.Modal.Kind != "details" {
			t.Fatalf("Enter on View capability details should open details, got %#v", m.home.Modal)
		}
	})

	t.Run("related profile actions", func(t *testing.T) {
		m, _ := homeFixture()
		setup := &setupBackendFixture{}
		m.backend = homeSetupBackend{Backend: m.backend, setupBackendFixture: setup}
		focusHomeContext(t, m)
		press(m, tea.KeyDown, "")
		press(m, tea.KeyDown, "") // Related MCP profile.
		pressAndRun(m, tea.KeyEnter)
		if m.form == nil || setup.previewRequest.Target != "production" || setup.previewRequest.Environment != "dev" {
			t.Fatalf("Enter on a server profile should open its exact shared workspace: form=%v request=%+v", m.form != nil, setup.previewRequest)
		}
	})
}

func TestHomeEscapeAndPaneKeysMoveOneLayerAtATime(t *testing.T) {
	m, _ := homeFixture()
	focusHomeContext(t, m)
	press(m, tea.KeyEscape, "")
	if m.home.Focus != CapabilitiesPane || m.home.Modal != nil {
		t.Fatalf("Esc from layer 2 should return to layer 1: focus=%v modal=%#v", m.home.Focus, m.home.Modal)
	}

	press(m, tea.KeyRight, "")
	if m.home.Focus != ProfilesPane {
		t.Fatalf("Right should focus layer 2, got %v", m.home.Focus)
	}
	press(m, tea.KeyLeft, "")
	if m.home.Focus != CapabilitiesPane {
		t.Fatalf("Left should focus layer 1, got %v", m.home.Focus)
	}
	press(m, tea.KeyTab, "")
	if m.home.Focus != ProfilesPane {
		t.Fatalf("Tab should focus layer 2, got %v", m.home.Focus)
	}
}

func TestSkillOnlyCapabilityHasUsableLayerTwoSetupAndDetails(t *testing.T) {
	m, _ := homeFixture()
	m.backend = homeSetupBackend{Backend: m.backend, setupBackendFixture: &setupBackendFixture{}}
	press(m, tea.KeyDown, "") // Plain is skill-only and has no MCP profile.
	focusHomeContext(t, m)
	view := m.View().Content
	for _, want := range []string{"Set up another target", "View capability details", "Installation · AACT records"} {
		if !strings.Contains(view, want) {
			t.Errorf("skill-only layer 2 missing %q:\n%s", want, view)
		}
	}
	pressAndRun(m, tea.KeyEnter)
	if m.home.Modal == nil || m.home.Modal.Kind != "target-chooser" {
		t.Fatal("skill-only setup did not open chooser")
	}
	pressAndRun(m, tea.KeyEnter)
	if m.form == nil {
		t.Fatal("Enter on skill-only Configure / install did not open the setup form")
	}
}

func TestHomeLayerTwoScrollShowsContinuationCues(t *testing.T) {
	m, msg := homeFixture()
	for i := 0; i < 14; i++ {
		msg.mcps = append(msg.mcps, mcp.Instance{Key: state.Key{Source: "one", Package: "inspect", Target: fmt.Sprintf("target-%02d", i)}, Status: "running"})
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.Update(msg)
	focusHomeContext(t, m)
	if !strings.Contains(m.View().Content, "↓ More below") {
		t.Fatal("layer 2 with additional related profiles lacks a down-scroll cue:\n" + m.View().Content)
	}
	press(m, tea.KeyEnd, "")
	if !strings.Contains(m.View().Content, "↑ More above") {
		t.Fatal("scrolled layer 2 lacks an up-scroll cue:\n" + m.View().Content)
	}
}

type homeSetupBackend struct {
	Backend
	*setupBackendFixture
}
