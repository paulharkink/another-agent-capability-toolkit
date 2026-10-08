package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// uxJourneyCases maps each approved journey to its production route assertion.
// Separate top-level tests keep every journey independently discoverable.
var uxJourneyCases = []struct {
	id  string
	ref string
	run func(*testing.T)
}{
	{"01", "target chooser and exact target selection", TestUXMultipleTargetsOpenLocalChooserAndSelectionOpensCorrectKey},
	{"02", "paired workspace and package/target facts", TestUXCapabilityAndTargetDetailsContainRealInformation},
	{"03", "shared workspace return to Environments", TestUXEnvironmentTargetUsesSharedInsetEditorAndBackRestoresParent},
	{"04", "keyboard in-place edit, Unicode paste and save", editPasteJourney},
	{"05", "authentication alternatives and task grouping", TestUXGrafanaSessionCookieInputsAllInAuthenticationAndIrrelevantCredentialsConditional},
	{"06", "foreground failure and draft recovery", TestUXReturnToConfigurationPreservesSubmittedDraftAndOrigin},
	{"07", "database choices and named destinations", TestInteractionSetupShowsDatabaseAndNamedMCPDestinationsInRightPane},
	{"08", "named agent destination and exact path", TestUXForeignEndpointCanRegisterWithoutRuntimeControl},
	{"09", "headless embedded picker cancel preserves workspace field and draft", TestUXActivePickerOwnsParentShortcutKeys},
	{"10", "directory collection editing", directoryCollectionJourney},
	{"11", "foreign runtime connection diagnosis and source lookup", TestUXObservedForeignProfileWithoutCatalogPackageKeepsDiagnosisAndRequiresSourceForBinding},
	{"12", "runtime empty state and log lifecycle", TestUXNoContainerLogsExplainsNextStepWithoutCallingDockerLogs},
	{"13", "preset versus saved target navigation", TestUXEnvironmentPresetDoesNotImplyInstalledAndNoSavedDuplicateRows},
	{"14", "agent detection and write destination", TestUXAgentsOverviewEnterFocusesDetailsAndDetailsShowsResolution},
	{"15", "management, settings and help hierarchy", TestUXSettingsCategoriesHaveRelatedControlsOnly},
	{"16", "pane-local keyboard navigation and scroll cues", TestHomeLayerTwoScrollShowsContinuationCues},
	{"17", "capability-specific authentication sections", TestUXAzureAndForgejoTaskSections},
}

func runUXJourney(t *testing.T, id string) {
	t.Helper()
	for _, journey := range uxJourneyCases {
		if journey.id == id {
			t.Run(journey.ref, journey.run)
			return
		}
	}
	t.Fatalf("journey %s is not mapped", id)
}

func TestUXJourney01(t *testing.T) { runUXJourney(t, "01") }
func TestUXJourney02(t *testing.T) { runUXJourney(t, "02") }
func TestUXJourney03(t *testing.T) { runUXJourney(t, "03") }
func TestUXJourney04(t *testing.T) { runUXJourney(t, "04") }
func TestUXJourney05(t *testing.T) { runUXJourney(t, "05") }
func TestUXJourney06(t *testing.T) { runUXJourney(t, "06") }
func TestUXJourney07(t *testing.T) { runUXJourney(t, "07") }
func TestUXJourney08(t *testing.T) { runUXJourney(t, "08") }
func TestUXJourney09(t *testing.T) { runUXJourney(t, "09") }
func TestUXJourney10(t *testing.T) { runUXJourney(t, "10") }
func TestUXJourney11(t *testing.T) { runUXJourney(t, "11") }
func TestUXJourney12(t *testing.T) { runUXJourney(t, "12") }
func TestUXJourney13(t *testing.T) { runUXJourney(t, "13") }
func TestUXJourney14(t *testing.T) { runUXJourney(t, "14") }
func TestUXJourney15(t *testing.T) { runUXJourney(t, "15") }
func TestUXJourney16(t *testing.T) { runUXJourney(t, "16") }
func TestUXJourney17(t *testing.T) { runUXJourney(t, "17") }

func TestUXFocusedPaneShowsOffscreenCueAndBoundedButtonsAtAllSupportedSizes(t *testing.T) {
	defs := make([]catalog.Input, 60)
	fields := make([]string, len(defs))
	for i := range defs {
		fields[i] = fmt.Sprintf("field_%02d", i)
		defs[i] = catalog.Input{Name: fields[i], Label: fmt.Sprintf("Field %02d", i), Type: "string"}
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 16}, {Width: 100, Height: 23}, {Width: 170, Height: 42}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			m := forms.NewForm(t.Context(), defs, nil)
			m.SetSections(forms.FormSection{Title: "Connection", Fields: fields})
			m.Update(size)
			view := ansi.Strip(m.View().Content)
			if got := len(strings.Split(view, "\n")); got > size.Height {
				t.Fatalf("rendered form exceeds terminal height: %d > %d\n%s", got, size.Height, view)
			}
			for _, button := range []string{"[ Save ]", "[ Cancel ]"} {
				if !strings.Contains(view, button) {
					t.Errorf("action button %q is not visible at %dx%d:\n%s", button, size.Width, size.Height, view)
				}
			}
			if !strings.Contains(view, "↓ more") {
				t.Errorf("overflow has no visible below cue at %dx%d:\n%s", size.Width, size.Height, view)
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
			for i := 0; i < len(fields)-1; i++ {
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			view = ansi.Strip(m.View().Content)
			if !strings.Contains(view, "Field 59") || !strings.Contains(view, "↑ more") || !strings.Contains(view, "[ Save ]") {
				t.Errorf("bottom selection, above cue, or fixed action bar is missing at %dx%d:\n%s", size.Width, size.Height, view)
			}
		})
	}
	main, _ := homeFixture()
	main.Update(tea.WindowSizeMsg{Width: 79, Height: 15})
	if got := ansi.Strip(main.View().Content); !strings.Contains(got, "80×16") {
		t.Fatalf("below-minimum main terminal does not explain how to recover:\n%s", got)
	}
	workspace := openWorkspaceGeometryFixture(t, tea.WindowSizeMsg{Width: 80, Height: 16})
	view := ansi.Strip(workspace.View().Content)
	if got := len(strings.Split(view, "\n")); got > 16 {
		t.Fatalf("80x16 workspace exceeds the parent terminal height: %d\n%s", got, view)
	}
	if !strings.Contains(view, "[ Save and apply ]") || strings.Contains(view, "[ Cancel ]") {
		t.Errorf("80x16 workspace should show only its bottom Save and apply action:\n%s", view)
	}
	if !strings.Contains(view, "Esc Back") {
		t.Errorf("80x16 workspace footer clips its Back hint:\n%s", view)
	}
	titleRow := -1
	for index, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Install · Demo") {
			titleRow = index
			break
		}
	}
	if titleRow < 4 {
		t.Fatalf("paired workspace overlay starts above the main-screen pane header: row=%d\n%s", titleRow, view)
	}
	workspace.Update(tea.WindowSizeMsg{Width: 79, Height: 15})
	if got := ansi.Strip(workspace.View().Content); !strings.Contains(got, "80×16") {
		t.Errorf("active workspace below minimum does not explain resize recovery:\n%s", got)
	}
}

