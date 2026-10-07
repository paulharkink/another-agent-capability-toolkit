package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestOperationResultOpensScrollableViewerWithAllAgentMessagesAndError(t *testing.T) {
	m, _ := homeFixture()
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("agent-%02d: MCP registration configured", i))
	}
	m.action = "configure registrations"
	m.Update(operationMsg{origin: "Catalog", output: strings.Join(lines, "\n"), err: errors.New("agent-41: permission denied")})
	first := m.View().Content
	if !strings.HasPrefix(first, navySGR) {
		t.Fatal("operation result lost the blue terminal palette")
	}
	if !strings.Contains(first, "Operation result") || !strings.Contains(first, "agent-01: MCP registration configured") || !strings.Contains(first, "agent-41: permission denied") || strings.Contains(first, "agent-40: MCP registration configured") {
		t.Fatalf("result viewer did not start at first page:\n%s", first)
	}
	press(m, tea.KeyEnd, "")
	last := m.View().Content
	if !strings.Contains(last, "agent-40: MCP registration configured") {
		t.Fatalf("last agent result was lost:\n%s", last)
	}
	press(m, tea.KeyHome, "")
	m.Update(tea.MouseWheelMsg{X: m.width / 2, Y: m.height / 2, Button: tea.MouseWheelDown})
	if !strings.Contains(m.View().Content, "agent-02: MCP registration configured") {
		t.Fatal("mouse wheel did not scroll result viewer")
	}
	press(m, tea.KeyEscape, "")
	if strings.Contains(m.View().Content, "Operation result") || m.view != "Catalog" || !strings.Contains(m.output, "agent-41: permission denied") {
		t.Fatalf("closing result viewer lost screen or status: view=%q output=%q", m.view, m.output)
	}
}

func TestFailedOperationShowsConcreteCauseOnFirstPage(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	lines := make([]string, 35)
	for i := range lines {
		lines[i] = fmt.Sprintf("agent-%02d: skill configured", i)
	}
	m.Update(operationMsg{origin: "Catalog", output: strings.Join(lines, "\n"), err: errors.New("claude: config file permission denied")})
	view := m.View().Content
	if !strings.Contains(view, "Failed: claude: config file permission denied") {
		t.Fatalf("specific error was hidden below the first page:\n%s", view)
	}
}

func TestResultStatusIsProminentAndFailuresUseHighContrastErrorRows(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "Installed", err: errors.New("repo-map failed: exact fixture cause")})
	view := m.View().Content
	if !strings.Contains(ansi.Strip(view), "FAILED") || !strings.Contains(view, "\x1b[1;38;2;255;77;95m") {
		t.Fatalf("failed outcome lacks a bold, high-contrast status label: %q", view)
	}
	if !strings.Contains(ansi.Strip(view), "repo-map failed: exact fixture cause") || !strings.Contains(view, "\x1b[1;38;2;255;77;95m║ Failed:") {
		t.Fatalf("actual error was changed or not emphasized: %q", view)
	}

	m, _ = homeFixture()
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "Installed"})
	if !strings.Contains(ansi.Strip(m.View().Content), "SUCCESS") {
		t.Fatalf("successful outcome lacks an explicit SUCCESS label: %q", m.View().Content)
	}
}

func TestFailedInstallResultShowsChildDiagnosticAndNavigation(t *testing.T) {
	m, _ := homeFixture()
	m.setupRetry = &setupRetryDraft{}
	m.setupOperationPending = true
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "Inputs saved", err: errors.New("command repo-map failed: exit status 1\nstderr:\nrepo map: missing root directory")})
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"command repo-map failed", "repo map: missing root directory", "Edit answers", "↑↓", "←→"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failed install hides %q:\n%s", want, view)
		}
	}
}

func TestOperationResultViewerClosesByMouseWithoutOpeningUnderlyingActions(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "codex: skill configured"})
	view := m.View().Content
	if !strings.Contains(view, "codex: skill configured") || !strings.Contains(view, "Back") {
		t.Fatalf("result was not foregrounded:\n%s", view)
	}
	m.Update(tea.MouseClickMsg{X: m.width / 2, Y: m.height - 2, Button: tea.MouseLeft})
	if strings.Contains(m.View().Content, "Operation result") || m.home.Modal != nil {
		t.Fatal("Close click did not dismiss viewer cleanly")
	}
}

