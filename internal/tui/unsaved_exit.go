package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type unsavedExitState struct {
	reason   string
	selected int
	apply    func() tea.Cmd
	discard  func() tea.Cmd
	onResult func(operationMsg) tea.Cmd
}

var unsavedExitChoices = []string{"Apply changes", "Discard changes", "Keep editing"}

// exitPopupView keeps the active form visible beneath a compact centered choice.
func (m *Model) exitPopupView() tea.View {
	state := m.unsavedExit
	m.unsavedExit = nil
	background := m.View().Content
	m.unsavedExit = state
	lines := strings.Split(background, "\n")
	width := min(52, max(38, m.width-6))
	height := 9
	x, y := max(0, (m.width-width)/2), max(0, (m.height-height)/2)
	box := make([]string, height)
	gold := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD36E"))
	panel := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1F5F9")).Background(lipgloss.Color("#123B83"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#0B2F70")).Background(lipgloss.Color("#FFD36E")).Bold(true)
	box[0] = gold.Render("╭" + strings.Repeat("─", width-2) + "╮")
	box[1] = gold.Render("│") + panel.Render(center("Unsaved changes", width-2)) + gold.Render("│")
	box[2] = gold.Render("│") + panel.Render(center("Apply before leaving this configuration?", width-2)) + gold.Render("│")
	summary := "Changed: no fields"
	if m.registration != nil {
		if m.registration.Transport != m.registration.OriginalTransport {
			summary = "Changed: MCP transport"
		} else {
			summary = "Changed: Agent registrations"
		}
	} else if m.form != nil {
		if labels, remaining := m.form.ChangedFieldSummary(3); len(labels) > 0 {
			summary = "Changed: " + strings.Join(labels, ", ")
			if remaining > 0 {
				summary += fmt.Sprintf(" and %d others", remaining)
			}
		}
	}
	box[3] = gold.Render("│") + panel.Render(fit(summary, width-2)) + gold.Render("│")
	for i, choice := range unsavedExitChoices {
		mark := "  "
		if i == m.unsavedExit.selected {
			mark = "› "
		}
		label := mark + choice
		inside := fit(label, width-2)
		if i == m.unsavedExit.selected {
			inside = selectedStyle.Render(inside)
		} else {
			inside = panel.Render(inside)
		}
		box[4+i] = gold.Render("│") + inside + gold.Render("│")
	}
	box[7] = gold.Render("│") + panel.Render(center("↑↓ / Tab · Enter · Esc keeps editing", width-2)) + gold.Render("│")
	box[8] = gold.Render("╰" + strings.Repeat("─", width-2) + "╯")
	for row := 0; row < height && y+row < len(lines); row++ {
		lines[y+row] = ansi.Cut(lines[y+row], 0, x) + box[row] + ansi.Cut(lines[y+row], x+width, m.width)
	}
	v := tea.NewView(navyCanvas(strings.Join(lines, "\n")))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func center(value string, width int) string {
	padding := max(0, width-ansi.StringWidth(value))
	left := padding / 2
	return strings.Repeat(" ", left) + value + strings.Repeat(" ", padding-left)
}

func (m *Model) unsavedExitKey(stroke string) tea.Cmd {
	switch stroke {
	case "esc":
		m.unsavedExit = nil
	case "up", "left", "shift+tab":
		m.unsavedExit.selected = (m.unsavedExit.selected + len(unsavedExitChoices) - 1) % len(unsavedExitChoices)
	case "down", "right", "tab":
		m.unsavedExit.selected = (m.unsavedExit.selected + 1) % len(unsavedExitChoices)
	case "enter", " ":
		return m.chooseUnsavedExit(m.unsavedExit.selected)
	}
	return nil
}

func (m *Model) unsavedExitMouse(event tea.MouseClickMsg) tea.Cmd {
	if event.Button != tea.MouseLeft {
		return nil
	}
	width := min(52, max(38, m.width-6))
	x, y := max(0, (m.width-width)/2), max(0, (m.height-9)/2)
	if event.X < x || event.X >= x+width || event.Y < y+4 || event.Y > y+6 {
		return nil
	}
	choice := event.Y - y - 4
	return m.chooseUnsavedExit(choice)
}

func (m *Model) chooseUnsavedExit(choice int) tea.Cmd {
	state := m.unsavedExit
	if state == nil {
		return nil
	}
	switch choice {
	case 0:
		m.unsavedExit = nil
		if state.apply != nil {
			m.unsavedExitIntent = state
			return state.apply()
		}
		if m.form == nil {
			return nil
		}
		m.unsavedExitApplying = true
		m.unsavedExitIntent = state
		if m.registration != nil {
			m.registration = nil
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		return cmd
	case 1:
		m.unsavedExit = nil
		if state.discard != nil {
			return state.discard()
		}
		if state.reason == "quit" {
			m.discardFormAndContinue(state.reason)
			return tea.Quit
		}
		cmd := m.discardFormAndContinue(state.reason)
		if cmd != nil {
			return cmd
		}
	case 2:
		m.unsavedExit = nil
	}
	return nil
}

func (m *Model) discardFormAndContinue(reason string) tea.Cmd {
	m.registration = nil
	if m.form != nil {
		m.form.SetUnsavedExitGuard(false)
		m.form.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	}
	if m.workspace != nil && m.workspace.Active {
		if reason == "back" {
			m.workspace.Draft = nil
			m.workspace.Active = false
			m.form = nil
			m.pendingSetup = nil
			m.pendingSetupField = ""
			m.output = "Back"
			return m.load()
		}
		m.workspace.Draft = nil
		m.workspace.Active = false
	}
	m.form = nil
	m.management.FormOverlay = false
	if reason == "back" {
		m.navigate("Catalog")
		return m.load()
	}
	if reason == "cancel" {
		m.pendingRegistration = nil
		m.pendingRegistrationRemoval = false
		m.pendingSetup = nil
		m.pendingSetupField = ""
		m.pendingDefaultAgents = false
		m.pendingMCPImageSource = false
		m.workspace = nil
		m.output = "Cancelled"
	}
	return m.load()
}

func (m *Model) finishUnsavedExit(intent *unsavedExitState) tea.Cmd {
	if intent == nil {
		return nil
	}
	if m.workspace != nil {
		m.workspace.Draft = nil
		if intent.reason == "back" {
			m.workspace.Active = false
		}
	}
	m.form = nil
	m.management.FormOverlay = false
	switch intent.reason {
	case "quit":
		return tea.Quit
	case "back":
		m.pendingSetup = nil
		m.pendingSetupField = ""
		m.output = "Back"
		return m.load()
	case "cancel":
		m.pendingRegistration = nil
		m.pendingRegistrationRemoval = false
		m.pendingSetup = nil
		m.pendingSetupField = ""
		m.pendingDefaultAgents = false
		m.pendingMCPImageSource = false
		m.workspace = nil
		m.output = "Cancelled"
		return m.load()
	default:
		m.navigate("Catalog")
		return m.load()
	}
}
