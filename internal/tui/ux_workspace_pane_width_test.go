package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestWorkspaceInformationUsesAvailablePaneWidthAcrossResizes(t *testing.T) {
	var widestRows []int
	for _, size := range []tea.WindowSizeMsg{{Width: 200, Height: 40}, {Width: 80, Height: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			m := NewContext(t.Context(), &setupBackendFixture{})
			m.view = "Catalog"
			m.Update(size)
			preview := viewmodel.SetupPreview{
				PackageName:    "Demo",
				CredentialNote: strings.Repeat("INFOBLOCK", 60),
				Key:            state.Key{Source: "fixture", Package: "demo", Environment: "dev", Target: "local"},
			}
			m.workspace = &workspaceState{Key: preview.Key, Active: true, Section: "Information", InvokingView: "Catalog"}
			m.openSetupForm(preview)
			m.configureWorkspaceForm(preview)
			m.form.SelectSectionID(sectionInformationID)
			m.form.FocusSection()

			formRows := strings.Split(ansi.Strip(m.form.View().Content), "\n")
			for rowIndex, row := range formRows {
				if got := ansi.StringWidth(row); got != m.form.PaneWidth() {
					t.Fatalf("form row %d has width %d, want form width %d: %q", rowIndex, got, m.form.PaneWidth(), row)
				}
			}
			longRows := 0
			widest := 0
			for _, row := range formRows {
				if !strings.Contains(row, "INFOBLOCK") {
					continue
				}
				longRows++
				divider := strings.Index(row, "│")
				if divider < 0 {
					t.Fatalf("split pane divider missing from row %q", row)
				}
				content := strings.TrimRight(row[divider+len("│"):], " █│║")
				if width := ansi.StringWidth(content); width > widest {
					widest = width
				}
			}
			if longRows == 0 {
				t.Fatal("long capability information was not rendered")
			}
			widestRows = append(widestRows, widest)
			if size.Width == 200 && widest < 110 {
				t.Fatalf("wide Information pane left long rows artificially narrow: widest=%d, rows=%d", widest, longRows)
			}
			if size.Width == 80 && widest > 50 {
				t.Fatalf("narrow Information row exceeds its pane-safe width: widest=%d\n%s", widest, strings.Join(formRows, "\n"))
			}
		})
	}
	if len(widestRows) != 2 || widestRows[0] <= widestRows[1] {
		t.Fatalf("wider terminal did not use wider information rows (and reduce wraps): wide/narrow=%v", widestRows)
	}
}

func TestLongLogLineClipsToResizedLogViewportAndPreservesPan(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	longLine := strings.Repeat("LOGTOKEN", 40)
	m.home.Modal = &modalState{Kind: "logs", Label: "diagnostic", Rows: []string{longLine}, Offset: 0}
	visibleWidths := []int{}
	for _, size := range []tea.WindowSizeMsg{{Width: 200, Height: 30}, {Width: 80, Height: 24}} {
		m.Update(size)
		base := make([]string, size.Height)
		for i := range base {
			base[i] = strings.Repeat(" ", size.Width)
		}
		rows := m.logsOverlay(base)
		maxTokenWidth := 0
		for i, row := range rows {
			if got := ansi.StringWidth(ansi.Strip(row)); got != size.Width {
				t.Fatalf("%dx%d log row %d has width %d, want %d: %q", size.Width, size.Height, i, got, size.Width, ansi.Strip(row))
			}
			if strings.Contains(row, "LOGTOKEN") {
				if width := strings.Count(row, "LOGTOKEN") * len("LOGTOKEN"); width > maxTokenWidth {
					maxTokenWidth = width
				}
			}
		}
		if maxTokenWidth == 0 || maxTokenWidth > min(size.Width-4, 98) {
			t.Fatalf("long log row does not fit %dx%d overlay content: visible token width=%d", size.Width, size.Height, maxTokenWidth)
		}
		visibleWidths = append(visibleWidths, maxTokenWidth)
		if m.home.Modal.Rows[0] != longLine {
			t.Fatal("resizing or clipping changed the source log line")
		}
	}
	if visibleWidths[0] <= visibleWidths[1] {
		t.Fatalf("wide log overlay did not expose more of a long line: wide/narrow=%v", visibleWidths)
	}
	m.logKey("right")
	if m.home.Modal.Column != 4 {
		t.Fatalf("horizontal pan stopped advancing after resize: column=%d", m.home.Modal.Column)
	}
}

func TestInformationWrapKeepsWordsTogetherWhenPaneAllows(t *testing.T) {
	lines := splitDisplayLine("Managed credential material is missing at endpoint", 26)
	if got := strings.Join(lines, ""); got != "Managed credential material is missing at endpoint" {
		t.Fatalf("information wrapping changed its source text: %q", got)
	}
	for index := 0; index+1 < len(lines); index++ {
		if !strings.HasSuffix(lines[index], " ") && !strings.HasPrefix(lines[index+1], " ") {
			t.Fatalf("information wrapping split a word at the pane edge: %#v", lines)
		}
	}
}
