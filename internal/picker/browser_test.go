package picker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestUXBrowserUnicodeCursorEditing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "café.txt")
	if err := os.WriteFile(path, []byte("unicode"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "file", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(m, tea.KeyTab, "")
	m.Update(tea.PasteMsg{Content: path})
	// Move left over t, x, t, and dot to place the cursor after the multibyte é.
	for i := 0; i < 4; i++ {
		browserKey(m, tea.KeyLeft, "")
	}
	browserKey(m, tea.KeyBackspace, "")
	if !utf8.ValidString(m.pathText) {
		t.Fatalf("backspace split a UTF-8 code point: %q", m.pathText)
	}
	browserKey(m, 'é', "é")
	if m.pathText != path {
		t.Fatalf("Unicode edit produced %q, want %q", m.pathText, path)
	}
	browserKey(m, tea.KeyEnter, "")
	got, err := m.Result()
	if err != nil || got != path {
		t.Fatalf("Result() = %q, %v; want %q", got, err, path)
	}
}

func TestUXBrowserSymlinkFileAndDirectorySelection(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target-dir")
	if err := os.Mkdir(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(targetDir, "inside.txt")
	if err := os.WriteFile(targetFile, []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(root, "directory-link")
	if err := os.Symlink(targetDir, dirLink); err != nil {
		t.Skipf("platform cannot create directory symlink: %v", err)
	}
	fileLink := filepath.Join(root, "file-link")
	if err := os.Symlink(targetFile, fileLink); err != nil {
		t.Skipf("platform cannot create file symlink: %v", err)
	}

	filePicker := NewBrowser(context.Background(), "file", root)
	filePicker.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(filePicker, tea.KeyDown, "") // from directory-link to file-link
	browserKey(filePicker, tea.KeyEnter, "")
	if got, err := filePicker.Result(); err != nil || got != fileLink {
		t.Fatalf("symlink file selection = %q, %v; want %q", got, err, fileLink)
	}

	directoryPicker := NewBrowser(context.Background(), "directory", root)
	directoryPicker.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(directoryPicker, tea.KeyEnter, "") // enter directory-link
	for i := 0; i < 3; i++ {
		browserKey(directoryPicker, tea.KeyTab, "")
	}
	browserKey(directoryPicker, tea.KeyEnter, "") // Select this directory
	if got, err := directoryPicker.Result(); err != nil || got != dirLink {
		t.Fatalf("symlink directory selection = %q, %v; want %q", got, err, dirLink)
	}

	contents := NewBrowser(context.Background(), "file", root)
	contents.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(contents, tea.KeyEnter, "") // enter directory-link
	if !strings.Contains(contents.View().Content, "inside.txt") {
		t.Fatalf("symlink directory target was not listed:\n%s", contents.View().Content)
	}
}

func browserKey(m *BrowserModel, code rune, text string) {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	if cmd != nil {
		m.Update(cmd())
	}
}

func TestUXBrowserFileEnterNavigatesThenSelects(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".kube"), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, ".kube", "config")
	if err := os.WriteFile(config, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "file", root)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if got := m.View().Content; !strings.Contains(got, ".kube") {
		t.Fatalf("dot directory absent:\n%s", got)
	}
	browserKey(m, tea.KeyEnter, "")
	if _, err := m.Result(); !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("directory Enter submitted: %v", err)
	}
	if !strings.Contains(m.View().Content, "config") {
		t.Fatalf("did not navigate into .kube:\n%s", m.View().Content)
	}
	browserKey(m, tea.KeyEnter, "")
	got, err := m.Result()
	if err != nil || got != config {
		t.Fatalf("Result() = %q, %v; want %q", got, err, config)
	}

	byAction := NewBrowser(context.Background(), "file", root)
	byAction.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(byAction, tea.KeyEnter, "")
	for i := 0; i < 3; i++ {
		browserKey(byAction, tea.KeyTab, "")
	}
	browserKey(byAction, tea.KeyEnter, "")
	if selected, err := byAction.Result(); err != nil || selected != config {
		t.Fatalf("Select file action returned %q, %v; want %q", selected, err, config)
	}
}

func TestUXBrowserDirectoryRequiresSelectThisDirectory(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "directory", root)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(m, tea.KeyEnter, "")
	if _, err := m.Result(); !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("row Enter selected directory: %v", err)
	}
	if m.dir != child {
		t.Fatal("Enter did not navigate into selected directory")
	}
	// Focus the visible Select this directory control and activate it.
	browserKey(m, tea.KeyTab, "")
	browserKey(m, tea.KeyTab, "")
	browserKey(m, tea.KeyTab, "")
	browserKey(m, tea.KeyEnter, "")
	got, err := m.Result()
	if err != nil || got != child {
		t.Fatalf("Result() = %q, %v; want %q", got, err, child)
	}
}

