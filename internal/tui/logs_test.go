package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
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
	m.home.Focus = CapabilitiesPane
	press(m, tea.KeyEnter, "") // L1 to the contextual profile list.
	profileIndex := m.home.Profiles.Index
	m.selectContext(profileIndex + 2)
	press(m, tea.KeyEnter, "") // Open that profile's actions.
	entries := m.menuEntries()
	for i, entry := range entries {
		if strings.HasPrefix(entry, "View logs") {
			m.home.Modal.Selected = i
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return cmd
		}
	}
	t.Fatalf("profile action %q absent: %v", "View logs", entries)
	return nil
}

func (b *logProfileBackend) UIRun(_ context.Context, action, source, packageID, profile, _, environment, target string) (string, error) {
	b.actions = append(b.actions, action)
	b.key = state.Key{Source: source, Package: packageID, Profile: profile, Environment: environment, Target: target}
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
	if m.home.Modal != nil || m.home.Focus != ProfilesPane || len(b.actions) != 1 {
		t.Fatalf("closing logs changed server or selection: modal=%v actions=%v", m.home.Modal, b.actions)
	}
}

func TestForeignProfileCannotFetchLogs(t *testing.T) {
	m, base := typedProfileFixture()
	b := &logProfileBackend{profileBackend: base, logs: "foreign logs"}
	m.backend = b
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	cmd := openLogAction(t, m)
	if cmd != nil || len(b.actions) != 0 || !strings.Contains(m.output, "not locally owned") {
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
	if m.home.Modal == nil || m.home.Modal.Follow || len(m.home.Modal.Rows) == 0 || !strings.Contains(m.home.Modal.Rows[0], "Docker daemon connection refused") || !strings.Contains(m.View().Content, "Docker daemon connection refused") {
		t.Fatalf("refresh failure hidden or follow continued: %s", m.View().Content)
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
