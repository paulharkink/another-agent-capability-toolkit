package picker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type BrowserModel struct {
	ctx                 context.Context
	kind, dir, pathText string
	entries             []Entry
	selected, offset    int
	pathCursor          int
	focus               int // 0 list, 1 path, 2 Open, 3 Select, 4 Cancel
	width, height       int
	result              string
	err                 error
	done                bool
	message             string
	resolveCancel       context.CancelFunc
	resolveRequest      uint64
}

type resolvedPathMsg struct {
	owner      *BrowserModel
	request    uint64
	expression string
	initial    bool
	path       string
	err        error
}

var browserBlue = lipgloss.NewStyle().Foreground(lipgloss.Color("#8AB4F8"))
var browserFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#2459A6")).Bold(true)
var browserMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B96A8"))
var browserError = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8585"))

func NewBrowser(ctx context.Context, kind, initial string) *BrowserModel {
	if ctx == nil {
		ctx = context.Background()
	}
	m := &BrowserModel{ctx: ctx, kind: kind, width: 80, height: 24, selected: -1, focus: 0}
	if kind != "file" && kind != "directory" {
		m.message = fmt.Sprintf("Unsupported picker kind %q", kind)
		return m
	}
	cwd, err := os.Getwd()
	if err != nil {
		m.message = err.Error()
		return m
	}
	m.dir = cwd
	if initial != "" {
		if resolved, e := filepath.Abs(initial); e == nil {
			if info, statErr := os.Stat(resolved); statErr == nil {
				if info.IsDir() {
					m.dir = resolved
					m.pathText = resolved
				} else {
					m.dir = filepath.Dir(resolved)
					m.pathText = resolved
				}
			} else {
				m.pathText = initial
			}
		} else {
			m.pathText = initial
		}
	}
	m.reload()
	m.pathCursor = len([]rune(m.pathText))
	return m
}

func (m *BrowserModel) Init() tea.Cmd {
	if m.pathText != "" {
		if _, err := os.Stat(m.pathText); err == nil {
			return nil
		}
		return m.resolvePath(m.pathText, true)
	}
	return nil
}

func (m *BrowserModel) Result() (string, error) {
	if !m.done {
		return "", ErrNotSubmitted
	}
	return m.result, m.err
}

func (m *BrowserModel) reload() {
	entries, err := browserEntries(m.dir, m.kind)
	if err != nil {
		m.entries = nil
		m.message = fmt.Sprintf("Cannot read %s: %v", m.dir, err)
		return
	}
	m.entries = entries
	if m.selected >= len(entries) {
		m.selected = len(entries) - 1
	}
	if m.selected < 0 && len(entries) > 0 {
		m.selected = 0
		if len(entries) > 1 && entries[0].Name == ".." {
			m.selected = 1
		}
	}
	m.message = ""
}

func browserEntries(dir, kind string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []Entry{{Name: "..", Path: filepath.Dir(dir), Directory: true}}
	for _, de := range des {
		name := de.Name()
		p := filepath.Join(dir, name)
		info, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		isDir := info.IsDir()
		if info.Mode()&os.ModeSymlink != 0 {
			if target, e := os.Stat(p); e == nil {
				isDir = target.IsDir()
			}
		}
		regular := false
		if actual, e := os.Stat(p); e == nil {
			regular = actual.Mode().IsRegular()
		}
		if kind == "directory" && !isDir {
			continue
		}
		out = append(out, Entry{Name: name, Path: p, Directory: isDir, Selectable: (kind == "directory" && isDir) || (kind == "file" && regular)})
	}
	return out, nil
}