func TestUXFormSplitPaneFocusHasStrongTitlesAndSelectedRows(t *testing.T) {
	form := forms.NewForm(t.Context(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "https://cluster.fixture.invalid"})
	form.SetSections(forms.FormSection{Title: "Overview"}, forms.FormSection{Title: "Connection", Fields: []string{"endpoint"}})
	form.SelectSection("Connection")
	form.Update(tea.WindowSizeMsg{Width: 100, Height: 23})
	marker := "\x1b[38;2;8;31;91;48;2;233;242;251m"
	view := form.View().Content
	form.SetSectionHeading("Workspace sections")
	view = form.View().Content
	if strings.Contains(ansi.Strip(view), "L3") || strings.Contains(ansi.Strip(view), "L4") || strings.Contains(ansi.Strip(view), "FOCUSED") || !strings.Contains(ansi.Strip(view), "Workspace sections") || !strings.Contains(ansi.Strip(view), "── Connection") {
		t.Fatalf("pane titles should use ordinary configured titles:\n%s", ansi.Strip(view))
	}
	if !strings.Contains(view, marker+"> Connection") {
		t.Fatalf("focused L3 selection lacks a distinct row style:\n%s", ansi.Strip(view))
	}
	form.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	view = form.View().Content
	if strings.Contains(ansi.Strip(view), "L3") || strings.Contains(ansi.Strip(view), "L4") || strings.Contains(ansi.Strip(view), "FOCUSED") || !strings.Contains(ansi.Strip(view), "Workspace sections") || !strings.Contains(ansi.Strip(view), "── Connection") {
		t.Fatalf("pane titles should remain ordinary after focus changes:\n%s", ansi.Strip(view))
	}
	if !strings.Contains(view, marker+"> Endpoint") {
		t.Fatalf("focused L4 field lacks a distinct row style:\n%s", ansi.Strip(view))
	}
}

func TestUXInactiveAuthenticationAlternativeIsVisiblyMutedAndFocusable(t *testing.T) {
	form := forms.NewForm(t.Context(), []catalog.Input{
		{Name: "token", Label: "Token", Type: "secret", ExclusiveGroup: "auth"},
		{Name: "kubeconfig", Label: "Source kubeconfig", Type: "file", ExclusiveGroup: "auth"},
	}, map[string]any{"token": "fixture-token"})
	form.SetSections(forms.FormSection{Title: "Authentication", Fields: []string{"token", "kubeconfig"}})
	form.SetExclusiveFields("token", "kubeconfig")
	form.Update(tea.WindowSizeMsg{Width: 100, Height: 23})
	form.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	view := form.View().Content
	if !strings.Contains(view, "38;2;168;189;219m") {
		t.Fatalf("inactive kubeconfig alternative is not visibly muted while Token is active:\n%s", ansi.Strip(view))
	}
	form.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	view = form.View().Content
	if !strings.Contains(ansi.Strip(view), "> Source kubeconfig") || !strings.Contains(view, "48;2;233;242;251m") {
		t.Fatalf("muted kubeconfig alternative is not keyboard-focusable with active focus contrast:\n%s", ansi.Strip(view))
	}
}

