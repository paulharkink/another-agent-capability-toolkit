package tui

import (
	"strings"
	"unicode/utf8"

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
	return left + dialog + sgrStateAt(base, x+dialogWidth) + right
}

// sgrStateAt returns the active SGR sequences at a cell boundary so that a
// style which spans across an overlay resumes unchanged on its far side.
func sgrStateAt(content string, cell int) string {
	// Canvas rows are separated with navySGR. Replay every SGR change so
	// bold, reverse, underline, and future parent styles survive the seam too.
	active := []string{navySGR}
	for i, col := 0, 0; i < len(content) && col < cell; {
		if content[i] == '\x1b' && i+1 < len(content) && content[i+1] == '[' {
			end := strings.IndexByte(content[i:], 'm')
			if end > 0 {
				sequence := content[i : i+end+1]
				if sgrResetsAll(content[i+2 : i+end]) {
					active = nil
				}
				active = append(active, sequence)
				i += end + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(content[i:])
		if content[i] == '\n' || content[i] == '\r' {
			i += size
			continue
		}
		col += ansi.StringWidth(content[i : i+size])
		i += size
	}
	return strings.Join(active, "")
}

func sgrResetsAll(parameters string) bool {
	if parameters == "" {
		return true
	}
	parts := strings.Split(parameters, ";")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "38" || parts[i] == "48" {
			if i+4 < len(parts) && parts[i+1] == "2" {
				i += 4
				continue
			}
			if i+2 < len(parts) && parts[i+1] == "5" {
				i += 2
				continue
			}
		}
		if parts[i] == "0" {
			return true
		}
	}
	return false
}
