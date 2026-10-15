package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type uxWorkspaceLogsBackend struct {
	*registrationWorkspaceBackend
	key     state.Key
	content string
}

func (b *uxWorkspaceLogsBackend) UIProfileLogs(_ context.Context, key state.Key) (string, error) {
	b.key = key
	return b.content, nil
}

func TestUXRuntimeVisibleLogsActionOpensSelectedInstanceLogs(t *testing.T) {
	m, backend := openRegistrationActionForUX(t)
	if m.workspace.Instance == nil {
		t.Fatal("fixture has no selected instance observation")
	}
	m.workspace.Instance.RuntimeStatus = "running"
	m.workspace.Instance.Ownership = "local"
	logs := &uxWorkspaceLogsBackend{registrationWorkspaceBackend: backend, content: "selected instance log line"}
	m.backend = logs
	m.configureWorkspaceForm(*m.workspace.Preview)
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	if !strings.Contains(ansi.Strip(m.View().Content), "View plain logs") {
		t.Fatalf("running Runtime section did not show the Logs action:\n%s", ansi.Strip(m.View().Content))
	}
	updated, cmd := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "logs:plain"})
	m = updated.(*Model)
	if cmd == nil || m.home.Modal == nil || m.home.Modal.Kind != "logs" {
		t.Fatalf("visible Runtime Logs action did not open the logs viewer: cmd=%v modal=%+v", cmd, m.home.Modal)
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if logs.key.Target != "foreign" || m.home.Modal == nil || !strings.Contains(strings.Join(m.home.Modal.Rows, "\n"), "selected instance log line") {
		t.Fatalf("Logs action did not show the exact selected instance: key=%+v modal=%+v", logs.key, m.home.Modal)
	}
}

func TestUXNoContainerLogsExplainsNextStepWithoutCallingDockerLogs(t *testing.T) {
	m, backend := typedProfileFixture()
	logs := &logProfileBackend{profileBackend: backend}
	m.backend = logs
	p := m.profiles()[0]
	cmd := m.openProfileLogs(p)
	if cmd != nil || len(logs.actions) != 0 {
		t.Fatalf("logs were requested without a running instance: cmd=%v actions=%v", cmd, logs.actions)
	}
	if m.home.Modal == nil || m.home.Modal.Kind != "logs" || !strings.Contains(strings.Join(m.home.Modal.Rows, "\n"), "No MCP container has been created for this target") {
		t.Fatalf("missing actionable empty state: %+v profile=%+v output=%q", m.home.Modal, p.Instance, m.output)
	}
}

func TestUXRunningLogsBelongToSelectedTargetAndPauseDoesNotMoveSections(t *testing.T) {
	m, backend := typedProfileFixture()
	backend.snapshot.Profiles[0].RuntimeStatus = "running"
	backend.snapshot.Profiles[0].Ownership = "local"
	backend.snapshot.Profiles[0].Key.Target = "selected"
	// Rebuild after mutating the service observation so the selected key is exact.
	m.Update(m.load()())
	p := m.profiles()[0]
	logs := &logProfileBackend{profileBackend: backend}
	m.backend = logs
	cmd := m.openProfileLogs(p)
	if cmd == nil {
		t.Fatal("running target logs did not load")
	}
	m.Update(cmd())
	if logs.key != p.Key || m.home.Modal == nil || !m.home.Modal.Follow {
		t.Fatalf("logs do not belong to selected target/follow state: key=%+v want=%+v modal=%+v", logs.key, p.Key, m.home.Modal)
	}
	m.logKey("up")
	if m.home.Modal.Follow {
		t.Fatalf("pausing/scrolling kept following: modal=%+v", m.home.Modal)
	}
}

