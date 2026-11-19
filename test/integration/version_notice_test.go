package integration

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/tui"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/versioninfo"
)

type releaseNoticeBackend struct{ catalogErr error }

func (b releaseNoticeBackend) UICatalog(context.Context) ([]catalog.Package, error) {
	return nil, b.catalogErr
}
func (releaseNoticeBackend) UIInventory(context.Context) ([]state.Installation, error) {
	return nil, nil
}
func (releaseNoticeBackend) UIMCPs(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (releaseNoticeBackend) UIAgents(context.Context) ([]string, error)     { return nil, nil }
func (releaseNoticeBackend) UISettings(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (releaseNoticeBackend) UISourceLabels(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (releaseNoticeBackend) UIRun(context.Context, string, string, string, string, string, string, string) (string, error) {
	return "", nil
}

func TestTUIShowsNewerReleaseWithoutReplacingOperationStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.2.0", "assets": []any{}}})
	}))
	defer server.Close()
	t.Setenv("AACT_RELEASE_API_URL", server.URL+"/releases")
	previous := versioninfo.Version
	versioninfo.Version = "1.0.0"
	defer func() { versioninfo.Version = previous }()

	model := tui.NewContext(context.Background(), releaseNoticeBackend{catalogErr: errors.New("catalog fixture failure")})
	loaded := model.Init()()
	updated, check := model.Update(loaded)
	model = updated.(*tui.Model)
	before := model.View().Content
	if !strings.Contains(before, "catalog fixture failure") {
		t.Fatalf("fixture operation status missing before update lookup: %q", before)
	}
	if check == nil {
		t.Fatal("initial catalog load did not schedule a release lookup")
	}
	updated, _ = model.Update(check())
	view := updated.(*tui.Model).View().Content
	if !strings.Contains(view, "AACT 1.2.0 is available") {
		t.Fatalf("new version notice missing: %q", view)
	}
	if !strings.Contains(view, "catalog fixture failure") {
		t.Fatalf("release notice replaced operation status: %q", view)
	}
}

func TestTUINewerReleaseNoticeAndFooterFitSixteenRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"tag_name": "v1.2.0", "assets": []any{}}})
	}))
	defer server.Close()
	t.Setenv("AACT_RELEASE_API_URL", server.URL+"/releases")
	previous := versioninfo.Version
	versioninfo.Version = "1.0.0"
	defer func() { versioninfo.Version = previous }()

	model := tui.NewContext(context.Background(), releaseNoticeBackend{})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	loaded := model.Init()()
	updated, check := model.Update(loaded)
	model = updated.(*tui.Model)
	if check == nil {
		t.Fatal("initial catalog load did not schedule the release check")
	}
	updated, _ = model.Update(check())
	view := updated.(*tui.Model).View().Content
	if !strings.Contains(view, "AACT 1.2.0 is available") {
		t.Fatalf("successful release response did not render the update notice: %q", view)
	}
	if !strings.Contains(view, "F10/q Quit") {
		t.Fatalf("footer was clipped by the update notice: %q", view)
	}
	if rows := len(strings.Split(view, "\n")); rows > 16 {
		t.Fatalf("home view rendered %d rows into a 16-row terminal: %q", rows, view)
	}
}

func TestTUIStartupCheckIsOfflineTolerantAndKeepsHomeFooterVisible(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-finish
		http.Error(w, "offline fixture", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	}()
	t.Setenv("AACT_RELEASE_API_URL", server.URL+"/releases")
	previous := versioninfo.Version
	versioninfo.Version = "1.0.0"
	defer func() { versioninfo.Version = previous }()
	model := tui.NewContext(context.Background(), releaseNoticeBackend{})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	loaded := model.Init()()
	start := time.Now()
	updated, check := model.Update(loaded)
	model = updated.(*tui.Model)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("initial UI update blocked on release discovery")
	}
	if check == nil {
		t.Fatal("initial catalog load did not schedule the release check")
	}
	before := model.View().Content
	if !strings.Contains(before, "F10/q Quit") {
		t.Fatalf("home footer missing before release check: %q", before)
	}
	if len(strings.Split(before, "\n")) > 16 {
		t.Fatalf("home screen exceeded terminal height before release check: %d lines", len(strings.Split(before, "\n")))
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- check() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("release check did not start")
	}
	if view := model.View().Content; !strings.Contains(view, "F10/q Quit") {
		t.Fatalf("UI unavailable while release lookup is pending: %q", view)
	}
	close(finish)
	select {
	case message := <-done:
		updated, _ = model.Update(message)
	case <-time.After(time.Second):
		t.Fatal("offline release lookup did not finish")
	}
	view := updated.(*tui.Model).View().Content
	if !strings.Contains(view, "F10/q Quit") {
		t.Fatalf("home footer clipped after release check: %q", view)
	}
	if len(strings.Split(view, "\n")) > 16 {
		t.Fatalf("home screen exceeded terminal height after release check: %d lines", len(strings.Split(view, "\n")))
	}
}