func TestUXMinimumWorkspaceWarningBlocksHiddenInputAndRetainsDraft(t *testing.T) {
	m := openWorkspaceGeometryFixture(t, tea.WindowSizeMsg{Width: 80, Height: 16})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // Connection section.
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	before := ansi.Strip(m.form.View().Content)
	if !strings.Contains(before, "fixturex") {
		t.Fatalf("fixture did not enter the existing field before resize:\n%s", before)
	}
	m.Update(tea.WindowSizeMsg{Width: 79, Height: 15})
	warning := m.View()
	if !strings.Contains(ansi.Strip(warning.Content), "Resize to continue") || strings.Contains(ansi.Strip(warning.Content), "[ Save ]") {
		t.Fatalf("below-minimum warning exposes hidden form controls:\n%s", ansi.Strip(warning.Content))
	}
	if !strings.Contains(warning.Content, "48;2;9;38;111m") || defaultBackgroundGlyphs(warning.Content) != 0 {
		t.Fatalf("below-minimum warning lost the application's blue canvas:\n%q", warning.Content)
	}
	_, hiddenKey := m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if hiddenKey != nil {
		t.Fatal("ordinary typing behind the recovery warning triggered an action")
	}
	if m.form == nil || !strings.Contains(ansi.Strip(m.form.View().Content), "fixturex") {
		t.Fatal("typing q behind the warning changed/closed the hidden draft")
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
	if quit != nil || !strings.Contains(ansi.Strip(m.View().Content), "Apply changes") {
		t.Fatal("F10 on the dirty recovery screen did not show the unsaved changes popup")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // Keep editing.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	if !strings.Contains(ansi.Strip(m.View().Content), "fixturex") {
		t.Fatalf("resizing back discarded the in-place field draft:\n%s", ansi.Strip(m.View().Content))
	}
}

func TestUXOperationCompletionAtSmallSizeRetainsForegroundResultAcrossResize(t *testing.T) {
	m, _ := openSetupInteraction(t)
	_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil || !m.busy {
		t.Fatal("fixture did not submit a real setup operation")
	}
	m.Update(tea.WindowSizeMsg{Width: 79, Height: 15})
	_, refresh := m.Update(runTeaCmd(t, m, save))
	if m.result == nil || m.busy || !strings.Contains(ansi.Strip(m.View().Content), "Result · SUCCESS · need 80×16") || !strings.Contains(ansi.Strip(m.View().Content), "Tab/Enter · Esc close") {
		t.Fatalf("worker completion/result controls were lost behind the below-minimum viewport: busy=%t result=%+v\n%s", m.busy, m.result, ansi.Strip(m.View().Content))
	}
	if refresh != nil {
		m.Update(refresh())
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.result == nil || !strings.Contains(ansi.Strip(m.View().Content), "Operation result") {
		t.Fatalf("foreground result did not survive resizing back to a supported size: result=%+v\n%s", m.result, ansi.Strip(m.View().Content))
	}
	_, closeResult := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if closeResult != nil || m.result != nil {
		t.Fatalf("result screen did not own its documented Enter/close action after resize: cmd=%v result=%+v", closeResult != nil, m.result)
	}
}

func TestUXLocateSourceOpensCenteredTaskSpecificRecoveryForm(t *testing.T) {
	m, _ := homeFixture()
	m.profileSnapshot = &viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{{Key: state.Key{Source: "gone", Package: "legacy", Target: "default"}, Name: "Old capability"}}}
	rows := m.capabilities()
	m.home.Capabilities.ID = rows[len(rows)-1].ID
	m.reconcileHome()
	m.focusPane(ProfilesPane)
	m.selectContext(0) // Locate source…
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form == nil || m.pending.action != "locate-source" || m.pending.source != "gone" {
		t.Fatalf("L2 Locate source did not preserve the unavailable source identity: form=%v pending=%+v", m.form != nil, m.pending)
	}
	if !m.management.FormOverlay {
		t.Fatal("Locate source is not marked as a centered management overlay")
	}
	x, y, width, height, ok := m.setupOverlayBounds()
	if !ok || y < 1 || width >= m.width || height >= m.height {
		t.Fatalf("Locate source did not use narrow overlay geometry: bounds=(%d,%d %dx%d) ok=%t", x, y, width, height, ok)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Locate source · gone", "Capabilities"} {
		if !strings.Contains(view, want) {
			t.Errorf("Locate source overlay lost %q or its L1/L2 context:\n%s", want, view)
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 79, Height: 15})
	belowMinimum := ansi.Strip(m.View().Content)
	if !strings.Contains(belowMinimum, "Resize to continue") || strings.Contains(belowMinimum, "Source checkout directory") {
		t.Fatalf("below-minimum Locate source form exposed hidden controls instead of recovery:\n%s", belowMinimum)
	}
}

func TestUXObservedForeignProfileWithoutCatalogPackageKeepsDiagnosisAndRequiresSourceForBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("PATH", t.TempDir())
	key := state.Key{Source: "external-runtime", Package: "absent-package", Environment: "remote", Target: "observed"}
	endpoint := "http://127.0.0.1:1/mcp"
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProfile(state.ProfileRecord{Key: key, Name: "Observed inspector"}); err != nil {
		t.Fatal(err)
	}
	runtime := &journeyForeignRuntime{instance: mcp.Instance{Key: key, Name: "Observed inspector", Status: "running", URL: endpoint, Ownership: "other-aact"}}
	svc := app.New(config.Source{ID: "local-source", Root: t.TempDir(), Catalog: nil}, store, app.Options{Runtime: runtime})
	m := NewContext(t.Context(), svc)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.Update(m.Init()())
	if got := m.profileSnapshot; got == nil || len(got.Profiles) != 1 || got.Profiles[0].Key != key || got.Profiles[0].URL != endpoint || got.Profiles[0].Ownership != "other-aact" {
		t.Fatalf("real service did not expose the exact foreign runtime facts: %+v", got)
	}
	capabilities := m.capabilities()
	if len(capabilities) != 1 || capabilities[0].CatalogIndex >= 0 {
		t.Fatalf("fixture accidentally supplied a local catalog package: %+v", capabilities)
	}
	m.home.Capabilities.ID = capabilities[0].ID
	m.reconcileHome()
	profileIndex := -1
	for i, row := range m.contextRows() {
		if row.Kind == "profile" && row.ProfileIndex >= 0 {
			profileIndex = i
			break
		}
	}
	if profileIndex < 0 {
		t.Fatal("observed foreign profile was not reachable in the selected capability's L2")
	}
	m.focusPane(ProfilesPane)
	m.selectContext(profileIndex)
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.result != nil || m.form == nil || m.workspace == nil || m.workspace.Key != key || m.workspace.Profile == nil || m.workspace.Profile.URL != endpoint {
		t.Fatalf("foreign endpoint route did not preserve observed target into its workspace: result=%+v workspace=%+v output=%q", m.result, m.workspace, m.output)
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Overview", "Endpoint", "Agents", "Information"} {
		if !strings.Contains(view, want) {
			t.Errorf("read-only observed workspace is missing section %q:\n%s", want, view)
		}
	}
	for _, forbidden := range []string{"Configure named registrations", "Remove named registrations", "Start MCP", "Stop MCP"} {
		if strings.Contains(view, forbidden) {
			t.Errorf("observed-only workspace offers unsupported action %q:\n%s", forbidden, view)
		}
	}
	m.workspace.SectionID = sectionEndpointID
	m.form.SelectSectionID(sectionEndpointID)
	m.form.FocusSection()
	_, check := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if check != nil {
		m.Update(check()) // L3 selection focuses the Endpoint details pane.
	}
	_, check = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if check == nil || !strings.Contains(ansi.Strip(m.View().Content), "> [ Check connection ]") {
		t.Fatalf("Endpoint L4 does not expose its diagnostic action (cmd=%v area=%d):\n%s", check != nil, m.form.FocusArea(), ansi.Strip(m.View().Content))
	}
	_, runCheck := m.Update(check())
	if runCheck == nil {
		t.Fatal("Endpoint action did not call the connection service")
	}
	_, refresh := m.Update(runCheck())
	if refresh != nil {
		m.Update(refresh())
	}
	if m.result == nil || !strings.Contains(strings.Join(m.result.Rows, "\n"), endpoint) {
		t.Fatalf("endpoint check did not retain its exact observed URL/result: %+v", m.result)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("endpoint diagnosis changed foreign runtime lifecycle: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.result != nil {
		t.Fatalf("Esc did not dismiss endpoint diagnostic before returning to Agents: %+v", m.result)
	}
	m.workspace.SectionID = sectionAgentsID
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Locate source checkout") || !strings.Contains(view, "Complete agent bindings require") {
		t.Fatalf("Agents does not explain source requirement and recovery action:\n%s", view)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pending.action != "locate-source" || m.pending.source != key.Source || m.registration != nil {
		t.Fatalf("observed-only Agents bypassed source recovery: pending=%+v registration=%+v", m.pending, m.registration)
	}
	if runtime.starts != 0 || runtime.stops != 0 {
		t.Fatalf("agent source lookup changed foreign runtime lifecycle: starts=%d stops=%d", runtime.starts, runtime.stops)
	}
}

func openWorkspaceGeometryFixture(t *testing.T, size tea.WindowSizeMsg) *Model {
	t.Helper()
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.catalog = []catalog.Package{{ID: "demo", Name: "Demo", Dir: "/fixture/demo", MCP: &catalog.MCP{Name: "demo"}}}
	m.Update(size)
	preview := viewmodel.SetupPreview{
		Key:         state.Key{Source: "fixture-source", Package: "demo", Environment: "dev", Target: "local"},
		PackageName: "Demo", MCP: true,
		Inputs:       []viewmodel.SetupInput{{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint", Type: "string"}, Value: "fixture", HasValue: true, Editable: true}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: "/fixture/codex", Selected: true}},
	}
	m.workspace = &workspaceState{Key: preview.Key, InvokingView: "Catalog", Active: true, Section: "Overview"}
	m.openSetupForm(preview)
	return m
}

func TestUXDecorativeFactsCannotLookLikeSelectableActions(t *testing.T) {
	m := forms.NewForm(t.Context(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "http://fixture.invalid/mcp"})
	m.SetSections(forms.FormSection{Title: "Overview", Fields: []string{"endpoint"}})
	m.SetSectionContent("Overview", []string{"Ownership: unknown", "Reachability: not checked"})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Ownership: unknown") || !strings.Contains(view, "Reachability: not checked") {
		t.Fatalf("read-only facts missing from focused pane:\n%s", view)
	}
	if strings.Contains(view, "[Ownership: unknown]") || strings.Contains(view, "[Reachability: not checked]") {
		t.Fatalf("decorative facts are rendered as bracketed controls:\n%s", view)
	}
}

func TestUXUnstructuredSettingsSuccessDoesNotInventUnsuccessfulSave(t *testing.T) {
	m, _ := homeFixture()
	m.action = "save settings"
	m.Update(operationMsg{origin: "Settings", output: "Default named agents saved for future MCP installs"})
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Saved: no") || strings.Contains(view, "Applied effects: none reported") {
		t.Fatalf("a successful settings message without structured outcome was presented as failed/no effects:\n%s", view)
	}
	if !strings.Contains(view, "Default named agents saved for future MCP installs") {
		t.Fatalf("settings result omitted its actual successful output:\n%s", view)
	}
}

func TestUXSuccessfulLoadRetryEndsOnlyItsOwnProgress(t *testing.T) {
	m := fixtureModel(t)
	m.busy = false
	m.result = nil
	m.action = "load"
	m.retryOperation = func() tea.Cmd {
		m.busy = true
		m.action = "load"
		return m.load()
	}
	m.Update(loadedMsg{err: fmt.Errorf("temporary load failure")})
	if m.result == nil || !m.result.CanRetry {
		t.Fatal("failed initial load did not expose retry")
	}
	_, retry := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if retry == nil || !m.busy {
		t.Fatal("retry did not start the load operation")
	}
	m.Update(retry())
	if m.busy || m.result != nil {
		t.Fatalf("successful load retry kept its progress or stale error visible: busy=%t result=%+v", m.busy, m.result)
	}
	m.busy = true
	m.action = "install"
	m.Update(loadedMsg{}) // A post-operation refresh may finish during another action.
	if !m.busy || m.action != "install" {
		t.Fatalf("unrelated refresh cleared another operation's progress: busy=%t action=%q", m.busy, m.action)
	}
}

func TestUXSettingsHighlightsOnlyTheFocusedDetailControl(t *testing.T) {
	m := fixtureModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.navigate("Settings")
	m.management.Focus = ProfilesPane
	m.management.SettingsDetailIndex = 0 // The focused detail is a fact, not an action.
	_, right := managementPaneWidths(m.width)
	details := wrapManagementDetails(m.managementSettingsDetails(), right-1)
	actionIndex, ok := m.settingsActionIndex(details)
	if !ok || actionIndex == 0 {
		t.Fatalf("fixture did not expose a settings action after its facts: details=%v action=%d", details, actionIndex)
	}
	view := m.View().Content
	selectedMarker := strings.SplitN(managementSelected.Render("marker"), "marker", 2)[0]
	if selectedMarker == "" {
		t.Skip("terminal style output is disabled in this environment")
	}
	var actionRow, factRow string
	for _, row := range strings.Split(view, "\n") {
		plain := ansi.Strip(row)
		if strings.Contains(plain, details[actionIndex]) {
			actionRow = row
		}
		if strings.Contains(plain, details[0]) {
			factRow = row
		}
	}
	if actionRow == "" || factRow == "" {
		t.Fatalf("fixture did not render the focused fact and action: action=%q fact=%q", actionRow, factRow)
	}
	if strings.Contains(actionRow, selectedMarker+details[actionIndex]) {
		t.Fatalf("settings action retained the selection style while a fact had focus:\n%s", ansi.Strip(view))
	}
	if !strings.Contains(factRow, selectedMarker+details[0]) {
		t.Fatalf("focused fact is missing its selection style:\n%s", ansi.Strip(view))
	}
	m.management.SettingsDetailIndex = actionIndex
	view = m.View().Content
	for _, row := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(row), details[actionIndex]) {
			if !strings.Contains(row, selectedMarker+details[actionIndex]) {
				t.Fatalf("focused settings action is missing its selection style:\n%s", ansi.Strip(view))
			}
			return
		}
	}
	t.Fatalf("focused settings action disappeared:\n%s", ansi.Strip(view))
}

