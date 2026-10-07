package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDirtySetupBackShowsUnsavedPopupAndKeepEditingPreservesDraft(t *testing.T) {
	m, backend := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyDown, "") // Source kubeconfig.
	setupKey(m, 'm', "m")        // Edit manually.
	setupKey(m, 'x', "x")
	setupKey(m, tea.KeyEnter, "")
	setupKey(m, tea.KeyEscape, "")                         // Details → sections.
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil { // Request to leave the dirty form.
		m.Update(cmd())
	}

	view := m.View().Content
	for _, want := range []string{"Apply changes", "Discard changes", "Keep editing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dirty form exit omitted %q:\n%s", want, view)
		}
	}
	setupKey(m, tea.KeyEnter, "") // Keep editing is selected by default.
	if m.form == nil || backend.installRequest != nil || !m.form.HasUnsavedChanges() {
		t.Fatalf("Keep editing changed or lost the active draft: form=%t applied=%t dirty=%t", m.form != nil, backend.installRequest != nil, m.form != nil && m.form.HasUnsavedChanges())
	}
}

func TestUnsavedPopupSummarizesChangedLabelsWithoutValues(t *testing.T) {
	m, _ := openSetupInteraction(t)
	if err := m.form.ApplyValues(map[string]any{
		"token":     "never-print-this-secret",
		"databases": []string{"grafana"},
	}); err != nil {
		t.Fatal(err)
	}
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	view := ansi.Strip(m.View().Content)
	changedRow := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Changed:") {
			changedRow = line
			break
		}
	}
	if changedRow == "" {
		t.Fatalf("dirty popup omitted the changed-field summary:\n%s", view)
	}
	for _, label := range []string{"Token", "Databases"} {
		if !strings.Contains(changedRow, label) {
			t.Errorf("changed-field summary omitted %q: %s", label, changedRow)
		}
	}
	for _, secret := range []string{"never-print-this-secret", "grafana"} {
		if strings.Contains(changedRow, secret) {
			t.Errorf("changed-field summary exposed a value %q: %s", secret, changedRow)
		}
	}
}

func TestCleanSetupBackDoesNotShowUnsavedPopup(t *testing.T) {
	m, _ := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	m.form.MarkClean()
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
		m.Update(cmd())
	}
	if strings.Contains(m.View().Content, "Apply changes") || m.form != nil || m.workspace.Active {
		t.Fatalf("clean form exit was guarded or did not complete:\n%s", m.View().Content)
	}
}

func TestDiscardFromDirtyBackClearsCachedWorkspaceDraft(t *testing.T) {
	m, backend := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	m.workspace.Draft = map[string]any{"token": "old draft"}
	dirtySetupAndRequestBack(t, m)
	setupKey(m, tea.KeyUp, "") // Discard changes.
	setupKey(m, tea.KeyEnter, "")
	if m.form != nil || m.workspace.Active || m.workspace.Draft != nil || backend.installRequest != nil {
		t.Fatalf("Discard did not leave and clear the cached draft: form=%t active=%t draft=%v applied=%t", m.form != nil, m.workspace.Active, m.workspace.Draft, backend.installRequest != nil)
	}
}

func TestMouseSelectingDiscardLeavesWithoutApplying(t *testing.T) {
	m, backend := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	m.workspace.Draft = map[string]any{"token": "old draft"}
	dirtySetupAndRequestBack(t, m)
	width, height := min(52, max(38, m.width-6)), 9
	x, y := max(0, (m.width-width)/2), max(0, (m.height-height)/2)
	_, _ = m.Update(tea.MouseClickMsg{X: x + 3, Y: y + 5, Button: tea.MouseLeft})
	if m.form != nil || m.workspace.Active || m.workspace.Draft != nil || backend.installRequest != nil {
		t.Fatalf("mouse Discard did not leave cleanly: form=%t active=%t draft=%v applied=%t", m.form != nil, m.workspace.Active, m.workspace.Draft, backend.installRequest != nil)
	}
}