func TestUXBrowserEscapeCancelsAndDotfilesVisible(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".hidden"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "file", root)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if !strings.Contains(m.View().Content, ".hidden") {
		t.Fatal("dotfile not visible by default")
	}
	browserKey(m, tea.KeyEscape, "")
	if _, err := m.Result(); !errors.Is(err, ErrCancelled) {
		t.Fatalf("Result() error = %v, want cancellation", err)
	}
}

func TestUXBrowserPasteManualPathPreservesInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "selected file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "file", "")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 24})
	browserKey(m, tea.KeyTab, "") // editable Path control
	m.Update(tea.PasteMsg{Content: path})
	if view := m.View().Content; !strings.Contains(view, path) {
		t.Fatalf("pasted path not retained in view:\n%s", view)
	}
	browserKey(m, tea.KeyEnter, "")
	got, err := m.Result()
	if err != nil || got != path {
		t.Fatalf("Result() = %q, %v; want %q", got, err, path)
	}
}

func TestUXBrowserAddressUsesInvokingShellExpansion(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not installed")
	}
	t.Setenv("AACT_PICKER_SHELL", shell)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "directory", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	browserKey(m, tea.KeyTab, "")
	m.Update(tea.PasteMsg{Content: "~"})
	browserKey(m, tea.KeyEnter, "")
	if m.dir != home {
		t.Fatalf("shell-expanded address navigated to %q, want invoking-shell home %q; message=%q", m.dir, home, m.message)
	}
}

func TestUXBrowserAddressCanReplaceCurrentPathWithPaste(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "selected file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "file", filepath.Join(root, "old file"))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: path})
	if m.pathText != path {
		t.Fatalf("pasted address did not replace current path: got %q want %q", m.pathText, path)
	}
	browserKey(m, tea.KeyEnter, "")
	if got, err := m.Result(); err != nil || got != path {
		t.Fatalf("address selection = %q, %v; want %q", got, err, path)
	}
}

func TestUXBrowserIgnoresResolverResultAfterCancel(t *testing.T) {
	m := NewBrowser(context.Background(), "directory", "")
	cmd := m.resolvePath("~", false)
	browserKey(m, tea.KeyEscape, "")
	m.Update(cmd())
	if _, err := m.Result(); !errors.Is(err, ErrCancelled) {
		t.Fatalf("late resolver result changed cancelled picker: %v", err)
	}
}

func TestUXBrowserMouseNavigatesAddressAndCancels(t *testing.T) {
	root := t.TempDir()
	hiddenDir := filepath.Join(root, ".hidden-dir")
	if err := os.Mkdir(hiddenDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenDir, ".hidden-file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "file", root)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	browserClickText(t, m, ".hidden-dir/")
	if !strings.Contains(ansi.Strip(m.View().Content), "› .hidden-dir/") {
		t.Fatalf("mouse did not highlight hidden directory:\n%s", ansi.Strip(m.View().Content))
	}
	browserClickText(t, m, "[ Open directory ]")
	if m.dir != hiddenDir || !strings.Contains(ansi.Strip(m.View().Content), ".hidden-file") {
		t.Fatalf("Open directory did not navigate into hidden directory:\n%s", ansi.Strip(m.View().Content))
	}
	browserClickText(t, m, "Path (Ctrl+L):")
	if m.focus != 1 {
		t.Fatal("mouse click did not focus the address bar")
	}
	browserClickText(t, m, "[ Cancel ]")
	if _, err := m.Result(); !errors.Is(err, ErrCancelled) {
		t.Fatalf("Cancel button result = %v, want ErrCancelled", err)
	}
}

func TestUXBrowserMouseWheelScrollsFileList(t *testing.T) {
	root := t.TempDir()
	for i := range 12 {
		path := filepath.Join(root, fmt.Sprintf("file-%02d", i))
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewBrowser(context.Background(), "file", root)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 9})
	m.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	if !strings.Contains(ansi.Strip(m.View().Content), "› file-01") {
		t.Fatalf("wheel down did not advance selection to first file:\n%s", ansi.Strip(m.View().Content))
	}
}

func browserClickText(t *testing.T, m *BrowserModel, text string) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if byteOffset := strings.Index(line, text); byteOffset >= 0 {
			x := ansi.StringWidth(line[:byteOffset])
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			return
		}
	}
	t.Fatalf("browser did not render %q:\n%s", text, ansi.Strip(m.View().Content))
}

func TestUXBrowserAbsoluteMissingResolverErrorPreservesDiagnostic(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	m := NewBrowser(context.Background(), "file", missing)
	const diagnostic = "PowerShell could not resolve the typed expression: item not found"
	m.applyResolvedPath(resolvedPathMsg{
		expression: missing,
		initial:    true,
		err:        errors.New(diagnostic),
	})
	if _, err := m.Result(); !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("absolute missing path closed picker: %v", err)
	}
	if m.pathText != missing {
		t.Fatalf("failed resolution changed address: got %q, want %q", m.pathText, missing)
	}
	if !strings.Contains(m.message, "Path unavailable:") || !strings.Contains(m.message, diagnostic) {
		t.Fatalf("resolver diagnostic was not preserved for absolute address: %q", m.message)
	}
}