func TestUXCaptureProductionViewsForReview(t *testing.T) {
	for _, screen := range []struct {
		name      string
		sectionID string
		size      tea.WindowSizeMsg
	}{
		{"Overview at minimum", sectionOverviewID, tea.WindowSizeMsg{Width: 80, Height: 16}},
		{"Connection", "package:connection", tea.WindowSizeMsg{Width: 100, Height: 23}},
		{"Authentication", "package:authentication", tea.WindowSizeMsg{Width: 100, Height: 23}},
		{"Agents destinations", sectionAgentsID, tea.WindowSizeMsg{Width: 100, Height: 23}},
		{"Information", sectionInformationID, tea.WindowSizeMsg{Width: 170, Height: 42}},
	} {
		t.Run(screen.name, func(t *testing.T) {
			m, _ := actualClusterWorkspace(t, screen.size)
			if !m.form.SelectSectionID(screen.sectionID) {
				t.Fatalf("fixture did not expose declared section ID %q", screen.sectionID)
			}
			m.form.FocusSection()
			t.Logf("FIXTURE VIEW · actual packages/cluster-inspector/package.toml · %s · %dx%d\n%s", screen.name, screen.size.Width, screen.size.Height, ansi.Strip(m.View().Content))
			t.Logf("ANSI_CELLS[%s]\n%s", screen.name, m.View().Content)
			writeUXCapture(t, screen.name, m.View().Content)
			if screen.name == "Authentication" {
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp}) // Start at the top of L4 without changing L3 selection.
				if m.form.FocusArea() != 1 || m.form.SectionTitle() != "Authentication" {
					t.Fatal("detail scrolling changed the focused L3 section or surrendered L4 focus")
				}
				writeUXCapture(t, "Authentication detail page 1", m.View().Content)
				m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				if m.form.FocusArea() != 1 || m.form.SectionTitle() != "Authentication" {
					t.Fatalf("PageDown changed Authentication L3 selection or left L4 focus (area=%d section=%q)", m.form.FocusArea(), m.form.SectionTitle())
				}
				writeUXCapture(t, "Authentication detail page 2", m.View().Content)
			}
			if screen.name == "Agents destinations" {
				paths := destinationDisplayPaths(m.workspace.Preview.Destinations)
				var allAgentViews strings.Builder
				allAgentViews.WriteString(m.View().Content)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if m.form.FocusArea() != 1 {
					t.Fatal("Enter in L3 did not transfer focus to L4 for keyboard path inspection")
				}
				for i := 0; i < len(paths)*6+30; i++ {
					allAgentViews.WriteString(m.form.View().Content + "\n<FRAME>\n")
					m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
					if m.form.FocusArea() != 1 || m.form.SectionTitle() != "Agents" {
						t.Fatalf("PageDown %d changed Agents L3 selection or left L4 focus (area=%d section=%q)", i, m.form.FocusArea(), m.form.SectionTitle())
					}
					if i == 0 || i == 2 || i == 4 {
						writeUXCapture(t, fmt.Sprintf("Agents destinations page %d", i/2+1), m.View().Content)
					}
				}
				assertRenderedTextIsComplete(t, allAgentViews.String(), paths...)
			}
			if screen.name == "Information" && m.workspace.Preview.CredentialNote != "" {
				chunks := []string{"Credential note:", "Managed credential", "is missing at", "auth/kubeconfig."}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				visible := ""
				for i := 0; i < 16; i++ {
					visible += strings.ReplaceAll(ansi.Strip(m.View().Content), "\\", "/")
					allVisible := true
					for _, chunk := range chunks {
						allVisible = allVisible && strings.Contains(visible, chunk)
					}
					if allVisible {
						break
					}
					m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
					if m.form.FocusArea() != 1 || m.form.SectionTitle() != "Information" {
						t.Fatal("PageDown left the Information details panel")
					}
				}
				for _, chunk := range chunks {
					if !strings.Contains(visible, chunk) {
						t.Fatalf("Information does not show credential provenance chunk %q", chunk)
					}
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				if m.form.FocusArea() != 1 || m.form.SectionTitle() != "Information" {
					t.Fatalf("PageDown changed Information L3 selection or left L4 focus (area=%d section=%q)", m.form.FocusArea(), m.form.SectionTitle())
				}
			}
			if screen.size.Width == 80 {
				m.Update(tea.WindowSizeMsg{Width: 79, Height: 15})
				belowMinimum := m.View().Content
				t.Logf("FIXTURE VIEW · actual Cluster Inspector workspace below-minimum · 79x15\n%s", ansi.Strip(belowMinimum))
				writeUXCapture(t, "Workspace below minimum", belowMinimum)
			}
		})
	}
	settings, _ := homeFixture()
	settings.Update(tea.WindowSizeMsg{Width: 100, Height: 23})
	settings.navigate("Settings")
	settingsView := settings.View().Content
	t.Logf("FIXTURE VIEW · production Settings · 100x23\n%s", ansi.Strip(settingsView))
	writeUXCapture(t, "Settings", settingsView)

	skillOnly, _ := homeFixture()
	skillOnly.Update(tea.WindowSizeMsg{Width: 100, Height: 23})
	for _, row := range skillOnly.capabilities() {
		if row.Package == "plain" {
			skillOnly.home.Capabilities.ID = row.ID
			break
		}
	}
	skillOnly.reconcileHome()
	skillView := skillOnly.View().Content
	t.Logf("FIXTURE VIEW · skill-only capability with no MCP runtime · 100x23\n%s", ansi.Strip(skillView))
	writeUXCapture(t, "Skill only no MCP", skillView)

	failure, backend := openSetupInteraction(t)
	backend.installErr = errors.New("fixture connection rejected")
	_, submit := failure.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if submit == nil {
		t.Fatal("failure fixture did not execute the production setup submit route")
	}
	_, refresh := failure.Update(runTeaCmd(t, failure, submit))
	if refresh != nil {
		failure.Update(refresh())
	}
	if failure.result == nil || !failure.result.Failed {
		t.Fatalf("failure fixture did not reach the actual foreground operation-result view: %+v", failure.result)
	}
	failureView := failure.View().Content
	t.Logf("FIXTURE VIEW · production setup failure/recovery · 120x28\n%s", ansi.Strip(failureView))
	writeUXCapture(t, "Foreground failure", failureView)

	// Show both sides of the authentication choice using the actual package and
	// Model keyboard route. The values are synthetic fixture data; no credential
	// or native chooser is involved.
	auth, _ := actualClusterWorkspace(t, tea.WindowSizeMsg{Width: 120, Height: 28})
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}, {Code: tea.KeyRight}} {
		auth.Update(key)
	}
	const fixtureToken = "fixture-token-not-a-credential"
	for _, msg := range []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEnter}, tea.PasteStartMsg{}, tea.PasteMsg{Content: fixtureToken}, tea.PasteEndMsg{}, tea.KeyPressMsg{Code: tea.KeyEnter}} {
		auth.Update(msg)
	}
	tokenView := auth.View().Content
	if got := fmt.Sprint(auth.form.Values()["token"]); got != fixtureToken {
		t.Fatalf("keyboard paste/commit did not preserve the synthetic token fixture: %q\n%s", got, ansi.Strip(auth.View().Content))
	}
	assertInactiveAlternativeInANSI(t, tokenView, "Source kubeconfig")
	if !strings.Contains(ansi.Strip(tokenView), fixtureToken) {
		t.Fatalf("token-selected capture does not show its synthetic fixture value:\n%s", ansi.Strip(tokenView))
	}
	writeUXCapture(t, "Authentication token selected kubeconfig inactive", tokenView)

	fixtureKubeconfig := filepath.Join(t.TempDir(), "selected-kubeconfig.yaml")
	if err := os.WriteFile(fixtureKubeconfig, []byte("synthetic kubeconfig fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyDown},    // Select Source kubeconfig.
		tea.KeyPressMsg{Code: 'm', Text: "m"}, // Edit the existing/default path; never open a picker.
		tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl},
		tea.PasteStartMsg{}, tea.PasteMsg{Content: fixtureKubeconfig}, tea.PasteEndMsg{},
		tea.KeyPressMsg{Code: tea.KeyEnter},
	} {
		auth.Update(msg)
	}
	kubeconfigView := auth.View().Content
	values := auth.form.Values()
	if fmt.Sprint(values["kubeconfig"]) != fixtureKubeconfig || fmt.Sprint(values["token"]) != "" {
		t.Fatalf("keyboard edit/commit did not switch to the synthetic kubeconfig choice: %#v", values)
	}
	assertInactiveAlternativeInANSI(t, kubeconfigView, "Token")
	if !strings.Contains(ansi.Strip(kubeconfigView), "Source kubeconfig:") || !strings.Contains(ansi.Strip(kubeconfigView), "[Browse · b]") {
		t.Fatalf("kubeconfig-selected capture does not show its active field and Browse action:\n%s", ansi.Strip(kubeconfigView))
	}
	writeUXCapture(t, "Authentication kubeconfig selected token inactive", kubeconfigView)
}

