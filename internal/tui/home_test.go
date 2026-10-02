package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"strings"
	"testing"
)

func homeFixture() (*Model, loadedMsg) {
	m := NewContext(context.Background(), fixtureBackend{})
	msg := loadedMsg{settings: map[string]string{"source": "one", "checkout": "/checkout"}, labels: map[string]string{"/one": "one", "/plain": "one", "/empty": "one"}, catalog: []catalog.Package{{ID: "inspect", Name: "Inspector", Dir: "/one", MCP: &catalog.MCP{}}, {ID: "plain", Name: "Plain", Dir: "/plain", Skill: &catalog.Skill{Name: "plain"}}, {ID: "empty", Name: "Empty", Dir: "/empty", MCP: &catalog.MCP{}}}, mcps: []mcp.Instance{{Key: state.Key{Source: "one", Package: "inspect", Environment: "dev", Target: "production"}, Name: "profile", Status: "running"}, {Key: state.Key{Source: "other", Package: "inspect", Target: "wrong-source"}, Status: "running"}, {Key: state.Key{Source: "one", Package: "else", Target: "wrong-package"}, Status: "running"}}}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(msg)
	return m, msg
}
func TestHomeTwoPanesFilterAndEmptyStates(t *testing.T) {
	m, _ := homeFixture()
	v := m.View().Content
	for _, s := range []string{"Capabilities", "MCP profiles", "production", "running", "Main menu", "F2 Actions"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q: %s", s, v)
		}
	}
	for _, s := range []string{"wrong-source", "wrong-package"} {
		if strings.Contains(v, s) {
			t.Errorf("unrelated profile %q visible", s)
		}
	}
	press(m, tea.KeyDown, "")
	if !strings.Contains(m.View().Content, "skill-only capability") {
		t.Fatal(m.View().Content)
	}
	press(m, tea.KeyTab, "")
	if m.view != "Catalog" {
		t.Fatal("Tab left home")
	}
	press(m, tea.KeyDown, "")
	if !strings.Contains(m.View().Content, "configure/install") {
		t.Fatal(m.View().Content)
	}
}
func TestHomePaneFocusAndStableScrolling(t *testing.T) {
	m, msg := homeFixture()
	for i := 0; i < 30; i++ {
		msg.mcps = append(msg.mcps, mcp.Instance{Key: state.Key{Source: "one", Package: "inspect", Target: fmt.Sprintf("target-%02d", i)}, Status: "running"})
	}
	m.Update(msg)
	press(m, tea.KeyTab, "")
	press(m, tea.KeyEnd, "")
	if !strings.Contains(m.View().Content, "target-29") {
		t.Fatal("End did not scroll profile list")
	}
	press(m, tea.KeyLeft, "")
	press(m, tea.KeyRight, "")
	if !strings.Contains(m.View().Content, "target-29") {
		t.Fatal("focus lost profile selection")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.Update(msg)
	if !strings.Contains(m.View().Content, "target-29") {
		t.Fatal("resize/refresh lost selection")
	}
	msg.mcps = msg.mcps[:len(msg.mcps)-1]
	m.Update(msg)
	if !strings.Contains(m.View().Content, "target-28") || !strings.Contains(m.output, "removed") {
		t.Fatal("deleted selected profile not explained", m.View().Content)
	}
	press(m, tea.KeyHome, "")
	if !strings.Contains(m.View().Content, "production") {
		t.Fatal("Home failed")
	}
	press(m, tea.KeyPgDown, "")
	if strings.Contains(m.View().Content, "> production") {
		t.Fatal("PageDown did not move")
	}
}
func TestHomeMenuDestinationsAndReturn(t *testing.T) {
	for i, name := range []string{"Agents", "Environments", "Settings", "Help"} {
		m, _ := homeFixture()
		press(m, 'm', "m")
		for j := 0; j < i; j++ {
			press(m, tea.KeyDown, "")
		}
		press(m, tea.KeyEnter, "")
		if m.view != name {
			t.Fatalf("destination %d: %s", i, m.view)
		}
		press(m, tea.KeyEscape, "")
		if m.view != "Catalog" || !strings.Contains(m.View().Content, "production") {
			t.Fatal("Back lost home")
		}
	}
}
func TestHomeMouseScrollAndResizeHitRegions(t *testing.T) {
	m, msg := homeFixture()
	for i := 0; i < 30; i++ {
		msg.catalog = append(msg.catalog, catalog.Package{ID: fmt.Sprintf("cap-%02d", i), Name: fmt.Sprintf("Capability %02d", i), Dir: fmt.Sprintf("/cap/%d", i), Skill: &catalog.Skill{}})
	}
	m.Update(msg)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.View()
	m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	m.Update(tea.MouseClickMsg{X: 10, Y: 5, Button: tea.MouseLeft})
	if !strings.Contains(m.View().Content, "Capability") {
		t.Fatal("scroll/click did not select scrolled row", m.View().Content)
	}
	m.Update(tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft})
	if !strings.Contains(m.View().Content, "Environments") {
		t.Fatal("Main menu control not clickable")
	}
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	if !strings.Contains(m.View().Content, "80×16") {
		t.Fatal("small terminal guidance missing")
	}
}

