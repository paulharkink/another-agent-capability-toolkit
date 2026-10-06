package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUXResultShowsExactLongChildErrorWrappedWithoutBlackRows(t *testing.T) {
	m, _ := homeFixture()
	m.width, m.height = 112, 28
	m.action = "install"
	diagnostic := "command repo-map failed: exit status 1: stderr: " + strings.Repeat("permission denied while opening the configured source directory; ", 4)
	m.Update(operationMsg{origin: "Catalog", output: diagnostic, err: errors.New(diagnostic)})
	view := m.View().Content
	plain := ansi.Strip(view)
	for _, part := range []string{"command repo-map failed", "configured source directory"} {
		if !strings.Contains(plain, part) {
			t.Fatalf("exact child output lost %q:\n%s", part, plain)
		}
	}
	for _, row := range strings.Split(view, "\n") {
		if ansi.StringWidth(row) > m.width {
			t.Fatalf("result row exceeds viewport: width=%d viewport=%d row=%q", ansi.StringWidth(row), m.width, row)
		}
	}
	if !strings.Contains(plain, "↓") || !strings.Contains(plain, "↑") {
		t.Fatalf("long result lacks visible scroll cues:\n%s", plain)
	}
}

func TestUXResultReportsSavedAndActualPerAgentAchievements(t *testing.T) {
	result := resultStateFromOperation("install", "", nil, viewmodel.OperationResult{
		Target: "dev", Step: "register", Saved: true,
		Changes: []state.Installation{{AgentID: "codex", Component: "mcp", Destination: "/tmp/codex.json"}},
		Errors:  []string{"claude: endpoint rejected registration"},
	})
	joined := strings.Join(result.Rows, "\n")
	for _, want := range []string{"Saved: yes", "codex", "/tmp/codex.json", "Failed", "claude: endpoint rejected registration"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("structured operation result missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "claude registered") {
		t.Fatalf("failed agent was reported as registered:\n%s", joined)
	}
}

func TestUXProgressVisibleResizesAndDoesNotClaimUnknownSteps(t *testing.T) {
	rows := operationProgressRows("Install", "dev", "", 24)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "Preparing") || strings.Contains(joined, "Starting") || strings.Contains(joined, "Registering") {
		t.Fatalf("progress invented an unknown step:\n%s", joined)
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > 24 {
			t.Fatalf("progress line exceeds resized width: %q", row)
		}
	}
	rows = operationProgressRows("Install", "dev", "authenticate", 54)
	if !strings.Contains(strings.Join(rows, "\n"), "authenticate") {
		t.Fatalf("real operation step is not visible:\n%v", rows)
	}
}
