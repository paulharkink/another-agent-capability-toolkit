package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func registrationExitFixture(t *testing.T) (*Model, *profileBackend, *registrationState) {
	t.Helper()
	m, backend := typedProfileFixture()
	profile := backend.snapshot.Profiles[1]
	m.workspace = &workspaceState{
		Key:    profile.Key,
		Active: true,
		Draft:  map[string]any{"endpoint": "underlying setup draft"},
	}
	m.form = forms.NewForm(t.Context(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "underlying setup draft"})
	m.agents = []string{"codex", "opencode"}
	m.openRegistrationOverlay(ProfileRow{Key: profile.Key, URL: profile.URL, Profile: &profile}, false)
	if m.registration == nil {
		t.Fatal("registration overlay did not open")
	}
	return m, backend, m.registration
}

func dirtyRegistration(r *registrationState) {
	if len(r.Agents) == 0 {
		return
	}
	id := r.Agents[0]
	r.Marked[id] = !r.Marked[id]
}

func TestRegistrationDirtyEscShowsSharedExitPromptAndDiscardKeepsSetupDraft(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyRegistration(r)
	if !r.HasUnsavedChanges() {
		t.Fatal("registration edit was not detected")
	}
	r.Area = 1
	m.registrationKey("esc") // Panel navigation remains unguarded.
	if m.unsavedExit != nil || r.Area != 0 {
		t.Fatal("panel navigation opened the exit prompt")
	}
	m.registrationKey("esc") // Leaving the registration overlay is guarded.
	if m.unsavedExit == nil || m.registration != r {
		t.Fatal("dirty registration Esc did not open exit prompt while retaining draft")
	}
	if cmd := m.chooseUnsavedExit(1); cmd != nil {
		t.Fatal("discarding registration edits unexpectedly started work")
	}
	if m.registration != nil {
		t.Fatal("Discard changes retained the registration overlay")
	}
	if m.workspace == nil || !m.workspace.Active || m.workspace.Draft["endpoint"] != "underlying setup draft" {
		t.Fatalf("discarding registration edits changed the underlying setup draft: %+v", m.workspace)
	}
}

func TestRegistrationExitApplyFailureShowsErrorAndKeepsDirtyOverlayEditable(t *testing.T) {
	m, backend, r := registrationExitFixture(t)
	dirtyRegistration(r)
	backend.err = errors.New("synthetic registration write failure")
	m.registrationKey("esc")
	cmd := m.chooseUnsavedExit(0)
	if cmd == nil {
		t.Fatal("Apply changes did not start registration operation")
	}
	m.Update(cmd())
	if m.registration != r || !r.HasUnsavedChanges() {
		t.Fatal("failed Apply did not retain dirty registration draft")
	}
	if m.result == nil || !m.unsavedExitFailure || !strings.Contains(m.View().Content, "synthetic registration write failure") {
		t.Fatalf("failed Apply did not show the actual backend error: result=%+v view=%s", m.result, m.View().Content)
	}
	m.closeResult()
	if m.registration != r || m.result != nil || !r.HasUnsavedChanges() {
		t.Fatal("closing the failure result lost the editable dirty registration overlay")
	}
	if m.workspace == nil || m.workspace.Draft["endpoint"] != "underlying setup draft" {
		t.Fatalf("failed Apply changed the underlying setup draft: %+v", m.workspace)
	}
}

func TestRegistrationSuccessfulApplyClearsOnlyRegistrationOverlay(t *testing.T) {
	m, backend, r := registrationExitFixture(t)
	dirtyRegistration(r)
	key := state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}
	if r.Profile.Key != key {
		t.Fatalf("fixture target changed: %+v", r.Profile.Key)
	}
	m.registrationKey("esc")
	cmd := m.chooseUnsavedExit(0)
	if cmd == nil {
		t.Fatal("Apply changes did not start registration operation")
	}
	m.Update(cmd())
	if backend.request == nil {
		t.Fatal("successful Apply did not send a registration request")
	}
	if m.registration != nil || m.unsavedExitIntent != nil {
		t.Fatal("successful Apply did not close the registration exit flow")
	}
	if r.HasUnsavedChanges() {
		t.Fatal("successful Apply did not set a clean registration baseline")
	}
	if m.workspace == nil || !m.workspace.Active || m.workspace.Draft["endpoint"] != "underlying setup draft" {
		t.Fatalf("successful Apply changed the underlying setup draft: %+v", m.workspace)
	}
}