func TestCapabilitySelectionSurvivesReorderAndDeletion(t *testing.T) {
	m, msg := homeFixture()
	press(m, tea.KeyDown, "")
	selected := m.home.Capabilities.ID
	msg.catalog[0], msg.catalog[1] = msg.catalog[1], msg.catalog[0]
	m.Update(msg)
	if m.home.Capabilities.ID != selected || m.home.Capabilities.Index != 0 {
		t.Fatal("refresh selected a different capability")
	}
	msg.catalog = msg.catalog[1:]
	m.Update(msg)
	if m.home.Capabilities.Index != 0 || !strings.Contains(m.output, "removed") {
		t.Fatal("removed capability did not choose/explain nearest survivor")
	}
}
func TestMouseRightPaneDoesNotMoveCapabilityAndFooterQuitMatchesText(t *testing.T) {
	m, msg := homeFixture()
	for i := 0; i < 30; i++ {
		msg.mcps = append(msg.mcps, mcp.Instance{Key: state.Key{Source: "one", Package: "inspect", Target: fmt.Sprintf("target-%02d", i)}})
	}
	m.Update(msg)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.View()
	left := m.home.Capabilities.ID
	m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelDown})
	m.Update(tea.MouseClickMsg{X: 60, Y: 5, Button: tea.MouseLeft})
	if m.home.Capabilities.ID != left || m.home.Focus != ProfilesPane || m.home.Profiles.Index != 1 {
		t.Fatalf("mouse selected wrong pane/row: %#v", m.home)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	view := m.View().Content
	lines := strings.Split(view, "\n")
	y := len(lines) - 2
	x := strings.Index(lines[y], "F10")
	// All preceding footer glyphs occupy one terminal cell; rune count handles arrows.
	x = len([]rune(lines[y][:x]))
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("click on visible F10 Quit did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("wrong footer action")
	}
}
func TestHomeMouseMarksOnlyCapabilityAndModalRetainsFocus(t *testing.T) {
	m, _ := homeFixture()
	m.View()
	m.Update(tea.MouseClickMsg{X: 4, Y: 4, Button: tea.MouseLeft})
	if !m.home.Marks[m.home.Capabilities.ID] {
		t.Fatal("mark click not applied")
	}
	press(m, tea.KeyTab, "")
	id := m.home.Profiles.ID
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEscape, "")
	if m.home.Focus != ProfilesPane || m.home.Profiles.ID != id {
		t.Fatal("modal changed origin")
	}
	press(m, tea.KeySpace, " ")
	if len(m.home.Marks) != 1 {
		t.Fatal("profile got a batch mark")
	}
}
func TestResizedViewsFitTerminalAndEmptyExplanationIsReadable(t *testing.T) {
	m, _ := homeFixture()
	press(m, tea.KeyDown, "")
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 16}, {Width: 100, Height: 24}} {
		m.Update(size)
		v := m.View()
		lines := strings.Split(v.Content, "\n")
		if len(lines) > size.Height {
			t.Fatalf("view height %d exceeds %d", len(lines), size.Height)
		}
		for _, line := range lines {
			if len([]rune(line)) > size.Width {
				t.Fatal("row exceeds width", line)
			}
		}
		if !strings.Contains(v.Content, "skill-only capability") {
			t.Fatal("empty explanation was truncated", v.Content)
		}
	}
}

func TestRemoveRegistrationsMenuCannotUninstallCapability(t *testing.T) {
	for _, ownership := range []string{"local", "foreign"} {
		for _, input := range []string{"keyboard", "mouse"} {
			t.Run(ownership+"/"+input, func(t *testing.T) {
				m, msg := homeFixture()
				msg.catalog[0].Skill = &catalog.Skill{Name: "companion"}
				msg.mcps[0].Ownership = ownership
				m.Update(msg)
				m.focusPane(ProfilesPane)
				press(m, tea.KeyEnter, "")
				m.home.Modal.Selected = 6
				var cmd tea.Cmd
				if input == "keyboard" {
					_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				} else {
					m.View()
					found := false
					for _, hit := range m.home.Hits {
						if hit.Control == "menu" && hit.Index == 6 {
							found = true
							_, cmd = m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
							break
						}
					}
					if !found {
						t.Fatal("remove registrations menu hit region missing")
					}
				}
				if cmd != nil || m.form != nil || m.busy || m.pending.action == "uninstall" {
					t.Fatalf("registration removal dispatched or opened uninstall: form=%v busy=%v pending=%q", m.form != nil, m.busy, m.pending.action)
				}
				if m.home.Modal == nil || !strings.Contains(m.menuEntries()[6], "disabled") {
					t.Fatal("registration removal must remain visibly disabled")
				}
				if !strings.Contains(m.output, "registration-only") {
					t.Fatal("disabled action must explain missing registration-only service support")
				}
			})
		}
	}
}
