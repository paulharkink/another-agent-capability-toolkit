package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type logProfileBackend struct {
	*profileBackend
	actions []string
	key     state.Key
	logs    string
	err     error
}

func openLogAction(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	profileIndex := m.home.Profiles.Index
	if profileIndex < 0 || profileIndex >= len(m.profiles()) {
		profileIndex = 0
	}
	p := m.profiles()[profileIndex]
	if p.Profile != nil && p.Profile.Ownership == "local" {
		p.Profile.RuntimeStatus = "running"
		p.Status = "running"
		p.Instance.Ownership = "local"
		if m.profileSnapshot != nil {
			for i := range m.profileSnapshot.Profiles {
				if m.profileSnapshot.Profiles[i].Key == p.Key {
					m.profileSnapshot.Profiles[i].RuntimeStatus = "running"
				}
			}
		}
	}
	base, ok := m.backend.(*logProfileBackend)
	if !ok {
		if existing, yes := m.backend.(*profileBackend); yes {
			base = &logProfileBackend{profileBackend: existing}
		} else {
			t.Fatalf("expected profile fixture backend, got %T", m.backend)
		}
	}
	m.backend = &logWorkspaceBackend{logProfileBackend: base, setup: &setupBackendFixture{}}
	m.selectPane(ProfilesPane, profileIndex)
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: p.Key.Source, PackageID: p.Key.Package, Environment: p.Key.Environment, Target: p.Key.Target}, "Overview")
	if cmd == nil {
		t.Fatalf("target workspace did not open for logs target %+v", p.Key)
	}
	m.Update(cmd())
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	return cmd
}

type logWorkspaceBackend struct {
	*logProfileBackend
	setup *setupBackendFixture
}

func (b *logWorkspaceBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	return viewmodel.SetupPreview{Key: state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target}, PackageName: request.PackageID, MCP: true}, nil
}

func (b *logWorkspaceBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setup.UIInstall(ctx, request)
}

func (b *logProfileBackend) UIRun(_ context.Context, action, source, packageID, _, environment, target string) (string, error) {
	b.actions = append(b.actions, action)
	b.key = state.Key{Source: source, Package: packageID, Environment: environment, Target: target}
	return b.logs, b.err
}

func TestOwnedProfileLogsOpenScrollableReadOnlyViewer(t *testing.T) {
	m, base := typedProfileFixture()
	base.snapshot.Profiles[0].RuntimeStatus = "running"
	base.snapshot.Profiles[0].CanStop = true
	m.Update(m.load()())
	b := &logProfileBackend{profileBackend: base}
	for i := 1; i <= 40; i++ {
		b.logs += fmt.Sprintf("line-%02d\n", i)
	}
	m.backend = b
	m.focusPane(ProfilesPane)
	cmd := openLogAction(t, m)
	if cmd == nil {
		t.Fatal("owned profile logs did not load")
	}
	m.Update(cmd())
	if len(b.actions) != 1 || b.actions[0] != "logs" || b.key != base.snapshot.Profiles[0].Key {
		t.Fatalf("logs were not fetched for selected profile: actions=%v key=%+v", b.actions, b.key)
	}
	if m.home.Modal == nil || m.home.Modal.Kind != "logs" || !strings.Contains(m.View().Content, "line-40") {
		t.Fatalf("log viewer did not open: %s", m.View().Content)
	}
	press(m, tea.KeyEnd, "")
	if !strings.Contains(m.View().Content, "line-40") {
		t.Fatal("End did not scroll to the last log line")
	}
	press(m, tea.KeyHome, "")
	m.View()
	m.Update(tea.MouseWheelMsg{X: m.width / 2, Y: m.height / 2, Button: tea.MouseWheelDown})
	if !strings.Contains(m.View().Content, "line-02") {
		t.Fatal("mouse wheel did not scroll log lines")
	}
	press(m, tea.KeyEscape, "")
	if m.home.Modal != nil || m.workspace == nil || !m.workspace.Active || m.form == nil || m.workspace.Section != "Logs" || len(b.actions) != 1 {
		t.Fatalf("closing logs did not retain the target workspace or changed the server: modal=%v workspace=%+v form=%v actions=%v", m.home.Modal, m.workspace, m.form != nil, b.actions)
	}
}

func TestForeignProfileCannotFetchLogs(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base, logs: "foreign logs"}
	m.backend = b
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	cmd := openLogAction(t, m)
	if cmd != nil || len(b.actions) != 0 || !strings.Contains(m.output, "only for locally owned runtimes") {
		t.Fatalf("foreign profile exposed local logs: cmd=%v actions=%v output=%q", cmd, b.actions, m.output)
	}
}

