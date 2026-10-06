package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

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
		t.Fatalf("missing actionable empty state: %+v profile=%+v output=%q", m.home.Modal, p.Profile, m.output)
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
	p.Profile.Ownership = "local"
	p.Profile.RuntimeStatus = "running"
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