func assertInactiveAlternativeInANSI(t *testing.T, view, label string) {
	t.Helper()
	muted := "\x1b[38;2;168;189;219m"
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(line), label) {
			if !strings.Contains(line, muted) {
				t.Fatalf("inactive authentication alternative %q is not visibly muted:\n%s", label, ansi.Strip(view))
			}
			return
		}
	}
	t.Fatalf("inactive authentication alternative %q is missing:\n%s", label, ansi.Strip(view))
}

func assertRenderedTextIsComplete(t *testing.T, rendered string, values ...string) {
	t.Helper()
	stripFrames := strings.NewReplacer("║", "", "│", "", "╔", "", "╗", "", "╚", "", "╝", "", "╠", "", "╣", "", "═", "", "╦", "", "╩", "")
	var frames []string
	for _, frame := range strings.Split(rendered, "<FRAME>") {
		var visibleDetails strings.Builder
		for _, line := range strings.Split(ansi.Strip(frame), "\n") {
			_, detail, hasDivider := strings.Cut(line, "│")
			if !hasDivider {
				continue
			}
			if cell, _, found := strings.Cut(detail, "║"); found {
				detail = cell
			}
			visibleDetails.WriteString(detail)
		}
		frames = append(frames, removeWhitespace(stripFrames.Replace(visibleDetails.String())))
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		want := removeWhitespace(value)
		for start := 0; start < len(want); start += 16 {
			end := min(len(want), start+32)
			chunk := want[start:end]
			found := false
			for _, frame := range frames {
				if strings.Contains(frame, chunk) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("keyboard-scrolled L4 never displays path/note chunk %q from %q:\n%s", chunk, value, ansi.Strip(rendered))
				break
			}
		}
	}
}

