package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type isolatedClusterRuntime struct {
	starts int
	last   mcp.RunSpec
}

func (r *isolatedClusterRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	r.last = spec
	return mcp.Instance{Key: key, Status: "running", Ownership: "local", URL: "http://127.0.0.1:18766/mcp"}, nil
}
func (*isolatedClusterRuntime) Stop(context.Context, state.Key) error        { return nil }
func (*isolatedClusterRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*isolatedClusterRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

// This opt-in integration test follows the real source manifest and target-a
// target read-only. Its state, HOME, agent config, and docker command are all
// isolated under t.TempDir; the docker shim exercises the packaged native
// helper without talking to a daemon or cluster.
func TestRealClusterInspectorTargetWorkspaceSaveApplyBuildsAndStarts(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("the checked-in preview helper is the native macOS arm64 artifact")
	}
	sourceRoot := os.Getenv("AACT_INTEGRATION_SOURCE_ROOT")
	if sourceRoot == "" {
		t.Skip("set AACT_INTEGRATION_SOURCE_ROOT to the read-only personal source checkout")
	}
	manifest := filepath.Join(sourceRoot, "aact.toml")
	if _, err := os.Stat(filepath.Join(sourceRoot, "environments", "home", "cluster-inspector", "target-a.toml")); err != nil {
		t.Skip("the supplied target-a target is not present")
	}
	_, testFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	stateRoot := t.TempDir()
	previewRoot := os.Getenv("AACT_BUNDLED_ROOT")
	if previewRoot == "" {
		previewRoot = filepath.Join(repoRoot, "dist", "preview", "packages")
	}
	source, err := config.Discover(sourceRoot, manifest, previewRoot, stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(stateRoot, "state"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(stateRoot, "home")
	configHome := filepath.Join(stateRoot, "config")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	shimDir := filepath.Join(stateRoot, "bin")
	if err := os.MkdirAll(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	dockerLog := filepath.Join(stateRoot, "docker-args.log")
	t.Setenv("AACT_FAKE_DOCKER_LOG", dockerLog)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dockerShim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AACT_FAKE_DOCKER_LOG\"\ncase \"$1\" in\nbuild) while [ $# -gt 0 ]; do if [ \"$1\" = --iidfile ]; then shift; printf 'sha256:%064d\\n' 0 > \"$1\"; break; fi; shift; done ;;\nrun) cat >/dev/null; printf '{\"auth_required\":false}' ;;\n*) exit 9 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(shimDir, "docker"), []byte(dockerShim), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	runtimeStub := &isolatedClusterRuntime{}
	svc := app.New(source, store, app.Options{Runtime: runtimeStub})
	if err := svc.UISetDefaultAgents(t.Context(), []string{"opencode"}); err != nil {
		t.Fatal(err)
	}
	m := NewContext(t.Context(), svc)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m.Update(m.Init()())
	key := state.Key{Source: source.ID, Package: "cluster-inspector", Environment: "sample-env", Target: "target-a"}
	request := viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}
	cmd := m.openTargetWorkspace(request, "Overview")
	if cmd == nil {
		t.Fatal("actual source did not open the target workspace")
	}
	m.Update(cmd())
	if m.pendingSetup == nil || m.pendingSetup.Key != key || m.pendingSetup.TargetPath != filepath.Join(sourceRoot, "environments", "home", "cluster-inspector", "target-a.toml") {
		t.Fatalf("workspace did not load the exact read-only target-a target: %+v", m.pendingSetup)
	}
	if m.workspace.Profile != nil {
		t.Fatalf("fresh environment target unexpectedly acquired runtime ownership: %+v", m.workspace.Profile)
	}
	if !strings.Contains(ansi.Strip(m.form.View().Content), "build, start, register") {
		t.Fatalf("fresh real target has no visible build/start action:\n%s", ansi.Strip(m.form.View().Content))
	}
	_, cmd = m.Update(forms.ActionMsg{Section: "Overview", ID: "apply"})
	if cmd == nil {
		t.Fatal("visible build/start action did not submit the unified Save and Apply operation")
	}
	completion := runTeaCmd(t, m, cmd)
	m.Update(completion)
	dockerArgs, err := os.ReadFile(dockerLog)
	dockerLogText := string(dockerArgs)
	if err != nil {
		dockerLogText = "<unavailable: " + err.Error() + ">"
	}
	expectedImage := fmt.Sprintf("sha256:%064d", 0)
	if runtimeStub.starts != 1 || runtimeStub.last.Image != expectedImage {
		t.Fatalf("actual UI path did not start the immutable image reported by Docker's iidfile: starts=%d image=%q want=%q completion=%#v output=%q result=%+v docker=%q", runtimeStub.starts, runtimeStub.last.Image, expectedImage, completion, m.output, m.result, dockerLogText)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerArgs), "build --progress=plain --iidfile ") || strings.Contains(string(dockerArgs), "build --quiet") || !strings.Contains(string(dockerArgs), filepath.Join(previewRoot, "cluster-inspector", "mcp")) || !strings.Contains(string(dockerArgs), "run --rm --interactive") {
		t.Fatalf("real native helper did not build and prepare the selected package: %s", fmt.Sprintf("%q", dockerArgs))
	}
	rows, err := store.Installations()
	if err != nil || len(rows) == 0 {
		t.Fatalf("isolated agent registration was not recorded: rows=%d err=%v", len(rows), err)
	}
	for _, row := range rows {
		if row.AgentID != "opencode" || row.Component != "mcp" {
			continue
		}
		relative, relErr := filepath.Rel(stateRoot, row.Destination)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			t.Fatalf("registration destination escaped temporary test state: %s", row.Destination)
		}
		if _, err := os.Stat(row.Destination); err != nil {
			t.Fatalf("temporary OpenCode MCP config was not written at %s: %v", row.Destination, err)
		}
		return
	}
	t.Fatal("the isolated operation did not record an OpenCode MCP registration")
}
