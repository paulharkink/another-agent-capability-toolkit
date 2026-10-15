package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func pressAndRun(m *Model, key rune) {
	_, cmd := m.Update(tea.KeyPressMsg{Code: key})
	if cmd != nil {
		m.Update(cmd())
	}
}

func startHomeSetup(m *Model) {
	capability, ok := m.selectedCapability()
	if !ok {
		return
	}
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: capability.Source, CapabilityID: capability.Package, Name: "test-profile"}}, "Overview")
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

func TestHomeEnterFocusesLayerTwoBeforeOpeningAnAction(t *testing.T) {
	m, _ := homeFixture()
	press(m, tea.KeyEnter, "")
	if m.home.Focus != ProfilesPane || m.home.Modal != nil {
		t.Fatalf("Enter should focus layer 2 before opening an action, got focus=%v modal=%#v", m.home.Focus, m.home.Modal)
	}
	for _, want := range []string{"Configure now", "View capability details", "Related MCP instances"} {
		if !strings.Contains(m.View().Content, want) {
			t.Errorf("layer 2 missing %q:\n%s", want, m.View().Content)
		}
	}
}

func TestHomeLayerTwoEnterOpensSetupDetailsOrProfileActions(t *testing.T) {
	t.Run("setup", func(t *testing.T) {
		m, _ := homeFixture()
		setup := &setupBackendFixture{}
		m.backend = homeSetupBackend{Backend: m.backend, setupBackendFixture: setup}
		focusHomeContext(t, m)
		pressAndRun(m, tea.KeyEnter) // Configure now opens the package directly.
		if m.home.Modal != nil || m.form == nil {
			t.Fatalf("Configure now did not open the setup form directly: modal=%#v form=%v", m.home.Modal, m.form != nil)
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
		if m.form == nil || setup.previewRequest.Ref.PackID != "one" || setup.previewRequest.Ref.CapabilityID != "inspect" || setup.previewRequest.Ref.Name != "production" || setup.previewRequest.Environment != "" {
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
	backend := &zeroInputSkillSetupBackend{}
	m.backend = skillOnlyInstanceBackend{backend}
	selectPlainSkill(t, m)
	m.focusPane(ProfilesPane)
	view := m.View().Content
	for _, want := range []string{"Manage skill access", "View capability details", "Skill · Not installed"} {
		if !strings.Contains(view, want) {
			t.Errorf("skill-only layer 2 missing %q:\n%s", want, view)
		}
	}
	if cmd := m.homeOperation("install-skill-only"); cmd != nil {
		t.Fatal("opening the skill-only install target selector unexpectedly started an operation")
	}
	if m.form == nil || m.pendingSkillOnly == nil || m.pendingSkillOnly.Action != "install-skill-only" {
		t.Fatalf("skill-only L2 Install skill did not open its targeted Agent access action: form=%v pending=%+v", m.form != nil, m.pendingSkillOnly)
	}
	assertAgentTargetSelector(t, m)
	if strings.Contains(m.View().Content, "already installed") && backend.skillOnlyRequest == nil {
		t.Fatal("L2 claims the skill is installed without an installed destination")
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
