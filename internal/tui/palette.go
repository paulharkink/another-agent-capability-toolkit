package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const navySGR = "\x1b[38;2;233;245;255;48;2;9;38;111m"

// Nested Lip Gloss spans reset terminal colors, so restore the canvas colors
// before drawing the next frame glyph or padding cell.
func navyCanvas(content string) string {
	restore := strings.NewReplacer("\x1b[m", navySGR, "\x1b[0m", navySGR, "\x1b[49m", navySGR)
	return navySGR + restore.Replace(content) + "\x1b[m"
}

// fixedPaletteRows keeps the rendered screen's own colors underneath a
// foreground dialog. It also gives any unused terminal cells the app's normal
// navy background instead of a terminal-default black background.
func fixedPaletteRows(content string, width, height int) []string {
	lines := strings.Split(content, "\n")
	rows := make([]string, height)
	for row := range rows {
		line := ""
		if row < len(lines) {
			line = ansi.Truncate(lines[row], width, "")
		}
		line = navyCanvas(line)
		used := ansi.StringWidth(line)
		if used < width {
			line += navySGR + strings.Repeat(" ", width-used)
		}
		rows[row] = line
	}
	return rows
}

// composeOverlayRow replaces only the dialog's cells. Outside that rectangle,
// the underlying screen retains its fixed palette; no global dimming is used.
func composeOverlayRow(base, dialog string, x, dialogWidth, width int) string {
	left := ansi.Cut(base, 0, x)
	right := ansi.Cut(base, x+dialogWidth, width)
	return left + dialog + navySGR + right
}
