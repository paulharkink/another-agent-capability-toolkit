package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

type homeWorkspaceSetupBackend struct {
	Backend
	setup *setupBackendFixture
}

func (b *homeWorkspaceSetupBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	b.setup.previewRequest = request
	key := setupProfileKey(request)
	return viewmodel.SetupPreview{
		Key:         key,
		PackageName: request.Ref.CapabilityID, Configured: true, MCP: request.Ref.CapabilityID == "inspect",
		MCPDefinitions: func() []catalog.MCP {
			if request.Ref.CapabilityID == "inspect" {
				return []catalog.MCP{{Name: "inspect"}}
			}
			return nil
		}(),
	}, nil
}

func (b *homeWorkspaceSetupBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setup.UIInstall(ctx, request)
}

func homeFixture() (*Model, loadedMsg) {
	m := NewContext(context.Background(), fixtureBackend{})
	msg := loadedMsg{settings: map[string]string{"source": "one", "checkout": "/checkout"}, labels: map[string]string{"/one": "one", "/plain": "one", "/empty": "one"}, catalog: []catalog.Package{{ID: "inspect", Name: "Inspector", Dir: "/one", MCP: &catalog.MCP{}}, {ID: "plain", Name: "Plain", Dir: "/plain", Skill: &catalog.Skill{Name: "plain"}}, {ID: "empty", Name: "Empty", Dir: "/empty", MCP: &catalog.MCP{}}}, mcps: []mcp.Instance{{Key: state.Key{Source: "one", Package: "inspect", Target: "production"}, Name: "profile", Status: "running"}, {Key: state.Key{Source: "other", Package: "inspect", Target: "wrong-source"}, Status: "running"}, {Key: state.Key{Source: "one", Package: "else", Target: "wrong-package"}, Status: "running"}}}
	msg.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "one", Package: "inspect", Target: "production"}, Name: "profile", RuntimeStatus: "running", Ownership: "local", URL: "http://127.0.0.1:8765/mcp", CanStart: true}}}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(msg)
	return m, msg
}
func TestHomeUsesApprovedBlueBackground(t *testing.T) {
	m, _ := homeFixture()
	if !strings.Contains(m.View().Content, "48;2;9;38;111m") {
		t.Fatalf("home screen lacks the approved navy blue background: prefix %q", m.View().Content[:min(100, len(m.View().Content))])
	}
}

func defaultBackgroundGlyphs(content string) int {
	painted, count := false, 0
	for i := 0; i < len(content); {
		if content[i] == '\x1b' && i+1 < len(content) && content[i+1] == '[' {
			end := strings.IndexByte(content[i:], 'm')
			if end > 0 {
				parameters := content[i+2 : i+end]
				if parameters == "" || parameters == "0" || parameters == "49" {
					painted = false
				}
				if strings.Contains(parameters, "48;2;") {
					painted = true
				}
				i += end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(content[i:])
		if r != '\n' && r != '\r' && !painted {
			count++
		}
		i += size
	}
	return count
}

func TestHomeAndOverlayHaveNoDefaultBackgroundGlyphs(t *testing.T) {
	m, _ := homeFixture()
	if got := defaultBackgroundGlyphs(m.View().Content); got != 0 {
		t.Fatalf("home has %d default-background glyphs", got)
	}
	press(m, tea.KeyF9, "")
	if got := defaultBackgroundGlyphs(m.View().Content); got != 0 {
		t.Fatalf("main menu has %d default-background glyphs", got)
	}
}

func TestHomeShowsCapabilityPackAndEnvironmentDirectory(t *testing.T) {
	m, msg := homeFixture()
	msg.settings["checkout"] = "/Users/test/sources/agent-skills"
	msg.settings["environment-root"] = "/Users/test/sources/agent-skills/environments"
	m.Update(msg)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Capability Pack: one", "Profiles: environments"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home does not reveal %q:\n%s", want, view)
		}
	}
}