func TestApplyFailureKeepsDirtyFormEditableAndShowsActualError(t *testing.T) {
	failure := errors.New("fixture rejected configuration apply")
	m, backend := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	dirtySetupAndRequestBack(t, m)
	form := m.form
	backend.installErr = failure
	setupKey(m, tea.KeyUp, "")
	setupKey(m, tea.KeyUp, "") // Apply changes.
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Apply changes did not run Save and apply")
	}
	result := runTeaCmd(t, m, cmd)
	m.Update(result)
	if m.form != form || !m.form.HasUnsavedChanges() || m.result == nil {
		t.Fatalf("failed apply lost the same dirty form or error dialog: same=%t dirty=%t result=%+v", m.form == form, m.form != nil && m.form.HasUnsavedChanges(), m.result)
	}
	if !strings.Contains(strings.Join(m.result.Rows, "\n"), failure.Error()) {
		t.Fatalf("failed apply did not show its actual error in the result dialog: %+v", m.result.Rows)
	}
	setupKey(m, tea.KeyEscape, "") // Dismiss the error dialog.
	if m.result != nil || m.form != form || !m.form.HasUnsavedChanges() {
		t.Fatal("dismissing the apply error lost the retained dirty form")
	}
	for attempts := 0; attempts < 3 && !strings.Contains(m.View().Content, "Apply changes"); attempts++ {
		if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
			m.Update(cmd())
		}
	}
	if !strings.Contains(m.View().Content, "Apply changes") {
		t.Fatal("failed apply reset the dirty baseline; leaving should prompt again")
	}
	setupKey(m, tea.KeyEnter, "") // Keep editing and dismiss the popup.
	setupKey(m, tea.KeyRight, "") // Form remains editable.
	if m.form != form || !m.form.HasUnsavedChanges() {
		t.Fatal("the failed form was not editable after the error was dismissed")
	}
	backend.installErr = nil
	_, retry := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if retry == nil {
		t.Fatal("retained configuration form could not retry Save and apply")
	}
	m.Update(runTeaCmd(t, m, retry))
	if backend.installRequest == nil || m.pendingSetup == nil {
		t.Fatal("retry did not run Save and apply against the retained setup")
	}
}

func TestPopupApplyCompletesBackOnlyAfterBackendSuccessAndClearsProgress(t *testing.T) {
	m, backend := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	dirtySetupAndRequestBack(t, m)
	setupKey(m, tea.KeyUp, "")
	setupKey(m, tea.KeyUp, "")
	_, apply := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if apply == nil {
		t.Fatal("Apply changes did not dispatch Save and apply")
	}
	result := runTeaCmd(t, m, apply)
	if m.form == nil || !m.workspace.Active {
		t.Fatal("configuration left before the apply result arrived")
	}
	m.Update(result)
	if backend.installRequest == nil || m.form != nil || m.workspace.Active || m.workspace.Draft != nil {
		t.Fatalf("successful apply did not complete the requested Back: request=%+v form=%t active=%t draft=%v", backend.installRequest, m.form != nil, m.workspace.Active, m.workspace.Draft)
	}
	if m.setupOperationPending || m.setupProgressEvents != nil || m.setupProgressDone != nil || m.unsavedExitIntent != nil {
		t.Fatal("successful exit left setup operation/progress state pending")
	}
}

func TestCtrlCFromUnsavedExitPopupQuitsDirectly(t *testing.T) {
	m, _ := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	dirtySetupAndRequestBack(t, m)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl-C from the exit popup did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl-C from the exit popup did not return tea.Quit")
	}
	if m.form != nil || m.workspace != nil {
		t.Fatal("Ctrl-C did not discard the active draft before quitting")
	}
}

