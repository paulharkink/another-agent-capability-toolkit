package picker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
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
		resolved, e := config.ResolvePath(initial, filepath.Join(cwd, "picker"))
		if e != nil {
			m.message = e.Error()
			m.pathText = initial
		} else if info, e := os.Stat(resolved); e == nil {
			if info.IsDir() {
				m.dir = resolved
				m.pathText = resolved
			} else {
				m.dir = filepath.Dir(resolved)
				m.pathText = resolved
			}
		} else {
			m.dir = filepath.Dir(resolved)
			m.pathText = resolved
			m.message = fmt.Sprintf("Path unavailable: %v", e)
		}
	}
	m.reload()
	if initial != "" {
		if _, err := os.Stat(m.pathText); err != nil {
			m.message = fmt.Sprintf("Path unavailable: %v", err)
		}
	}
	m.pathCursor = len([]rune(m.pathText))
	return m
}

func (m *BrowserModel) Init() tea.Cmd { return nil }

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
		m.done = true
		m.err = m.ctx.Err()
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.width < 1 {
			m.width = 1
		}
		if m.height < 1 {
			m.height = 1
		}
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
		if m.focus == 1 {
			m.editPath(msg)
			return m, nil
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
				m.openSelected()
			case 3:
				m.selectCurrentOrEntry()
			case 4:
				m.done = true
				m.err = ErrCancelled
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *BrowserModel) editPath(key tea.KeyPressMsg) {
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
		m.navigatePath()
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

func (m *BrowserModel) navigatePath() {
	p, err := config.ResolvePath(strings.TrimSpace(m.pathText), filepath.Join(m.dir, "picker"))
	if err != nil {
		m.message = err.Error()
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		m.message = fmt.Sprintf("Path unavailable: %v", err)
		return
	}
	if info.IsDir() {
		m.dir = p
		m.pathText = p
		m.pathCursor = len([]rune(p))
		m.selected = -1
		m.reload()
		return
	}
	if m.kind == "file" && info.Mode().IsRegular() {
		m.finish(p)
		return
	}
	m.message = fmt.Sprintf("%s is not a %s", p, m.kind)
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
	n := m.height - 10
	if n < 1 {
		n = 1
	}
	return n
}
func (m *BrowserModel) openSelected() {
	if m.selected >= 0 && m.selected < len(m.entries) && m.entries[m.selected].Directory {
		m.dir = m.entries[m.selected].Path
		m.pathText = m.dir
		m.pathCursor = len([]rune(m.pathText))
		m.selected = -1
		m.reload()
		return
	}
	m.navigatePath()
}
func (m *BrowserModel) selectCurrentOrEntry() {
	if m.kind == "directory" {
		m.finish(m.dir)
		return
	}
	if m.selected >= 0 && m.selected < len(m.entries) && m.entries[m.selected].Selectable {
		m.finish(m.entries[m.selected].Path)
		return
	}
	m.navigatePath()
}
func (m *BrowserModel) finish(path string) {
	validated, err := selectedPath(path, m.kind, m.dir)
	if err != nil {
		m.message = err.Error()
		return
	}
	m.done = true
	m.result = validated
	m.err = nil
}

func (m *BrowserModel) View() tea.View {
	var lines []string
	if m.width < 30 || m.height < 7 {
		lines = []string{"File picker", "Terminal too small (minimum 30×7)", "Esc Cancel"}
		return tea.NewView(strings.Join(lines, "\n"))
	}
	lines = append(lines, browserBlue.Bold(true).Render("Browse "+m.kind), browserMuted.Render("Current: "+m.dir))
	pathLabel := "Path: " + m.pathText
	if m.focus == 1 {
		chars := []rune(m.pathText)
		cursor := m.pathCursor
		if cursor < 0 {
			cursor = 0
		}
		if cursor > len(chars) {
			cursor = len(chars)
		}
		pathLabel = browserFocus.Render("Path: " + string(chars[:cursor]) + "▏" + string(chars[cursor:]))
	}
	lines = append(lines, pathLabel)
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
	if start > 0 {
		lines = append(lines, browserMuted.Render("↑ more"))
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
		lines = append(lines, label)
	}
	if end < len(m.entries) {
		lines = append(lines, browserMuted.Render("↓ more"))
	}
	openLabel := "Open directory"
	if m.focus == 2 {
		openLabel = browserFocus.Render("[ Open directory ]")
	}
	selectLabel := "Select file"
	if m.kind == "directory" {
		selectLabel = "Select this directory"
	}
	if m.focus == 3 {
		selectLabel = browserFocus.Render("[ " + selectLabel + " ]")
	}
	cancelLabel := "Cancel"
	if m.focus == 4 {
		cancelLabel = browserFocus.Render("[ Cancel ]")
	}
	lines = append(lines, openLabel+"   "+selectLabel+"   "+cancelLabel)
	if m.message != "" {
		lines = append(lines, browserError.Render(m.message))
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return tea.NewView(strings.Join(lines, "\n"))
}