func TestHomeContextDistinguishesActionsFromHeadings(t *testing.T) {
	m, _ := homeFixture()
	press(m, tea.KeyEnter, "")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"── Capability", "── Related MCP profiles", "── Installation · AACT records", "› View capability details", "profile"} {
		if !strings.Contains(view, want) {
			t.Fatalf("L2 lacks visible action or heading cue %q:\n%s", want, view)
		}
	}
}

func TestSkillOnlyContextHasNoMCPSection(t *testing.T) {
	m, _ := homeFixture()
	press(m, tea.KeyDown, "")
	press(m, tea.KeyEnter, "")
	view := ansi.Strip(m.View().Content)
	for _, unwanted := range []string{"Related MCP profiles", "No MCP profiles", "MCP ·", "related MCP profiles"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("skill-only L2 contains %q:\n%s", unwanted, view)
		}
	}
	for _, want := range []string{"Configure now", "View capability details", "Installation · AACT records"} {
		if !strings.Contains(view, want) {
			t.Fatalf("skill-only L2 missing %q:\n%s", want, view)
		}
	}
}

func TestHomeMenuHasOnlySingleCapabilityActions(t *testing.T) {
	m, _ := homeFixture()
	m.openHomeMenu("actions")
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Apply marked") || strings.Contains(view, "Mark for batch") {
		t.Fatalf("actions menu still contains batch controls:\n%s", view)
	}
}