func TestUXBrowserUnreadableOrMissingPathStaysOpen(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	m := NewBrowser(context.Background(), "file", missing)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 20})
	if cmd := m.Init(); cmd != nil {
		m.Update(cmd())
	}
	if _, err := m.Result(); !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("initial path error closed picker: %v", err)
	}
	if !strings.Contains(strings.ToLower(m.View().Content), "unavailable") {
		t.Fatalf("missing-path error not visible:\n%s", m.View().Content)
	}
	if m.pathText != missing {
		t.Fatalf("missing-path resolution changed the address: got %q, want %q", m.pathText, missing)
	}
	browserKey(m, tea.KeyEscape, "")
	if _, err := m.Result(); !errors.Is(err, ErrCancelled) {
		t.Fatalf("could not cancel after path error: %v", err)
	}
	t.Run("unreadable directory", func(t *testing.T) {
		blocked := filepath.Join(t.TempDir(), "blocked")
		if err := os.Mkdir(blocked, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(blocked, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(blocked, 0700) })
		if _, err := os.ReadDir(blocked); err == nil {
			t.Skip("process can read mode-000 directory")
		}
		picker := NewBrowser(context.Background(), "file", blocked)
		picker.Update(tea.WindowSizeMsg{Width: 90, Height: 20})
		if _, err := picker.Result(); !errors.Is(err, ErrNotSubmitted) {
			t.Fatalf("unreadable directory closed picker: %v", err)
		}
		if !strings.Contains(strings.ToLower(picker.View().Content), "cannot read") {
			t.Fatalf("unreadable error not shown:\n%s", picker.View().Content)
		}
	})
}

func TestUXBrowserResizeKeepsActionsAndScrollCue(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(root, "item-"+string(rune('A'+i))), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewBrowser(context.Background(), "file", root)
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 9})
	view := m.View().Content
	for _, want := range []string{"Open", "Select", "Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q action:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "↑") && !strings.Contains(view, "↓") && !strings.Contains(strings.ToLower(view), "more") {
		t.Fatalf("no visible scroll cue:\n%s", view)
	}
	if lines := strings.Split(view, "\n"); len(lines) > 9 {
		t.Fatalf("view exceeds resized height: %d lines", len(lines))
	}
}

func TestUXBrowserResizeBoundsUnicodeContentAndActions(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, strings.Repeat("界", 45)), 0700); err != nil {
		t.Fatal(err)
	}
	m := NewBrowser(context.Background(), "directory", root)
	m.dir = strings.Repeat("目录/", 12)
	m.pathText = strings.Repeat("路径/", 14)
	m.pathCursor = len([]rune(m.pathText))
	m.message = strings.Repeat("权限错误: 不可读取 ", 8)
	m.focus = 1
	for _, width := range []int{30, 43, 44} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 14})
		view := m.View().Content
		if !strings.Contains(view, "▏") {
			t.Errorf("width %d clipped the active path cursor:\n%s", width, view)
		}
		for lineNumber, line := range strings.Split(view, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("width %d line %d renders %d cells: %q", width, lineNumber+1, got, line)
			}
		}
		for _, action := range []string{"Open directory", "Select this directory", "Cancel"} {
			if !strings.Contains(view, action) {
				t.Errorf("width %d hides action %q:\n%s", width, action, view)
			}
		}
	}
}

func TestUXBrowserLongUnicodePathViewportPreservesCursorContext(t *testing.T) {
	path := strings.Repeat("左", 20) + "LEFT" + "RIGHT" + strings.Repeat("右", 20)
	m := NewBrowser(context.Background(), "file", "")
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	browserKey(m, tea.KeyTab, "")
	m.Update(tea.PasteMsg{Content: path})
	for i := 0; i < 25; i++ {
		browserKey(m, tea.KeyLeft, "")
	}
	view := m.View().Content
	if !strings.Contains(view, "LEFT▏RIGHT") {
		t.Fatalf("bounded path viewport must preserve text on both sides of the cursor:\n%s", view)
	}
	for lineNumber, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > 30 {
			t.Fatalf("line %d renders %d cells at width 30:\n%s", lineNumber+1, got, view)
		}
	}
}

func TestUXTryNativeUnavailableDoesNotReadTerminal(t *testing.T) {
	calls := 0
	native := func(_ context.Context, kind, initial string) (string, error) {
		calls++
		if kind != "file" || initial != "" {
			t.Fatalf("native arguments = %q, %q", kind, initial)
		}
		return "", ErrUnavailable
	}
	if _, err := tryNative(context.Background(), "file", "", native); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("TryNative error = %v, want ErrUnavailable", err)
	}
	if calls != 1 {
		t.Fatalf("native dialog calls = %d, want 1", calls)
	}
}