func TestUXClosingLogsDoesNotStopRuntime(t *testing.T) {
	m, backend := typedProfileFixture()
	backend.snapshot.Profiles[0].Ownership = "local"
	backend.snapshot.Profiles[0].RuntimeStatus = "running"
	m.Update(m.load()())
	p := m.profiles()[0]
	logs := &logProfileBackend{profileBackend: backend}
	m.backend = logs
	cmd := m.openProfileLogs(p)
	if cmd == nil {
		t.Fatal("running target logs did not load")
	}
	m.Update(cmd())
	m.logKey("q")
	if m.home.Modal != nil || len(logs.actions) != 1 || logs.actions[0] != "logs" {
		t.Fatalf("closing logs invoked a runtime mutation: modal=%+v actions=%v", m.home.Modal, logs.actions)
	}
}

func TestUXLegacyProfileCannotResurrectRawConnectionsEditor(t *testing.T) {
	m, _ := typedProfileFixture()
	_ = openLogAction(t, m)
	if m.workspace == nil || !m.workspace.Active || m.workspace.Section != "Logs" || m.form == nil || m.form.SectionTitle() != "Logs" {
		t.Fatalf("logs route left the exact target workspace: workspace=%+v section=%v", m.workspace, m.form)
	}
	view := m.View().Content
	if strings.Contains(view, "Connections editor") || strings.Contains(view, "Raw Connections") {
		t.Fatalf("legacy raw Connections editor resurfaced: %s", view)
	}
}

func TestUXLogProcessFailureUsesForegroundResult(t *testing.T) {
	m, backend := typedProfileFixture()
	backend.snapshot.Profiles[0].Ownership = "local"
	backend.snapshot.Profiles[0].RuntimeStatus = "running"
	m.Update(m.load()())
	p := m.profiles()[0]
	logs := &logProfileBackend{profileBackend: backend}
	logs.err = context.Canceled
	m.backend = logs
	cmd := m.openProfileLogs(p)
	m.Update(cmd())
	if m.result == nil || !m.result.Failed || !strings.Contains(strings.Join(m.result.Rows, "\n"), "context canceled") {
		t.Fatalf("log process failure was not put in the structured foreground result: result=%+v view=%s", m.result, m.View().Content)
	}
}

func TestUXLogsCloseIgnoresLateSessionResult(t *testing.T) {
	m, backend := typedProfileFixture()
	backend.snapshot.Profiles[0].Ownership = "local"
	backend.snapshot.Profiles[0].RuntimeStatus = "running"
	m.Update(m.load()())
	p := m.profiles()[0]
	logs := &logProfileBackend{profileBackend: backend, logs: "running"}
	m.backend = logs
	cmd := m.openProfileLogs(p)
	m.logKey("esc")
	m.Update(cmd())
	if m.home.Modal != nil {
		t.Fatalf("stale result reopened logs: %+v", m.home.Modal)
	}
}

type blockingLogsBackend struct {
	*profileBackend
	started  chan struct{}
	canceled chan struct{}
}

func (b *blockingLogsBackend) UIProfileLogs(ctx context.Context, _ state.Key) (string, error) {
	close(b.started)
	<-ctx.Done()
	close(b.canceled)
	return "", ctx.Err()
}

func TestUXClosingLogsCancelsPendingFetch(t *testing.T) {
	m, base := typedProfileFixture()
	b := &blockingLogsBackend{profileBackend: base, started: make(chan struct{}), canceled: make(chan struct{})}
	m.backend = b
	p := m.profiles()[0]
	p.Instance.Ownership = "local"
	p.Instance.RuntimeStatus = "running"
	p.Instance.Ownership = "local"
	p.Status = "running"
	cmd := m.openProfileLogs(p)
	if cmd == nil {
		t.Fatal("running target logs did not start")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("log fetch did not start")
	}
	m.logKey("q")
	select {
	case <-b.canceled:
	case <-time.After(time.Second):
		t.Fatal("closing the log viewer did not cancel the pending fetch")
	}
	if msg := <-done; msg == nil {
		t.Fatal("canceled log fetch returned no result")
	}
}
