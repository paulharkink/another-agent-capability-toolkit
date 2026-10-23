package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXResultDialogFitsConfigurationInsetAcrossTerminalSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 200, Height: 34}, {Width: 120, Height: 36}, {Width: 80, Height: 16}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			backend := &setupBackendFixture{installErr: errors.New("registration failed")}
			m := NewContext(t.Context(), backend)
			m.view = "Catalog"
			m.Update(size)
			preview := viewmodel.SetupPreview{
				PackageName: "Demo", MCP: true, Key: state.Key{Source: "team", Package: "demo", Environment: "qa", Target: "local"},
				Inputs:       []viewmodel.SetupInput{{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string"}, Editable: true}},
				Destinations: []viewmodel.SetupDestination{{ID: "codex", Name: "Codex", Path: "/repos/codex", Selected: true}},
			}
			m.workspace = &workspaceState{Key: preview.Key, InvokingView: "Catalog", Active: true, Section: "Inputs", SectionID: sectionInputsID, Preview: &preview}
			m.openSetupForm(preview)
			m.pendingSetup = &preview
			m.pendingSetupField = "__aact_destinations"
			if err := m.form.ApplyValues(map[string]any{"repo": "/repos/demo"}); err != nil {
				t.Fatal(err)
			}
			_, save := m.Update(forms.ActionMsg{SectionID: sectionInputsID, ID: "save-inputs"})
			if save == nil {
				t.Fatal("Inputs Save did not execute")
			}
			m.Update(save())
			if backend.savedInputRequest == nil || backend.installRequest != nil {
				t.Fatalf("Inputs Save did not remain separate from install: saved=%+v install=%+v", backend.savedInputRequest, backend.installRequest)
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			if m.form.SectionID() != sectionAgentsID {
				t.Fatalf("Down did not select Agent access: %q", m.form.SectionID())
			}
			beforeX, beforeY, beforeWidth, beforeHeight, beforeOK := m.setupOverlayBounds()
			parentRows := strings.Split(ansi.Strip(m.View().Content), "\n")
			_, install := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
			if install == nil {
				t.Fatal("Agent access Install did not execute")
			}
			m.Update(runTeaCmd(t, m, install))
			if m.result == nil || !m.result.CanReturn || !m.result.Failed {
				t.Fatal("Agent access install failure did not open a recoverable result")
			}
			if !strings.Contains(strings.Join(m.result.Rows, "\\n"), "registration failed") {
				t.Fatalf("result lost the install error: %#v", m.result.Rows)
			}
			if x, y, width, height, ok := m.setupOverlayBounds(); !ok || !beforeOK || x != beforeX || y != beforeY || width != beforeWidth || height != beforeHeight {
				t.Errorf("retained configuration frame moved after save: bounds=(%d,%d %dx%d) ok=%t", x, y, width, height, ok)
			}
			capture := m.View().Content
			if size.Width == 200 {
				writeUXCapture(t, "result-geometry-wide", capture)
			}
			if size.Width == 80 {
				writeUXCapture(t, "result-geometry-minimum", capture)
			}
			rows := strings.Split(ansi.Strip(capture), "\n")
			if len(rows) != size.Height {
				t.Fatalf("result surface has %d rows, want viewport height %d", len(rows), size.Height)
			}
			for index, row := range rows {
				if got := ansi.StringWidth(row); got != size.Width {
					t.Errorf("result row %d has %d terminal cells, want %d: %q", index, got, size.Width, row)
				}
			}
			if !strings.Contains(capture, "Return to configuration") {
				t.Fatal("result dialog clipped the configuration return action")
			}
			if !strings.Contains(ansi.Strip(capture), "Edit answers") || !strings.Contains(ansi.Strip(capture), "Esc Close") {
				t.Fatal("result title clipped its edit or close keyboard action")
			}
			bodyWidth := max(1, min(78, beforeWidth-2)-4)
			visualRows := wrapResultRows(m.result.Rows, bodyWidth)
			dialogX, dialogY, dialogWidth, dialogHeight := expectedSetupResultBounds(beforeX, beforeY, beforeWidth, beforeHeight, len(visualRows))
			if !beforeOK || dialogX < beforeX+1 || dialogX+dialogWidth > beforeX+beforeWidth-1 || dialogY < beforeY+1 || dialogY+dialogHeight > beforeY+beforeHeight-1 {
				t.Fatalf("result dialog does not fit inside setup frame with a one-cell inset: frame=(%d,%d %dx%d) dialog=(%d,%d %dx%d)", beforeX, beforeY, beforeWidth, beforeHeight, dialogX, dialogY, dialogWidth, dialogHeight)
			}
			plain := strings.Split(ansi.Strip(capture), "\n")
			borderFound := false
			for y, row := range plain {
				if !strings.Contains(row, "╔") {
					continue
				}
				borderWidth := strings.Count(row, "═") + 2
				left := ansi.StringWidth(strings.SplitN(row, "╔", 2)[0])
				if borderWidth != dialogWidth || left != dialogX || y != dialogY {
					continue
				}
				borderFound = true
			}
			if !borderFound {
				t.Errorf("result dialog top border does not match its inset geometry (%d,%d %dx%d)", dialogX, dialogY, dialogWidth, dialogHeight)
			}
			if beforeOK {
				for row := beforeY; row < beforeY+beforeHeight && row < len(rows); row++ {
					leftBefore := ansi.Strip(ansi.Cut(parentRows[row], beforeX, beforeX+1))
					rightBefore := ansi.Strip(ansi.Cut(parentRows[row], beforeX+beforeWidth-1, beforeX+beforeWidth))
					if leftBefore == "" || rightBefore == "" {
						continue
					}
					leftAfter := ansi.Strip(ansi.Cut(rows[row], beforeX, beforeX+1))
					rightAfter := ansi.Strip(ansi.Cut(rows[row], beforeX+beforeWidth-1, beforeX+beforeWidth))
					if leftAfter != leftBefore || rightAfter != rightBefore {
						t.Fatalf("result overlay erased parent frame side borders at row %d: left %q→%q right %q→%q", row, leftBefore, leftAfter, rightBefore, rightAfter)
					}
				}
			}

			if size.Width == 200 {
				m.Update(tea.MouseClickMsg{X: dialogX + 5, Y: dialogY + dialogHeight - 2, Button: tea.MouseLeft})
			} else {
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if m.result != nil || m.form == nil || m.pendingSetup == nil {
				t.Fatal("return action did not restore the setup editor")
			}
			m.form.FocusSection()
			m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			if m.form.SectionID() != sectionInputsID {
				t.Fatalf("could not return to Inputs after result: selected %q", m.form.SectionID())
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
			view := ansi.Strip(m.View().Content)
			pages := collectWorkspacePages(t, m)
			if !strings.Contains(pages, "Repository") || !strings.Contains(pages, "/repos/demo") {
				t.Fatalf("Inputs lost the submitted Repository value after returning from result:\n%s", pages)
			}
			for index, row := range strings.Split(view, "\n") {
				if got := ansi.StringWidth(row); got != size.Width {
					t.Fatalf("configuration row %d has %d terminal cells, want %d", index, got, size.Width)
				}
			}
		})
	}
}

func expectedSetupResultBounds(frameX, frameY, frameWidth, frameHeight, rowCount int) (x, y, width, height int) {
	width = min(78, frameWidth-2)
	height = min(frameHeight-2, max(10, min(16, rowCount+7)))
	return frameX + (frameWidth-width)/2, frameY + (frameHeight-height)/2, width, height
}

func TestUXResultDialogRetainsLongErrorAfterResize(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{installErr: errors.New(strings.Repeat("permission denied from registration endpoint ", 12))})
	m.view = "Catalog"
	m.width, m.height = 200, 34
	preview := viewmodel.SetupPreview{PackageName: "Demo", Key: state.Key{Package: "demo", Environment: "qa", Target: "local"}}
	m.workspace = &workspaceState{Key: preview.Key, InvokingView: "Catalog", Active: true, Section: "Inputs"}
	m.openSetupForm(preview)
	m.pendingSetup = &preview
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex"}})
	m.Update(runTeaCmd(t, m, cmd))
	if !strings.Contains(ansi.Strip(m.View().Content), "permission denied from registration endpoint") {
		t.Fatal("long registration error disappeared from the result viewer")
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if !strings.Contains(ansi.Strip(m.View().Content), "Operation result") {
		t.Fatal("result viewer was lost after resizing")
	}
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 34})
	if !strings.Contains(ansi.Strip(m.View().Content), "Operation result") {
		t.Fatal("result viewer was not restored after expanding")
	}
}