func TestOperationResultViewerFitsSmallTerminal(t *testing.T) {
	m, _ := homeFixture()
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", output: "done"})
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 5})
	view := m.View().Content
	if !strings.HasPrefix(view, navySGR) {
		t.Fatal("small-terminal result lost the blue terminal palette")
	}
	if len(strings.Split(view, "\n")) > 5 || !strings.Contains(view, "80×16") {
		t.Fatalf("small terminal overflowed without resize guidance: %q", view)
	}
	press(m, tea.KeyEscape, "")
	if m.result != nil {
		t.Fatal("small terminal cannot close result")
	}
}

func TestOperationResultViewerPreservesGlobalQuitShortcut(t *testing.T) {
	m, _ := homeFixture()
	m.Update(operationMsg{origin: "Catalog", output: "done"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF10})
	if cmd == nil {
		t.Fatal("F10 was swallowed by operation result viewer")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("F10 did not quit: %T", cmd())
	}
}

func TestOperationResultMatchesMockDialogPaletteAcrossFullSurface(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "success", want: "38;2;217;246;228m"},
		{name: "error", err: errors.New("permission denied"), want: "1;38;2;255;77;95m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := homeFixture()
			m.action = "save"
			m.Update(operationMsg{origin: "Catalog", output: "Operation completed", err: tc.err})
			view := m.View().Content
			if got := defaultBackgroundGlyphs(view); got != 0 {
				t.Fatalf("result surface has %d glyphs with default/black background", got)
			}
			if !strings.Contains(view, "48;2;12;49;133m") {
				t.Fatal("result dialog does not use mock #0c3185 background")
			}
			if !strings.Contains(view, tc.want) {
				t.Fatalf("result status does not use mock palette %q", tc.want)
			}
		})
	}
}

func TestOperationResultViewerPansLongRowsHorizontally(t *testing.T) {
	m, _ := homeFixture()
	longRow := strings.Repeat("0123456789", 20)
	m.action = "save"
	m.Update(operationMsg{origin: "Catalog", output: longRow})
	before := ansi.Strip(m.View().Content)
	press(m, tea.KeyRight, "")
	after := ansi.Strip(m.View().Content)
	if before == after || !strings.Contains(after, "4567890123") {
		t.Fatalf("right pan did not shift result text by four columns:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestOperationResultOverlayPreservesUnderlyingPalette(t *testing.T) {
	m, _ := homeFixture()
	m.action = "save"
	underlying := m.View().Content
	m.Update(operationMsg{origin: "Catalog", output: "Inputs saved"})
	view := m.View().Content
	if !strings.Contains(ansi.Strip(view), "AACT · Another Agent Capability Toolkit") || !strings.Contains(ansi.Strip(view), "Operation result") {
		t.Fatalf("result overlay should retain the underlying screen and modal:\n%s", ansi.Strip(view))
	}
	if strings.Contains(view, "48;2;6;22;74m") || strings.Contains(view, "38;2;82;103;143m") {
		t.Fatal("result overlay recolored the underlying layer with a darkened palette")
	}
	if !strings.Contains(view, "48;2;9;38;111m") || !strings.Contains(view, "38;2;255;227;138m") {
		t.Fatal("result overlay removed the home screen's fixed background or selection palette")
	}
	if ansi.Strip(underlying) == ansi.Strip(view) {
		t.Fatal("result dialog was not drawn over the retained screen")
	}
}

func TestProgressOverlayPreservesUnderlyingPalette(t *testing.T) {
	m, _ := homeFixture()
	m.width, m.height = 110, 30
	m.action = "check connection"
	underlying := m.View().Content
	view := m.progressView().Content
	if strings.Contains(view, "48;2;6;22;74m") || strings.Contains(view, "38;2;82;103;143m") {
		t.Fatal("progress overlay recolored the underlying layer with a darkened palette")
	}
	if !strings.Contains(view, "48;2;9;38;111m") || !strings.Contains(view, "38;2;255;227;138m") {
		t.Fatal("progress overlay removed the home screen's fixed background or selection palette")
	}
	if !strings.Contains(ansi.Strip(view), "Operation in progress") || ansi.Strip(underlying) == ansi.Strip(view) {
		t.Fatal("progress dialog was not drawn over the retained screen")
	}
}