func removeWhitespace(value string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, value)
}

func destinationDisplayPaths(destinations []viewmodel.SetupDestination) []string {
	paths := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		for _, path := range []string{destination.ConfigPath, destination.Path} {
			if path == "" {
				continue
			}
			found := false
			for _, old := range paths {
				if old == path {
					found = true
					break
				}
			}
			if !found {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

// writeUXCapture is opt-in so ordinary tests never write artifacts. The
// capture bytes come directly from the production Bubble Tea View.
func writeUXCapture(t *testing.T, name, content string) {
	t.Helper()
	dir := os.Getenv("AACT_UX_CAPTURE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name = strings.NewReplacer(" ", "-", "/", "-").Replace(strings.ToLower(name))
	if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func actualClusterWorkspace(t *testing.T, size tea.WindowSizeMsg) (*Model, *app.Service) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := catalog.Load(filepath.Join(repoRoot, "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("PATH", t.TempDir()) // No discovery or native-picker command can run.
	environmentRoot := t.TempDir()
	key := state.Key{Source: "fixture-source", Package: pkg.ID, Environment: "qa", Target: "local"}
	targetPath := filepath.Join(environmentRoot, key.Environment, key.Package, key.Target+".toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("[cluster]\napi_server = 'https://cluster.fixture.invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProfile(state.ProfileRecord{Key: key, Name: "Fixture cluster"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.Root(), "manager"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "manager", "settings.json"), []byte(`{"agents":["opencode"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	source := config.Source{ID: key.Source, Root: repoRoot, ManifestPath: filepath.Join(repoRoot, "aact.toml"), EnvironmentRoot: environmentRoot, Catalog: []catalog.Package{pkg}, PackageDefaults: map[string]map[string]any{pkg.ID: {"local_port": 9911}}}
	svc := app.New(source, store, app.Options{Runtime: journeyEmptyRuntime{}})
	m := NewContext(t.Context(), svc)
	m.Update(size)
	m.Update(m.Init()())
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	if cmd == nil {
		t.Fatal("real app service did not expose the fixture Cluster Inspector workspace")
	}
	m.Update(cmd())
	if m.form == nil || m.workspace == nil || !m.workspace.Active {
		t.Fatalf("real Model.Update path did not open an active workspace: form=%t workspace=%+v", m.form != nil, m.workspace)
	}
	return m, svc
}

func TestUXAllScreensKeyboardOnlyNoBatch(t *testing.T) {
	// The setup route is submitted by a single keyboard Ctrl-S and the fixture
	// records one concrete SetupInstallRequest; there is no batch confirmation
	// or extra apply step between the form and service request.
	m, backend := openSetupInteraction(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("keyboard Ctrl-S did not submit the setup form")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.installRequest == nil || backend.installRequest.PackageID == "" {
		t.Fatalf("keyboard setup did not reach the real UI service boundary: %+v", backend.installRequest)
	}
	if strings.Contains(strings.ToLower(m.View().Content), "batch") {
		t.Fatalf("single-target keyboard flow exposed an unrelated batch step:\n%s", m.View().Content)
	}
}

func TestUXActivePickerOwnsParentShortcutKeys(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("safe native-unavailable injection is verified on macOS only")
	}
	// The Darwin picker first resolves `osacompile` by name. An empty test PATH
	// makes it return ErrUnavailable before it can build or open an OS dialog.
	t.Setenv("PATH", t.TempDir())
	m, _ := openSetupInteraction(t)
	m.form.SelectSection("Authentication")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // Source kubeconfig.
	spy := &journeyObservedModel{model: m, states: make(chan journeyViewState, 16)}
	p := tea.NewProgram(spy, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(100, 30))
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	finished := false
	t.Cleanup(func() {
		if !finished {
			p.Quit()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
	})
	p.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	waitJourneyView(t, spy.states, func(s journeyViewState) bool {
		return strings.Contains(s.content, "Source kubeconfig") && !strings.Contains(s.content, "Browse file")
	})
	p.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	state := waitJourneyView(t, spy.states, func(s journeyViewState) bool { return strings.Contains(s.content, "Browse file") })
	if state.section != "Authentication" {
		t.Fatalf("picker opened from the wrong section: %q\n%s", state.section, state.content)
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyF3})
	state = queryJourneyProgram(t, p)
	if state.section != "Authentication" {
		t.Fatalf("F3 escaped the active picker and changed the parent section to %q:\n%s", state.section, state.content)
	}
	if !strings.Contains(state.content, "Browse file") {
		t.Fatalf("F3 closed the active fallback picker:\n%s", state.content)
	}
	p.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	state = queryJourneyProgram(t, p)
	if state.section != "Authentication" || strings.Contains(state.content, "Browse file") || !strings.Contains(state.content, "Source kubeconfig") {
		t.Fatalf("Escape did not cancel only the browser and restore its parent field:\n%s", state.content)
	}
	p.Quit()
	select {
	case <-done:
		finished = true
	case <-time.After(3 * time.Second):
		t.Fatal("headless Bubble Tea program did not stop")
	}
}

type journeyQueryMsg struct{ reply chan journeyViewState }

type journeyViewState struct {
	content string
	section string
}

type journeyObservedModel struct {
	model  *Model
	states chan journeyViewState
}

func (m *journeyObservedModel) Init() tea.Cmd  { return nil }
func (m *journeyObservedModel) View() tea.View { return m.model.View() }
func (m *journeyObservedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if query, ok := msg.(journeyQueryMsg); ok {
		section := ""
		if m.model.form != nil {
			section = m.model.form.SectionTitle()
		} else if m.model.workspace != nil {
			section = m.model.workspace.Section
		}
		query.reply <- journeyViewState{content: ansi.Strip(m.model.View().Content), section: section}
		return m, nil
	}
	updated, cmd := m.model.Update(msg)
	m.model = updated.(*Model)
	section := ""
	if m.model.form != nil {
		section = m.model.form.SectionTitle()
	} else if m.model.workspace != nil {
		section = m.model.workspace.Section
	}
	state := journeyViewState{content: ansi.Strip(m.model.View().Content), section: section}
	select {
	case m.states <- state:
	default:
	}
	return m, cmd
}

func queryJourneyProgram(t *testing.T, p *tea.Program) journeyViewState {
	t.Helper()
	reply := make(chan journeyViewState, 1)
	p.Send(journeyQueryMsg{reply: reply})
	select {
	case state := <-reply:
		return state
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event-loop state query")
		return journeyViewState{}
	}
}

func waitJourneyView(t *testing.T, states <-chan journeyViewState, predicate func(journeyViewState) bool) journeyViewState {
	t.Helper()
	deadline := time.After(5 * time.Second)
	last := journeyViewState{}
	for {
		select {
		case state := <-states:
			last = state
			if predicate(state) {
				return state
			}
		case <-deadline:
			t.Fatalf("timed out waiting for headless Model.View transition; last section=%q view:\n%s", last.section, last.content)
		}
	}
}

func TestUXSaveErrorRecoveryPreservesActualAchievements(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	if err := os.Mkdir(filepath.Join(home, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	store, err := state.Open(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "journey-source", Package: "demo", Environment: "dev", Target: "foreign"}
	endpoint := "http://127.0.0.1:1/mcp"
	if err := store.Record(state.Installation{Key: key, AgentID: "runtime", Component: "runtime", URL: endpoint, Transport: "streamable-http"}); err != nil {
		t.Fatal(err)
	}
	environmentRoot := t.TempDir()
	targetPath := filepath.Join(environmentRoot, key.Environment, key.Package, key.Target+".toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := catalog.Package{ID: "demo", Name: "Demo", Dir: root, Skill: &catalog.Skill{Name: "demo"}, MCPs: []catalog.MCP{{Name: "demo", Transport: "streamable-http"}}, UI: &catalog.Presentation{Sections: []catalog.Section{{ID: "connection", Title: "Connection", Fields: []string{"endpoint"}}}}, Inputs: []catalog.Input{{Name: "endpoint", Label: "Endpoint URI", Type: "string", Default: endpoint}}}
	probe, err := agents.DefaultDiscoveryProbe()
	if err != nil {
		t.Fatal(err)
	}
	probe.LookPath = func(name string) (string, error) {
		switch name {
		case "codex", "claude":
			return filepath.Join(root, "bin", name), nil
		default:
			return "", os.ErrNotExist
		}
	}
	svc := app.New(config.Source{ID: key.Source, Root: root, EnvironmentRoot: environmentRoot, Catalog: []catalog.Package{pkg}, PackageDefaults: map[string]map[string]any{}}, store, app.Options{Runtime: journeySetupRuntime{}, Runner: journeyAgentRunner{}, DiscoveryProbe: &probe})
	m := NewContext(t.Context(), svc)
	m.Update(m.Init()())
	preview := viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}
	cmd := m.openTargetWorkspace(preview, "Overview")
	if cmd == nil {
		t.Fatal("could not open target workspace from the real service")
	}
	m.Update(cmd())
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	if m.form == nil || !strings.Contains(m.View().Content, "Agents") || !strings.Contains(m.View().Content, "codex") {
		t.Fatalf("manifest-backed Agents section did not open: %s", m.View().Content)
	}
	// Select Codex and Claude using only the complete Agents form controls.
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ")
	press(m, tea.KeyDown, "")
	press(m, tea.KeyDown, "")
	press(m, tea.KeySpace, " ")
	_, apply := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if apply == nil {
		t.Fatalf("keyboard Save did not call complete capability install:\n%s", m.View().Content)
	}
	m.Update(runTeaCmd(t, m, apply))
	if m.result == nil || !m.result.Failed {
		t.Fatalf("mixed registration outcome was not shown as a failure: %+v", m.result)
	}
	resultText := strings.Join(m.result.Rows, "\n")
	if !strings.Contains(resultText, "Applied · codex · mcp") || strings.Contains(resultText, "Applied · claude · mcp") {
		t.Fatalf("operation result did not distinguish the achieved Codex effect from the failed Claude effect: %s", resultText)
	}
	stored, storedErr := store.Installations()
	serviceSnapshot, serviceSnapshotErr := svc.UIProfileSnapshot(t.Context())
	var childProfile *viewmodel.Profile
	for i := range serviceSnapshot.Profiles {
		profile := &serviceSnapshot.Profiles[i]
		if profile.Key == (state.Key{Source: key.Source, Package: key.Package, Environment: key.Environment, Target: key.Target, MCP: "demo"}) {
			childProfile = profile
			break
		}
	}
	if serviceSnapshotErr != nil || childProfile == nil || len(childProfile.RegisteredAgents) != 1 || childProfile.RegisteredAgents[0] != "codex" {
		t.Fatalf("refreshed MCP child achievements do not match actual config effects: snapshot=%+v serviceSnapshot=%+v serviceSnapshotErr=%v stored=%+v storeErr=%v result=%+v output=%q", m.profileSnapshot, serviceSnapshot, serviceSnapshotErr, stored, storedErr, m.result, m.output)
	}
	if !containsInstallation(t, store, key, "codex", "mcp") {
		t.Fatal("successful Codex MCP binding was not recorded")
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Component == "mcp" && row.AgentID == "claude" {
			t.Fatalf("failed Claude registration was incorrectly recorded as achieved: %+v", row)
		}
	}
	// The complete workspace remains available after the mixed operation.
	if m.workspace == nil || !m.workspace.Active {
		t.Fatal("mixed result lost the retained workspace")
	}

}

func indexOf(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}

func containsInstallation(t *testing.T, store *state.Store, key state.Key, agentID, component string) bool {
	t.Helper()
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key.Source == key.Source && row.Key.Package == key.Package && row.Key.Environment == key.Environment && row.Key.Target == key.Target && row.AgentID == agentID && row.Component == component {
			return true
		}
	}
	return false
}

type journeySetupRuntime struct{}

type journeyAgentRunner struct{}

func (journeyAgentRunner) Run(_ context.Context, args []string, _ string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	for _, arg := range args {
		if arg == "get" {
			name := "registration"
			if len(args) > 3 {
				name = args[3]
			}
			return nil, fmt.Errorf("MCP server %q not found", name)
		}
	}
	return nil, nil
}

func (journeySetupRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	return mcp.Instance{Key: key, Status: "running", URL: fmt.Sprintf("http://%s:%d%s", spec.Host, spec.HostPort, spec.EndpointPath)}, nil
}
func (journeySetupRuntime) Stop(context.Context, state.Key) error        { return nil }
func (journeySetupRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (journeySetupRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type journeyEmptyRuntime struct{}

func (journeyEmptyRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	return mcp.Instance{}, fmt.Errorf("unexpected runtime start in registration journey")
}
func (journeyEmptyRuntime) Stop(context.Context, state.Key) error {
	return fmt.Errorf("unexpected runtime stop in registration journey")
}
func (journeyEmptyRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (journeyEmptyRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return nil, fmt.Errorf("unexpected runtime logs in registration journey")
}

type journeyForeignRuntime struct {
	instance mcp.Instance
	starts   int
	stops    int
}

func (r *journeyForeignRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	return mcp.Instance{}, fmt.Errorf("foreign runtime must not be started")
}
func (r *journeyForeignRuntime) Stop(context.Context, state.Key) error {
	r.stops++
	return fmt.Errorf("foreign runtime must not be stopped")
}
func (r *journeyForeignRuntime) List(context.Context) ([]mcp.Instance, error) {
	return []mcp.Instance{r.instance}, nil
}
func (*journeyForeignRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return nil, fmt.Errorf("foreign runtime logs are outside this journey")
}

func directoryCollectionJourney(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
	m.openSetupForm(viewmodel.SetupPreview{
		Key:          state.Key{Source: "fixture-source", Package: "demo", Environment: "dev", Target: "paths"},
		PackageName:  "Demo",
		Inputs:       []viewmodel.SetupInput{{Definition: catalog.Input{Name: "roots", Label: "Directories", Type: "directory", Multiple: true}, Value: []string{first, second}, HasValue: true, Editable: true}},
		Destinations: []viewmodel.SetupDestination{{ID: "codex", Path: filepath.Join(root, "agent"), Selected: true}},
	})
	m.form.SelectSection("Inputs")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(m.View().Content), "Edit:") {
		t.Fatalf("Model.Update Enter did not edit the focused directory row:\n%s", ansi.Strip(m.View().Content))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if fmt.Sprint(m.form.Values()["roots"]) != fmt.Sprint([]string{first, second}) {
		t.Fatalf("Backspace while editing changed collection rows before commit: %#v", m.form.Values()["roots"])
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if fmt.Sprint(m.form.Values()["roots"]) != fmt.Sprint([]string{second}) {
		t.Fatalf("Model.Update focused-row Backspace did not remove only the selected path: %#v", m.form.Values()["roots"])
	}
}

func editPasteJourney(t *testing.T) {
	want := "opaque ☃ with spaces\nsecond line"
	m, backend := openSetupInteraction(t)
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyRight},
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl},
		tea.PasteStartMsg{}, tea.PasteMsg{Content: want}, tea.PasteEndMsg{},
		tea.KeyPressMsg{Code: tea.KeyEnter},
	} {
		m.Update(msg)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("in-place Unicode edit did not submit through the production Model.Update path")
	}
	m.Update(runTeaCmd(t, m, cmd))
	if backend.installRequest == nil || backend.installRequest.Inputs["token"] != want {
		t.Fatalf("Model.Update edit/paste/save changed or lost the exact value: request=%+v", backend.installRequest)
	}
}