func TestUXResultDialogUsesObservedManagementOverlayBounds(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.view = "Environments"
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 34})
	preview := viewmodel.SetupPreview{PackageName: "Demo", Key: state.Key{Package: "demo", Environment: "qa", Target: "local"}}
	m.workspace = &workspaceState{Key: preview.Key, Active: true, ObservedOnly: true, InvokingView: "Environments"}
	m.management.FormOverlay = true
	m.openSetupForm(preview)
	m.setupRetry = &setupRetryDraft{preview: preview, values: map[string]any{}, origin: "Environments"}
	m.result = &resultState{Rows: []string{"Operation failed"}, CanReturn: true, CanRetry: true}

	frameX, frameY, frameWidth, frameHeight, ok := managementFormOverlayBounds(m.width, m.height)
	if !ok {
		t.Fatal("management overlay bounds were not available")
	}
	if x, y, width, height, gotOK := m.setupOverlayBounds(); !gotOK || x != frameX || y != frameY || width != frameWidth || height != frameHeight {
		t.Fatalf("observed workspace did not retain management frame bounds: got=(%d,%d %dx%d) ok=%t want=(%d,%d %dx%d)", x, y, width, height, gotOK, frameX, frameY, frameWidth, frameHeight)
	}
	bodyWidth := max(1, min(78, frameWidth-2)-4)
	visualRows := wrapResultRows(m.result.Rows, bodyWidth)
	dialogX, dialogY, dialogWidth, dialogHeight := expectedSetupResultBounds(frameX, frameY, frameWidth, frameHeight, len(visualRows))
	if dialogX < frameX+1 || dialogX+dialogWidth > frameX+frameWidth-1 || dialogY < frameY+1 || dialogY+dialogHeight > frameY+frameHeight-1 {
		t.Fatalf("result dialog escaped observed management frame: frame=(%d,%d %dx%d) dialog=(%d,%d %dx%d)", frameX, frameY, frameWidth, frameHeight, dialogX, dialogY, dialogWidth, dialogHeight)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Esc Close") {
		t.Fatal("management-frame result clipped its close action")
	}
}

