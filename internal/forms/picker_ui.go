package forms

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
)

func (m *FormModel) applyPicked(msg pickedMsg) {
	if errors.Is(msg.err, picker.ErrCancelled) {
		if msg.resumeEdit {
			m.editing = true
		}
		return
	}
	if msg.err != nil {
		m.message = msg.err.Error()
		if msg.resumeEdit {
			m.editing = true
		}
		return
	}
	var err error
	switch msg.action {
	case "add":
		err = m.editor.AddPath(msg.name, msg.path)
	case "edit":
		err = m.editor.EditPath(msg.name, msg.index, msg.path)
	default:
		err = m.editor.Apply(msg.name, msg.path)
	}
	m.setError(err)
	if err == nil && msg.resumeEdit {
		m.editing = false
		m.editAction = ""
		m.editOriginal = ""
		m.buffer = ""
		m.cursor = 0
	} else if err != nil && msg.resumeEdit {
		m.editing = true
	}
	if err == nil && msg.action != "add" {
		m.activateExclusive(msg.name)
	}
	if err == nil && msg.action == "add" {
		m.rowIndex[msg.name] = len(collectionRows(m.editor.Values()[msg.name])) - 1
	}
}

func (m *FormModel) resizeBrowser() {
	if m.browser == nil {
		return
	}
	_, _, _, _, innerWidth, innerHeight, _ := m.pickerGeometry()
	m.browser.Update(tea.WindowSizeMsg{Width: innerWidth, Height: innerHeight})
}

// pickerGeometry returns both panel and browser-content origins in form-local
// coordinates. The TUI setup overlay already translates app coordinates into
// this local form space before forwarding mouse messages.
func (m *FormModel) pickerGeometry() (panelX, panelY, browserX, browserY, browserWidth, browserHeight int, framed bool) {
	width, height := max(1, m.width), max(1, m.height)
	outerWidth, outerHeight := min(78, width-4), min(20, height-4)
	if outerWidth >= 32 && outerHeight >= 9 {
		panelX, panelY = (width-outerWidth)/2, (height-outerHeight)/2
		browserX, browserY = panelX+1, panelY+1
		return panelX, panelY, browserX, browserY, outerWidth - 2, outerHeight - 2, true
	}
	return 0, 0, 0, 0, width, height, false
}

func (m *FormModel) pickerMouse(msg tea.MouseMsg) (tea.MouseMsg, bool) {
	mouse := msg.Mouse()
	panelX, panelY, browserX, browserY, browserWidth, browserHeight, framed := m.pickerGeometry()
	panelWidth, panelHeight := browserWidth, browserHeight
	if framed {
		panelWidth += 2
		panelHeight += 2
	}
	if mouse.X < panelX || mouse.X >= panelX+panelWidth || mouse.Y < panelY || mouse.Y >= panelY+panelHeight {
		return nil, false
	}
	switch event := msg.(type) {
	case tea.MouseClickMsg:
		outerLines := strings.Split(ansi.Strip(m.View().Content), "\n")
		innerLines := strings.Split(ansi.Strip(m.browser.View().Content), "\n")
		if mouse.Y < 0 || mouse.Y >= len(outerLines) {
			return nil, false
		}
		for innerY, innerLine := range innerLines {
			length, outerStart, innerStart := longestSharedRun(outerLines[mouse.Y], innerLine)
			if length == 0 {
				continue
			}
			outerRun := []rune(outerLines[mouse.Y])[outerStart : outerStart+length]
			if strings.TrimSpace(string(outerRun)) == "" {
				continue
			}
			outerColumn := ansi.StringWidth(string([]rune(outerLines[mouse.Y])[:outerStart]))
			innerColumn := ansi.StringWidth(string([]rune(innerLine)[:innerStart]))
			runWidth := ansi.StringWidth(string(outerRun))
			if mouse.X < outerColumn || mouse.X >= outerColumn+runWidth {
				continue
			}
			event.X, event.Y = innerColumn+mouse.X-outerColumn, innerY
			return event, true
		}
		return nil, true
	case tea.MouseWheelMsg:
		event.X, event.Y = mouse.X-browserX, mouse.Y-browserY
		return event, true
	default:
		return nil, false
	}
}

func longestSharedRun(left, right string) (length, leftStart, rightStart int) {
	a, b := []rune(left), []rune(right)
	previous := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				current[j] = previous[j-1] + 1
				if current[j] > length {
					length, leftStart, rightStart = current[j], i-current[j], j-current[j]
				}
			}
		}
		previous = current
	}
	return length, leftStart, rightStart
}

func (m *FormModel) pickerView() tea.View {
	base := m.layout().content
	if len(m.sections) > 0 {
		base = modalCanvas(base)
	} else {
		base = navyCanvas(base)
	}
	width := max(1, m.width)
	inner := m.browser.View().Content
	panelX, panelY, _, _, innerWidth, innerHeight, framed := m.pickerGeometry()
	panel := inner
	if framed {
		panel = lipgloss.NewStyle().Width(innerWidth).Height(innerHeight).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#8AB4F8")).
			Background(lipgloss.Color("#09266F")).Render(inner)
	}
	if gotWidth := lipgloss.Width(panel); gotWidth > width {
		return tea.NewView(fmt.Sprintf("%s\n%s", base, inner))
	}
	compositor := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(panel).X(panelX).Y(panelY).Z(1),
	)
	view := tea.NewView(compositor.Render())
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