func TestF10OnDirtyFormPromptsAndCtrlCOnDirtyFormDiscards(t *testing.T) {
	m, _ := openSetupInteraction(t)
	enableWorkspaceBackForTest(m)
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyDown, "")
	setupKey(m, 'm', "m")
	setupKey(m, 'x', "x")
	_, quit := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
	if quit != nil || !strings.Contains(m.View().Content, "Apply changes") {
		t.Fatal("F10 on a dirty form did not open the unsaved changes popup")
	}

	n, _ := openSetupInteraction(t)
	enableWorkspaceBackForTest(n)
	setupKey(n, tea.KeyRight, "")
	setupKey(n, tea.KeyDown, "")
	setupKey(n, 'm', "m")
	setupKey(n, 'x', "x")
	_, quit = n.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit == nil {
		t.Fatal("Ctrl-C on a dirty form did not quit directly")
	}
	if _, ok := quit().(tea.QuitMsg); !ok || n.form != nil || n.workspace != nil {
		t.Fatal("Ctrl-C did not directly discard the form before quitting")
	}
}

func TestUnsavedExitPopupFitsMinimumAndWideTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 16}, {120, 28}, {279, 37}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ := openSetupInteraction(t)
			enableWorkspaceBackForTest(m)
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			dirtySetupAndRequestBack(t, m)
			for i, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if got := ansi.StringWidth(line); got > size.width {
					t.Fatalf("popup line %d overflows width %d with %d cells: %q", i+1, size.width, got, line)
				}
			}
		})
	}
}

func TestDefaultAgentsPopupApplyFailureRetainsActionAndRetriesSameService(t *testing.T) {
	failure := errors.New("settings adapter rejected default agents")
	backend := &retryDefaultAgentsBackend{firstErr: failure}
	m := NewContext(t.Context(), backend)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(m.Init()())
	m.navigate("Settings")
	setupKey(m, tea.KeyDown, "")
	setupKey(m, tea.KeyF2, "")
	setupKey(m, tea.KeyEnd, "")
	setupKey(m, tea.KeyEnter, "")
	if m.form == nil {
		t.Fatal("default agents settings form did not open")
	}
	setupKey(m, tea.KeySpace, " ") // Change the selected default agent.
	if !m.form.HasUnsavedChanges() {
		t.Fatal("default agent edit was not reflected as unsaved")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
	if !strings.Contains(ansi.Strip(m.View().Content), "Apply changes") {
		t.Fatal("F10 on edited default agents did not prompt")
	}
	setupKey(m, tea.KeyUp, "")
	setupKey(m, tea.KeyUp, "")
	_, apply := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if apply == nil {
		t.Fatal("popup Apply did not save default agents")
	}
	m.Update(apply())
	if m.result == nil || !strings.Contains(strings.Join(m.result.Rows, "\n"), failure.Error()) || m.form == nil || !m.form.HasUnsavedChanges() {
		t.Fatalf("failed settings save did not show the real error and preserve the dirty form: result=%+v form=%t dirty=%t", m.result, m.form != nil, m.form != nil && m.form.HasUnsavedChanges())
	}
	setupKey(m, tea.KeyEscape, "") // Dismiss the error dialog.
	if m.form == nil || !m.form.HasUnsavedChanges() || !m.pendingDefaultAgents {
		t.Fatal("dismissing settings error lost the dirty form or its save route")
	}
	_, retry := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if retry == nil {
		t.Fatal("Ctrl-S could not retry the default agents service")
	}
	m.Update(retry())
	if backend.attempts != 2 {
		t.Fatalf("settings save attempted %d times, want first failure and second retry", backend.attempts)
	}
}

type retryDefaultAgentsBackend struct {
	editableSettingsBackend
	firstErr error
	attempts int
}

func (b *retryDefaultAgentsBackend) UISetDefaultAgents(_ context.Context, ids []string) error {
	b.attempts++
	if b.attempts == 1 {
		return b.firstErr
	}
	b.saved = append([]string(nil), ids...)
	return nil
}

func dirtySetupAndRequestBack(t *testing.T, m *Model) {
	t.Helper()
	setupKey(m, tea.KeyRight, "")
	setupKey(m, tea.KeyDown, "")
	setupKey(m, 'm', "m")
	setupKey(m, 'x', "x")
	setupKey(m, tea.KeyEnter, "")
	setupKey(m, tea.KeyEscape, "")
	if cmd := setupKey(m, tea.KeyEscape, ""); cmd != nil {
		m.Update(cmd())
	}
}
