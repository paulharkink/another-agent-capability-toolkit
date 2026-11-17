package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/tui"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/versioninfo"
)

type releaseNoticeBackend struct{}

func (releaseNoticeBackend) UICatalog(context.Context) ([]catalog.Package, error) {
	return nil, errors.New("catalog fixture failure")
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

	model := tui.NewContext(context.Background(), releaseNoticeBackend{})
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