func TestRegistrationCancelActionGuardsDirtyDraft(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyRegistration(r)
	r.Area = 2
	r.Action = 0
	m.registrationKey("enter")
	if m.unsavedExit == nil || m.registration != r {
		t.Fatal("dirty Cancel action closed the registration overlay without confirmation")
	}

	clean, _, cleanRegistration := registrationExitFixture(t)
	cleanRegistration.Area = 2
	cleanRegistration.Action = 0
	clean.registrationKey("enter")
	if clean.unsavedExit != nil || clean.registration != nil {
		t.Fatal("clean Cancel action did not close the registration overlay directly")
	}
}

func TestRegistrationMouseCancelGuardsDirtyDraft(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyRegistration(r)
	m.View() // Lay out the mouse overlay bounds.
	cmd := m.registrationMouse(tea.MouseClickMsg{X: r.X + 1, Y: r.Y + r.H - 2, Button: tea.MouseLeft})
	if cmd != nil || m.unsavedExit == nil || m.registration != r {
		t.Fatal("dirty mouse Cancel closed the registration overlay without confirmation")
	}
}

func TestRegistrationExitPromptIsVisibleWithoutHostForm(t *testing.T) {
	m, backend := typedProfileFixture()
	profile := backend.snapshot.Profiles[1]
	m.agents = []string{"codex"}
	m.openRegistrationOverlay(ProfileRow{Key: profile.Key, URL: profile.URL, Profile: &profile}, false)
	if m.form != nil {
		t.Fatal("fixture unexpectedly has a host configuration form")
	}
	r := m.registration
	r.Marked["codex"] = true
	m.registrationKey("esc")
	view := m.View().Content
	for _, choice := range []string{"Apply changes", "Discard changes", "Keep editing"} {
		if !strings.Contains(view, choice) {
			t.Fatalf("standalone registration exit prompt omitted %q:\n%s", choice, view)
		}
	}
}

func TestRegistrationMarkCleanAndRevertUseSemanticValues(t *testing.T) {
	r := &registrationState{
		Marked: map[string]bool{}, OriginalMarked: map[string]bool{},
		Transport: "streamable-http", OriginalTransport: "streamable-http",
	}
	r.Marked["codex"] = true
	if !r.HasUnsavedChanges() {
		t.Fatal("marking a new agent was not detected")
	}
	r.Marked["codex"] = false
	if r.HasUnsavedChanges() {
		t.Fatal("reverting a new agent mark still counted as dirty")
	}
	r.Transport = "sse"
	r.MarkClean()
	if r.HasUnsavedChanges() {
		t.Fatal("MarkClean did not update transport and registration baselines")
	}
}

func TestRegistrationCtrlCQuitsWithoutExitPrompt(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyRegistration(r)
	cmd := m.registrationKey("ctrl+c")
	if cmd == nil || m.unsavedExit != nil || m.registration != r {
		t.Fatal("Ctrl-C did not bypass the unsaved exit prompt")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl-C did not request direct quit")
	}
}

func dirtyUnderlyingForm(t *testing.T, m *Model) {
	t.Helper()
	m.form = forms.NewForm(t.Context(), []catalog.Input{{Name: "endpoint", Label: "Endpoint", Type: "string"}}, map[string]any{"endpoint": "initial"})
	m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.form.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	m.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestRegistrationF10PromptsForDirtyUnderlyingFormEvenWhenRegistrationIsClean(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyUnderlyingForm(t, m)
	m.registrationKey("f10")
	if m.unsavedExit == nil || m.unsavedExit.reason != "quit" || m.registration != r {
		t.Fatal("F10 skipped the host configuration prompt or discarded the clean registration overlay")
	}
}

func TestRegistrationDiscardOnQuitThenPromptsForDirtyUnderlyingForm(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyRegistration(r)
	dirtyUnderlyingForm(t, m)
	m.registrationKey("f10")
	if m.unsavedExit == nil || m.unsavedExit.reason != "quit" {
		t.Fatal("dirty registration did not prompt on F10")
	}
	if cmd := m.chooseUnsavedExit(1); cmd != nil {
		t.Fatal("discarding registration changes unexpectedly started work")
	}
	if m.registration != nil || m.unsavedExit == nil || m.unsavedExit.reason != "quit" || !m.form.HasUnsavedChanges() {
		t.Fatal("registration discard quit without prompting for the dirty host configuration")
	}
}

func TestRegistrationApplyOnQuitThenPromptsForDirtyUnderlyingForm(t *testing.T) {
	m, _, r := registrationExitFixture(t)
	dirtyRegistration(r)
	dirtyUnderlyingForm(t, m)
	m.registrationKey("f10")
	cmd := m.chooseUnsavedExit(0)
	if cmd == nil {
		t.Fatal("Apply changes did not start registration operation")
	}
	m.Update(cmd())
	if m.registration != nil || m.unsavedExit == nil || m.unsavedExit.reason != "quit" || !m.form.HasUnsavedChanges() {
		t.Fatal("successful registration apply quit before prompting for the dirty host configuration")
	}
}