func TestHomeHighlightsFocusedSelectionAndMenu(t *testing.T) {
	m, msg := homeFixture()
	m.Update(msg)
	view := m.View().Content
	if !strings.Contains(view, "48;2;233;242;251m") {
		t.Fatal("selected capability lacks the light highlight")
	}
	if !strings.Contains(view, "38;2;255;227;138m") {
		t.Fatal("pane title lacks the approved gold accent")
	}
	m.home.Modal = &modalState{Kind: "actions"}
	if !strings.Contains(m.View().Content, "48;2;233;242;251m") {
		t.Fatal("Actions menu lacks a visible selected item")
	}
}
func TestHomeTwoPanesFilterAndEmptyStates(t *testing.T) {
	m, _ := homeFixture()
	v := m.View().Content
	for _, s := range []string{"Capabilities", "Inspector", "profile", "Main menu", "F2 Open / Focus"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q: %s", s, v)
		}
	}
	for _, s := range []string{"wrong-source", "wrong-package"} {
		if strings.Contains(v, s) {
			t.Errorf("unrelated profile %q visible", s)
		}
	}
	if got := m.profiles(); len(got) != 1 || got[0].Key.Target != "production" {
		t.Fatalf("related profile filtering changed: %#v", got)
	}
	if got := m.profiles()[0].Status; got != "running" {
		t.Fatalf("runtime observation was lost from the selected profile: %q", got)
	}
	press(m, tea.KeyDown, "")
	if strings.Contains(m.View().Content, "Related MCP profiles") {
		t.Fatal("skill-only context still shows MCP content", m.View().Content)
	}
	press(m, tea.KeyTab, "")
	if m.view != "Catalog" || m.home.Focus != ProfilesPane {
		t.Fatal("Tab did not focus the context list")
	}
	press(m, tea.KeyDown, "")
	if !strings.Contains(m.View().Content, "Configure now") {
		t.Fatal(m.View().Content)
	}
}
func TestHomePaneFocusAndStableScrolling(t *testing.T) {
	m, msg := homeFixture()
	for i := 0; i < 30; i++ {
		key := state.Key{Source: "one", Package: "inspect", Target: fmt.Sprintf("target-%02d", i)}
		msg.mcps = append(msg.mcps, mcp.Instance{Key: key, Status: "running"})
		msg.profileSnapshot.Profiles = append(msg.profileSnapshot.Profiles, viewmodel.Profile{Key: key, Name: key.Target, RuntimeStatus: "running", Ownership: "local"})
	}
	m.Update(msg)
	press(m, tea.KeyEnter, "")
	lastProfileRow := len(m.contextRows()) - 1
	m.selectContext(lastProfileRow)
	if m.home.Context.Index != lastProfileRow {
		t.Fatal("could not select the last related profile")
	}
	press(m, tea.KeyLeft, "")
	press(m, tea.KeyRight, "")
	if m.home.Context.Index != lastProfileRow {
		t.Fatal("focus lost context selection")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.Update(msg)
	if m.home.Context.Index != lastProfileRow {
		t.Fatal("resize/refresh lost selection")
	}
	msg.mcps = msg.mcps[:len(msg.mcps)-1]
	msg.profileSnapshot.Profiles = msg.profileSnapshot.Profiles[:len(msg.profileSnapshot.Profiles)-1]
	m.Update(msg)
	if m.home.Context.Index != lastProfileRow-1 || !strings.Contains(m.output, "removed") {
		t.Fatal("deleted selected profile not explained", m.View().Content)
	}
	press(m, tea.KeyHome, "")
	if m.home.Context.Index != 0 {
		t.Fatal("Home failed to select first context row")
	}
	press(m, tea.KeyPgDown, "")
	if m.home.Context.Index == 0 {
		t.Fatal("PageDown did not move")
	}
}
func TestHomeMenuDestinationsAndReturn(t *testing.T) {
	for i, name := range []string{"Agents", "Settings", "Help"} {
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
		if m.view != "Catalog" || m.home.Focus != CapabilitiesPane || m.home.Capabilities.Index != 0 {
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
	if !strings.Contains(m.View().Content, "Main menu") {
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
	m.Update(tea.MouseClickMsg{X: 60, Y: 6, Button: tea.MouseLeft})
	if m.home.Capabilities.ID != left || m.home.Focus != ProfilesPane || m.home.Context.Index != 1 {
		t.Fatalf("mouse selected wrong pane/row: %#v", m.home)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	view := m.View().Content
	lines := strings.Split(view, "\n")
	y := len(lines) - 2
	x := strings.Index(ansi.Strip(lines[y]), "F10")
	// All preceding footer glyphs occupy one terminal cell; rune count handles arrows.
	x = len([]rune(ansi.Strip(lines[y])[:x]))
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("click on visible F10 Quit did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("wrong footer action")
	}
}
func TestHomeMouseSelectsCapabilityWithoutMarkingAndModalRetainsFocus(t *testing.T) {
	m, _ := homeFixture()
	m.backend = &homeWorkspaceSetupBackend{Backend: m.backend, setup: &setupBackendFixture{}}
	m.View()
	m.Update(tea.MouseClickMsg{X: 4, Y: 4, Button: tea.MouseLeft})
	if strings.Contains(ansi.Strip(m.View().Content), "[x]") {
		t.Fatal("capability click marked a batch")
	}
	press(m, tea.KeyTab, "")
	m.selectContext(2)
	id := m.home.Profiles.ID
	pressAndRun(m, tea.KeyF2)
	if m.form == nil || m.workspace == nil {
		t.Fatal("F2 did not open the selected target workspace")
	}
	pressAndRun(m, tea.KeyEscape)
	if m.home.Focus != ProfilesPane || m.home.Profiles.ID != id {
		t.Fatal("modal changed origin")
	}
	press(m, tea.KeySpace, " ")
	if strings.Contains(ansi.Strip(m.View().Content), "Apply marked") {
		t.Fatal("Space exposed batch controls")
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
			if lipgloss.Width(line) > size.Width {
				t.Fatal("row exceeds width", line)
			}
		}
		if size.Height == 16 && !strings.Contains(v.Content, "More below") {
			t.Fatal("short view omitted its scroll cue", v.Content)
		}
		if size.Height > 16 && !strings.Contains(v.Content, "Configure now") {
			t.Fatal("skill-only action was truncated", v.Content)
		}
	}
}

func TestRemoveRegistrationsMenuCannotUninstallCapability(t *testing.T) {
	for _, ownership := range []string{"local", "foreign"} {
		t.Run(ownership, func(t *testing.T) {
			m, backend := typedProfileFixture()
			backend.snapshot.Profiles[1].Ownership = ownership
			backend.snapshot.Profiles[1].RegisteredAgents = []string{"claude"}
			m.Update(m.load()())
			openRegistrationWorkspaceAction(t, m, true)
			if m.registration != nil || m.form == nil || !m.form.HasSectionID(sectionAgentsID) || m.busy || backend.request != nil || m.pending.action == "uninstall" {
				t.Fatalf("agent management did not use the complete capability workspace: registration=%+v form=%v busy=%v pending=%q", m.registration, m.form != nil, m.busy, m.pending.action)
			}
			if strings.Contains(m.View().Content, "Uninstall capability") || strings.Contains(m.View().Content, "Remove registrations") {
				t.Fatal("separate registration removal flow remained visible")
			}
		})
	}
}

func TestHomeRoutesProfileToApprovedWorkspace(t *testing.T) {
	m, _ := homeFixture()
	press(m, tea.KeyF9, "")
	if got := m.menuEntries(); !reflect.DeepEqual(got, []string{"Agents", "Settings", "Help", "Back"}) {
		t.Fatalf("main menu: %v", got)
	}
	press(m, tea.KeyEscape, "")
	press(m, tea.KeyF2, "")
	if m.home.Focus != ProfilesPane || m.home.Modal != nil {
		t.Fatal("F2 should focus the context list before opening an action")
	}
	for _, want := range []string{"Configure now", "View capability details", "MCP · profile", "Installation · AACT records"} {
		if !strings.Contains(m.View().Content, want) {
			t.Fatalf("context list missing %q:\n%s", want, m.View().Content)
		}
	}
	m.selectContext(2)
	m.backend = &homeWorkspaceSetupBackend{Backend: m.backend, setup: &setupBackendFixture{}}
	pressAndRun(m, tea.KeyF2)
	if m.form == nil || m.workspace == nil || m.form.SectionTitle() != "Overview" {
		t.Fatalf("F2 on a target did not open the shared Overview workspace: form=%v workspace=%+v", m.form != nil, m.workspace)
	}
	for _, want := range []string{"Configure ·", "Overview", "Agents", "Runtime", "Logs", "Information"} {
		if !strings.Contains(ansi.Strip(m.View().Content), want) {
			t.Fatalf("shared target workspace omitted %q:\n%s", want, ansi.Strip(m.View().Content))
		}
	}
}

func TestDisabledProfileOverviewStartStaysInactiveAndExplainsReason(t *testing.T) {
	m, msg := homeFixture()
	msg.mcps[0].Ownership = "foreign"
	msg.profileSnapshot.Profiles[0].Ownership = "other-aact"
	msg.profileSnapshot.Profiles[0].StartDisabledReason = "Owned by another installation"
	m.Update(msg)
	m.backend = &homeWorkspaceSetupBackend{Backend: m.backend, setup: &setupBackendFixture{}}
	m.selectContext(2)
	pressAndRun(m, tea.KeyF2)
	if m.form == nil || m.workspace == nil {
		t.Fatalf("F2 did not open the shared workspace: form=%v workspace=%+v", m.form != nil, m.workspace)
	}
	handled, cmd := m.workspaceOverviewAction("s")
	if !handled || cmd != nil || m.busy || !strings.Contains(strings.ToLower(m.output), "another installation") {
		t.Fatalf("disabled Start activated or hid reason: handled=%t cmd=%v busy=%t output=%q", handled, cmd != nil, m.busy, m.output)
	}
}

func TestHomeTopControlsAndContextPaneFocus(t *testing.T) {
	m, _ := homeFixture()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"AACT · Another Agent Capability Toolkit", "F9 Main menu: Agents | Settings | Help", "F2 Open / Focus"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home missing %q:\n%s", want, view)
		}
	}
	m.focusPane(ProfilesPane)
	m.View()
	x := -1
	for _, hit := range m.home.Hits {
		if hit.Control == "actions" && hit.Y == 1 {
			x = hit.X + 1
			break
		}
	}
	if x < 0 {
		t.Fatal("top Actions control has no hit region")
	}
	if cmd := m.homeMouse(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft}); cmd != nil {
		t.Fatal("Actions click dispatched unexpected command")
	}
	if m.home.Modal != nil || m.home.Focus != ProfilesPane {
		t.Fatal("top Open / Focus control did not focus the context list")
	}
}