func (m *BrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.ctx != nil && m.ctx.Err() != nil && !m.done {
		m.cancelResolve()
		m.done = true
		m.err = m.ctx.Err()
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.MouseMsg:
		return m, m.mouseUpdate(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.width < 1 {
			m.width = 1
		}
		if m.height < 1 {
			m.height = 1
		}
		return m, nil
	case resolvedPathMsg:
		if msg.owner != m || msg.request != m.resolveRequest || m.done {
			return m, nil
		}
		m.cancelResolve()
		m.applyResolvedPath(msg)
		return m, nil
	case tea.PasteMsg:
		if m.focus == 1 {
			m.insertPathText(msg.Content)
			m.message = ""
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.done {
			return m, nil
		}
		if msg.Code == tea.KeyEscape {
			m.cancelResolve()
			m.done = true
			m.err = ErrCancelled
			return m, nil
		}
		if msg.Code == tea.KeyTab && msg.Mod&tea.ModShift != 0 {
			m.focus = (m.focus + 4) % 5
			return m, nil
		}
		if msg.Code == tea.KeyTab {
			m.focus = (m.focus + 1) % 5
			return m, nil
		}
		if msg.Code == 'l' && msg.Mod&tea.ModCtrl != 0 {
			m.focus = 1
			m.pathCursor = len([]rune(m.pathText))
			return m, nil
		}
		if msg.Code == 'a' && msg.Mod&tea.ModCtrl != 0 && m.focus == 1 {
			m.pathText = ""
			m.pathCursor = 0
			m.message = ""
			return m, nil
		}
		if m.focus == 1 {
			return m, m.editPath(msg)
		}
		if m.focus == 0 {
			m.handleListKey(msg)
			return m, nil
		}
		if msg.Code == tea.KeyLeft || msg.Code == tea.KeyRight {
			if msg.Code == tea.KeyLeft {
				m.focus = (m.focus + 4) % 5
			} else {
				m.focus = (m.focus + 1) % 5
			}
			return m, nil
		}
		if msg.Code == tea.KeyEnter || msg.Code == tea.KeySpace {
			switch m.focus {
			case 2:
				return m, m.openSelected()
			case 3:
				return m, m.selectCurrentOrEntry()
			case 4:
				m.cancelResolve()
				m.done = true
				m.err = ErrCancelled
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *BrowserModel) mouseUpdate(msg tea.MouseMsg) tea.Cmd {
	if m.done {
		return nil
	}
	mouse := msg.Mouse()
	if _, ok := msg.(tea.MouseWheelMsg); ok {
		if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
			return nil
		}
		m.focus = 0
		code := tea.KeyDown
		if mouse.Button == tea.MouseWheelUp {
			code = tea.KeyUp
		}
		m.handleListKey(tea.KeyPressMsg{Code: code})
		return nil
	}
	if mouse.Button != tea.MouseLeft {
		return nil
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	visibleStart := m.offset
	visibleEnd := min(len(m.entries), visibleStart+m.visibleRows())
	for index := visibleStart; index < visibleEnd; index++ {
		entry := m.entries[index]
		label := entry.Name
		if entry.Directory {
			label += "/"
		} else if !entry.Selectable {
			label += " (unavailable)"
		}
		if mouse.Y < len(lines) && strings.Contains(lines[mouse.Y], label) {
			m.focus = 0
			m.selected = index
			return nil
		}
	}
	if mouse.Y < len(lines) && strings.Contains(lines[mouse.Y], "Path") {
		m.focus = 1
		m.pathCursor = len([]rune(m.pathText))
		return nil
	}
	// Locate buttons from the actual rendered rows so wrapped controls remain clickable.
	labels := []string{"Open directory", "Select file", "Cancel"}
	if m.kind == "directory" {
		labels[1] = "Select this directory"
	}
	for action, label := range labels {
		needle := "[ " + label + " ]"
		for y, line := range lines {
			plain := line
			if y == mouse.Y {
				start := strings.Index(plain, needle)
				if start < 0 || mouse.X < start || mouse.X >= start+len(needle) {
					continue
				}
				m.focus = action + 2
				switch action {
				case 0:
					return m.openSelected()
				case 1:
					return m.selectCurrentOrEntry()
				default:
					m.cancelResolve()
					m.done = true
					m.err = ErrCancelled
					return nil
				}
			}
		}
	}
	return nil
}

func (m *BrowserModel) editPath(key tea.KeyPressMsg) tea.Cmd {
	switch key.Code {
	case tea.KeyBackspace, tea.KeyDelete:
		chars := []rune(m.pathText)
		if key.Code == tea.KeyBackspace && m.pathCursor > 0 {
			chars = append(chars[:m.pathCursor-1], chars[m.pathCursor:]...)
			m.pathCursor--
		} else if key.Code == tea.KeyDelete && m.pathCursor < len(chars) {
			chars = append(chars[:m.pathCursor], chars[m.pathCursor+1:]...)
		}
		m.pathText = string(chars)
	case tea.KeyEnter:
		return m.navigatePath()
	case tea.KeyLeft:
		if m.pathCursor > 0 {
			m.pathCursor--
		}
	case tea.KeyRight:
		if m.pathCursor < len([]rune(m.pathText)) {
			m.pathCursor++
		}
	case tea.KeyHome:
		m.pathCursor = 0
	case tea.KeyEnd:
		m.pathCursor = len([]rune(m.pathText))
	default:
		if key.Text != "" && key.Mod == 0 {
			m.insertPathText(key.Text)
			m.message = ""
		}
	}
	return nil
}

func (m *BrowserModel) insertPathText(text string) {
	chars := []rune(m.pathText)
	insert := []rune(text)
	if m.pathCursor < 0 {
		m.pathCursor = 0
	}
	if m.pathCursor > len(chars) {
		m.pathCursor = len(chars)
	}
	chars = append(chars[:m.pathCursor], append(insert, chars[m.pathCursor:]...)...)
	m.pathText = string(chars)
	m.pathCursor += len(insert)
}

func (m *BrowserModel) navigatePath() tea.Cmd {
	return m.resolvePath(strings.TrimSpace(m.pathText), false)
}

func (m *BrowserModel) resolvePath(expression string, initial bool) tea.Cmd {
	m.cancelResolve()
	ctx, cancel := context.WithCancel(m.ctx)
	m.resolveCancel = cancel
	m.resolveRequest++
	request, owner, cwd := m.resolveRequest, m, m.dir
	m.message = "Resolving path with invoking shell…"
	return func() tea.Msg {
		path, err := ResolveShellPath(ctx, expression, cwd)
		return resolvedPathMsg{owner: owner, request: request, expression: expression, initial: initial, path: path, err: err}
	}
}

func (m *BrowserModel) cancelResolve() {
	if m.resolveCancel != nil {
		m.resolveCancel()
		m.resolveCancel = nil
	}
}

func (m *BrowserModel) applyResolvedPath(msg resolvedPathMsg) {
	if msg.err != nil {
		if msg.err == context.Canceled || msg.err == context.DeadlineExceeded {
			return
		}
		m.message = fmt.Sprintf("Path unavailable: %v", msg.err)
		return
	}
	info, err := os.Stat(msg.path)
	if err != nil {
		m.message = fmt.Sprintf("Path unavailable: %v", err)
		return
	}
	if info.IsDir() {
		m.dir = msg.path
		m.pathText = msg.path
		m.pathCursor = len([]rune(msg.path))
		m.selected = -1
		m.reload()
		return
	}
	if m.kind == "file" && info.Mode().IsRegular() {
		if msg.initial {
			m.dir = filepath.Dir(msg.path)
			m.pathText = msg.path
			m.pathCursor = len([]rune(msg.path))
			m.reload()
			return
		}
		m.finish(msg.path)
		return
	}
	m.message = fmt.Sprintf("%s is not a %s", msg.path, m.kind)
}

func (m *BrowserModel) handleListKey(key tea.KeyPressMsg) {
	switch key.Code {
	case tea.KeyUp:
		if m.selected > 0 {
			m.selected--
		}
	case tea.KeyDown:
		if m.selected+1 < len(m.entries) {
			m.selected++
		}
	case tea.KeyHome:
		m.selected = 0
	case tea.KeyEnd:
		m.selected = len(m.entries) - 1
	case tea.KeyEnter:
		if m.selected >= 0 && m.selected < len(m.entries) {
			e := m.entries[m.selected]
			if e.Directory {
				m.dir = e.Path
				m.pathText = m.dir
				m.pathCursor = len([]rune(m.pathText))
				m.selected = -1
				m.reload()
			} else if e.Selectable {
				m.finish(e.Path)
			}
		}
	case tea.KeySpace:
		m.selectCurrentOrEntry()
	}
	visible := m.visibleRows()
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+visible {
		m.offset = m.selected - visible + 1
	}
}

func (m *BrowserModel) visibleRows() int {
	reserve := 3 + len(m.actionLines()) + 2 // heading/path, actions, address hint and scroll cue
	if m.message != "" {
		reserve++
	}
	n := m.height - reserve
	if n < 1 {
		n = 1
	}
	return n
}
func (m *BrowserModel) openSelected() tea.Cmd {
	if m.selected >= 0 && m.selected < len(m.entries) && m.entries[m.selected].Directory {
		m.dir = m.entries[m.selected].Path
		m.pathText = m.dir
		m.pathCursor = len([]rune(m.pathText))
		m.selected = -1
		m.reload()
		return nil
	}
	return m.navigatePath()
}
func (m *BrowserModel) selectCurrentOrEntry() tea.Cmd {
	if m.kind == "directory" {
		m.finish(m.dir)
		return nil
	}
	if m.selected >= 0 && m.selected < len(m.entries) && m.entries[m.selected].Selectable {
		m.finish(m.entries[m.selected].Path)
		return nil
	}
	return m.navigatePath()
}
func (m *BrowserModel) finish(path string) {
	validated, err := selectedPath(path, m.kind, m.dir)
	if err != nil {
		m.message = err.Error()
		return
	}
	m.done = true
	m.cancelResolve()
	m.result = validated
	m.err = nil
}

func (m *BrowserModel) View() tea.View {
	var lines []string
	if m.width < 30 || m.height < 7 {
		lines = []string{"File picker", "Terminal too small (minimum 30×7)", "Esc Cancel"}
		return tea.NewView(strings.Join(lines, "\n"))
	}
	lines = append(lines, fitPickerLine(browserBlue.Bold(true).Render("Browse "+m.kind), m.width), fitPickerLine(browserMuted.Render("Current: "+m.dir), m.width))
	pathLabel := "Path (Ctrl+L): " + m.pathText
	if m.focus == 1 {
		pathLabel = browserFocus.Render(activePathLine(m.pathText, m.pathCursor, m.width))
		lines = append(lines, pathLabel)
	} else {
		lines = append(lines, fitPickerLine(pathLabel, m.width))
	}
	rows := m.visibleRows()
	start := m.offset
	if start < 0 {
		start = 0
	}
	if start > len(m.entries) {
		start = len(m.entries)
	}
	end := start + rows
	if end > len(m.entries) {
		end = len(m.entries)
	}
	cue := ""
	if start > 0 && end < len(m.entries) {
		cue = "↑ ↓ more"
	} else if start > 0 {
		cue = "↑ more"
	} else if end < len(m.entries) {
		cue = "↓ more"
	}
	if cue != "" && m.message == "" {
		lines = append(lines, fitPickerLine(browserMuted.Render(cue), m.width))
	}
	for i := start; i < end; i++ {
		e := m.entries[i]
		label := e.Name
		if e.Directory {
			label += "/"
		}
		if !e.Selectable && !e.Directory {
			label += " (unavailable)"
		}
		if i == m.selected && m.focus == 0 {
			label = browserFocus.Render("› " + label)
		} else if i == m.selected {
			label = "  " + label
		} else {
			label = "  " + label
		}
		lines = append(lines, fitPickerLine(label, m.width))
	}
	lines = append(lines, m.actionLines()...)
	lines = append(lines, fitPickerLine(browserMuted.Render("Ctrl+L address · Ctrl+A replace · Tab controls"), m.width))
	if m.message != "" {
		lines = append(lines, fitPickerLine(browserError.Render(m.message), m.width))
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return tea.NewView(strings.Join(lines, "\n"))
}

func (m *BrowserModel) actionLines() []string {
	labels := []string{"Open directory", "Select file", "Cancel"}
	if m.kind == "directory" {
		labels[1] = "Select this directory"
	}
	var buttons []string
	for i, label := range labels {
		if m.focus == i+2 {
			buttons = append(buttons, browserFocus.Render("[ "+label+" ]"))
		} else {
			buttons = append(buttons, "[ "+label+" ]")
		}
	}
	joined := strings.Join(buttons, "   ")
	if ansi.StringWidth(joined) <= m.width {
		return []string{joined}
	}
	for i := range buttons {
		buttons[i] = fitPickerLine(buttons[i], m.width)
	}
	return buttons
}

func fitPickerLine(line string, width int) string {
	if width < 1 {
		return ""
	}
	return ansi.Truncate(line, width, "…")
}

func activePathLine(path string, cursor, width int) string {
	chars := []rune(path)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(chars) {
		cursor = len(chars)
	}
	full := "Path: " + string(chars[:cursor]) + "▏" + string(chars[cursor:])
	if ansi.StringWidth(full) <= width {
		return full
	}
	const label = "Path: "
	room := width - ansi.StringWidth(label) - ansi.StringWidth("▏")
	if room < 0 {
		return ansi.Truncate(full, width, "…")
	}
	before, after := string(chars[:cursor]), string(chars[cursor:])
	leftBudget, rightBudget := room/2, room-room/2
	if before == "" {
		leftBudget, rightBudget = 0, room
	} else if after == "" {
		leftBudget, rightBudget = room, 0
	}
	left := suffixWithin(before, leftBudget)
	right := prefixWithin(after, rightBudget)
	// Reclaim space when one side of the cursor has less text than its share.
	if extra := leftBudget - ansi.StringWidth(left); extra > 0 {
		rightBudget += extra
		right = prefixWithin(after, rightBudget)
	}
	if extra := rightBudget - ansi.StringWidth(right); extra > 0 {
		leftBudget += extra
		left = suffixWithin(before, leftBudget)
	}
	line := label + left + "▏" + right
	for ansi.StringWidth(line) > width {
		if right != "" && (left == "" || ansi.StringWidth(right) > ansi.StringWidth(left)) {
			right = prefixWithin(right, ansi.StringWidth(right)-1)
		} else if left != "" {
			left = suffixWithin(left, ansi.StringWidth(left)-1)
		} else {
			break
		}
		line = label + left + "▏" + right
	}
	return line
}

func suffixWithin(text string, width int) string {
	if width <= 0 || text == "" {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	for remove := 1; remove <= ansi.StringWidth(text); remove++ {
		candidate := ansi.TruncateLeft(text, remove, "…")
		if ansi.StringWidth(candidate) <= width {
			return candidate
		}
	}
	return "…"
}

func prefixWithin(text string, width int) string {
	if width <= 0 || text == "" {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	for keep := width - 1; keep > 0; keep-- {
		candidate := ansi.Truncate(text, keep, "…")
		if ansi.StringWidth(candidate) <= width {
			return candidate
		}
	}
	return "…"
}