func TestUXResultMouseClickOutsideDialogDoesNotActivateFooterAction(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{installErr: errors.New("registration failed")})
	m.view = "Catalog"
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	preview := viewmodel.SetupPreview{PackageName: "Demo", Key: state.Key{Source: "team", Package: "demo", Environment: "qa", Target: "local"}}
	m.openSetupForm(preview)
	m.pendingSetup = &preview
	m.pendingSetupField = "__aact_destinations"
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex"}})
	m.form = nil
	m.Update(runTeaCmd(t, m, cmd))
	if m.result == nil || !m.result.CanReturn {
		t.Fatal("setup failure did not present its foreground result")
	}
	frameX, frameY, frameWidth, frameHeight, ok := m.setupOverlayBounds()
	if !ok {
		t.Fatal("setup frame was unavailable")
	}
	bodyWidth := max(1, min(78, frameWidth-2)-4)
	visualRows := wrapResultRows(m.result.Rows, bodyWidth)
	dialogX, dialogY, dialogWidth, dialogHeight := expectedSetupResultBounds(frameX, frameY, frameWidth, frameHeight, len(visualRows))
	m.Update(tea.MouseClickMsg{X: dialogX + dialogWidth, Y: dialogY + dialogHeight - 2, Button: tea.MouseLeft})
	if m.result != nil || m.form != nil || m.setupRetry != nil {
		t.Fatalf("outside click activated a result footer action: result=%+v form=%t retry=%t", m.result, m.form != nil, m.setupRetry != nil)
	}
}