func TestLogFollowRefreshesAndPauseStopsPolling(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base, logs: "first\n"}
	m.backend = b
	m.focusPane(ProfilesPane)
	cmd := openLogAction(t, m)
	if cmd == nil {
		t.Fatal("log panel did not open")
	}
	m.Update(cmd())
	if m.home.Modal == nil || !m.home.Modal.Follow {
		t.Fatal("log panel did not start following")
	}
	b.logs = "first\nsecond\n"
	_, poll := m.Update(logPollMsg{session: m.logSession})
	if poll == nil {
		t.Fatal("follow tick did not fetch logs")
	}
	m.Update(poll())
	if !strings.Contains(m.View().Content, "second") || len(b.actions) != 2 {
		t.Fatalf("follow refresh lost new lines: %s actions=%v", m.View().Content, b.actions)
	}
	press(m, 'f', "f")
	if m.home.Modal == nil || m.home.Modal.Follow {
		t.Fatal("f did not pause log following")
	}
	_, poll = m.Update(logPollMsg{session: m.logSession})
	if poll != nil || len(b.actions) != 2 {
		t.Fatalf("paused logs still polled: actions=%v", b.actions)
	}
}

func TestLateLogRefreshCannotReopenClosedPanel(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base, logs: "first\n"}
	m.backend = b
	m.focusPane(ProfilesPane)
	cmd := openLogAction(t, m)
	if cmd == nil {
		t.Fatal("log panel did not open")
	}
	m.Update(cmd())
	_, poll := m.Update(logPollMsg{session: m.logSession})
	if poll == nil {
		t.Fatal("follow tick did not start fetch")
	}
	press(m, tea.KeyEscape, "")
	m.Update(poll())
	if m.home.Modal != nil {
		t.Fatalf("late log result reopened closed panel: %+v", m.home.Modal)
	}
}

func TestLogRefreshFailureShowsCauseAndPauses(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base, logs: "first\n"}
	m.backend = b
	m.focusPane(ProfilesPane)
	cmd := openLogAction(t, m)
	if cmd == nil {
		t.Fatal("log panel did not open")
	}
	m.Update(cmd())
	b.err = errors.New("Docker daemon connection refused")
	_, poll := m.Update(logPollMsg{session: m.logSession})
	m.Update(poll())
	if m.result == nil || !m.result.Failed || !strings.Contains(strings.Join(m.result.Rows, "\n"), "Docker daemon connection refused") || !strings.Contains(m.View().Content, "Docker daemon connection refused") {
		t.Fatalf("refresh failure was not surfaced as a structured foreground result: %s", m.View().Content)
	}
}

func TestFollowingLogsStartsAtTailAndScrollingPauses(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base}
	for i := 1; i <= 40; i++ {
		b.logs += fmt.Sprintf("line-%02d\n", i)
	}
	m.backend = b
	m.focusPane(ProfilesPane)
	cmd := openLogAction(t, m)
	if cmd == nil {
		t.Fatal("log panel did not open")
	}
	m.Update(cmd())
	if !strings.Contains(m.View().Content, "line-40") || m.home.Modal == nil || !m.home.Modal.Follow {
		t.Fatalf("follow mode did not start at tail: %s", m.View().Content)
	}
	press(m, tea.KeyHome, "")
	if m.home.Modal.Follow || !strings.Contains(m.View().Content, "line-01") {
		t.Fatalf("scrolling did not pause follow: %s", m.View().Content)
	}
}

func TestLogFollowControlRespondsToMouse(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base, logs: "line\n"}
	m.backend = b
	m.focusPane(ProfilesPane)
	cmd := openLogAction(t, m)
	if cmd == nil {
		t.Fatal("log panel did not open")
	}
	m.Update(cmd())
	m.View()
	var hit *hitRegion
	for i := range m.home.Hits {
		if m.home.Hits[i].Control == "log-follow" {
			hit = &m.home.Hits[i]
			break
		}
	}
	if hit == nil {
		t.Fatal("Follow/Pause control lacks a mouse target")
	}
	m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
	if m.home.Modal == nil || m.home.Modal.Follow {
		t.Fatal("mouse click did not pause log following")
	}
}

