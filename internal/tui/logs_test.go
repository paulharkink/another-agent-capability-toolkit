package tui

import (
	"context"
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
}

func (b *logProfileBackend) UIRun(_ context.Context, action, source, packageID, _, environment, target string) (string, error) {
	b.actions = append(b.actions, action)
	b.key = state.Key{Source: source, Package: packageID, Environment: environment, Target: target}
	return b.logs, nil
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
	cmd := openProfileAction(t, m, 7, false)
	if cmd == nil {
		t.Fatal("owned profile logs did not load")
	}
	m.Update(cmd())
	if len(b.actions) != 1 || b.actions[0] != "logs" || b.key != base.snapshot.Profiles[0].Key {
		t.Fatalf("logs were not fetched for selected profile: actions=%v key=%+v", b.actions, b.key)
	}
	if m.home.Modal == nil || m.home.Modal.Kind != "logs" || !strings.Contains(m.View().Content, "line-01") {
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
	cmd := openProfileAction(t, m, 7, false)
	if cmd != nil || len(b.actions) != 0 || !strings.Contains(m.output, "not locally owned") {
		t.Fatalf("foreign profile exposed local logs: cmd=%v actions=%v output=%q", cmd, b.actions, m.output)
	}
}
