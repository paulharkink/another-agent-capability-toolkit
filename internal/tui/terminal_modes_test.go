package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBubbleTeaRestoresTerminalModesInPTY(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script utility unavailable for PTY lifecycle check")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childArgs := []string{binary, "-test.run=^TestBubbleTeaTerminalPTYChild$"}
	var cmd *exec.Cmd
	if runtime.GOOS == "linux" {
		cmd = exec.Command(script, "-q", "-e", "-c", shellJoin(childArgs), "/dev/null")
	} else {
		cmd = exec.Command(script, append([]string{"-q", "/dev/null"}, childArgs...)...)
	}
	cmd.Env = append(os.Environ(), "AACT_TERMINAL_PTY_CHILD=1")
	cmd.Stdin = strings.NewReader("\x1b[21~") // F10
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Bubble Tea PTY child did not exit cleanly: %v\noutput=%q", err, output)
	}
	for _, sequence := range []string{"\x1b[?1049h", "\x1b[?1049l", "\x1b[?25l", "\x1b[?25h", "\x1b[?1002h", "\x1b[?1002l", "\x1b[?1006h", "\x1b[?1006l"} {
		if !strings.Contains(string(output), sequence) {
			t.Errorf("PTY output omitted Bubble Tea terminal mode sequence %q: %q", sequence, output)
		}
	}
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

func TestBubbleTeaTerminalPTYChild(t *testing.T) {
	if os.Getenv("AACT_TERMINAL_PTY_CHILD") != "1" {
		return
	}
	program := tea.NewProgram(NewContext(context.Background(), fixtureBackend{}), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	if _, err := program.Run(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
}