func TestWorkspaceStartAppliesCurrentDraftAndStopUsesRuntimeAction(t *testing.T) {
	for _, tc := range []struct{ shortcut, want string }{{"s", "start"}, {"x", "stop"}} {
		t.Run(tc.want, func(t *testing.T) {
			m, base := typedProfileFixture()
			m.backend = &logProfileBackend{profileBackend: base}
			cmd := openLogAction(t, m)
			_ = cmd
			workspaceBackend := m.backend.(*logWorkspaceBackend)
			logsBackend := workspaceBackend.logProfileBackend
			m.home.Modal = nil
			m.busy = false
			m.workspace.Section = "Overview"
			m.form.SelectSection("Overview")
			m.workspace.Profile.CanStart = tc.shortcut == "s"
			m.workspace.Profile.CanStop = tc.shortcut == "x"
			_, operation := m.workspaceOverviewAction(tc.shortcut)
			if operation == nil {
				t.Fatalf("workspace %s did not submit an operation", tc.shortcut)
			}
			m.Update(runTeaCmd(t, m, operation))
			if tc.shortcut == "s" {
				if workspaceBackend.setup.installRequest == nil {
					t.Fatal("workspace Start did not Save and apply the current setup draft")
				}
				if len(logsBackend.actions) != 0 {
					t.Fatalf("workspace Start bypassed the setup lifecycle: runtime actions=%v", logsBackend.actions)
				}
				return
			}
			if len(logsBackend.actions) != 1 || logsBackend.actions[0] != tc.want {
				t.Fatalf("workspace %s sent service action %v, want %q", tc.shortcut, logsBackend.actions, tc.want)
			}
		})
	}
}

type replacementLogsBackend struct {
	*logProfileBackend
	setup       *setupBackendFixture
	mu          sync.Mutex
	calls       int
	started     chan int
	secondReply chan struct{}
}

func (b *replacementLogsBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	return viewmodel.SetupPreview{Key: state.Key{Source: request.SourceID, Package: request.PackageID, Environment: request.Environment, Target: request.Target}, PackageName: request.PackageID}, nil
}

func (b *replacementLogsBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return b.setup.UIInstall(ctx, request)
}

func (b *replacementLogsBackend) UIProfileLogs(ctx context.Context, _ state.Key) (string, error) {
	b.mu.Lock()
	b.calls++
	call := b.calls
	b.mu.Unlock()
	b.started <- call
	if call == 1 {
		<-ctx.Done()
		return "", ctx.Err()
	}
	select {
	case <-b.secondReply:
		return "fresh replacement output", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestPauseResumeReplacementIgnoresIntentionalCancellation(t *testing.T) {
	m, base := typedProfileFixture()
	base.snapshot.Profiles[0].RuntimeStatus = "running"
	base.snapshot.Profiles[0].Ownership = "local"
	m.Update(m.load()())
	backend := &replacementLogsBackend{
		logProfileBackend: &logProfileBackend{profileBackend: base},
		setup:             &setupBackendFixture{},
		started:           make(chan int, 2),
		secondReply:       make(chan struct{}),
	}
	m.backend = backend
	key := base.snapshot.Profiles[0].Key
	m.selectPane(ProfilesPane, 0)
	setup := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Overview")
	m.Update(setup())
	_, firstFetch := m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if firstFetch == nil {
		t.Fatal("workspace Logs route did not start the first fetch")
	}
	firstResult := make(chan tea.Msg, 1)
	go func() { firstResult <- firstFetch() }()
	if call := <-backend.started; call != 1 {
		t.Fatalf("first fetch call = %d, want 1", call)
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})            // Pause.
	_, secondFetch := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}) // Resume and replace.
	if secondFetch == nil {
		t.Fatal("resuming Follow did not start a replacement fetch")
	}
	secondResult := make(chan tea.Msg, 1)
	go func() { secondResult <- secondFetch() }()
	if call := <-backend.started; call != 2 {
		t.Fatalf("replacement fetch call = %d, want 2", call)
	}
	m.Update(<-firstResult)
	if m.result != nil || m.home.Modal == nil || m.home.Modal.Kind != "logs" {
		t.Fatalf("intentional cancellation replaced the active viewer with an error: result=%+v modal=%+v", m.result, m.home.Modal)
	}
	close(backend.secondReply)
	m.Update(<-secondResult)
	if m.result != nil || m.home.Modal == nil || !strings.Contains(strings.Join(m.home.Modal.Rows, "\n"), "fresh replacement output") {
		t.Fatalf("replacement response was not retained: result=%+v modal=%+v", m.result, m.home.Modal)
	}
	if m.workspace == nil || !m.workspace.Active || m.workspace.Section != "Logs" {
		t.Fatalf("pause/resume changed workspace context: %+v", m.workspace)
	}
}
