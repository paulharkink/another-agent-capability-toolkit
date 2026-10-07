package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type emptyDockerPS struct {
	calls     [][]string
	listedIDs string
	inspect   []byte
}

func (e *emptyDockerPS) Run(_ context.Context, args []string, _ string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	e.calls = append(e.calls, append([]string(nil), args...))
	if len(args) < 2 || args[0] != "docker" {
		return nil, fmt.Errorf("unexpected command in read-only Docker observation: %q", args)
	}
	switch args[1] {
	case "ps":
		return []byte(e.listedIDs), nil
	case "inspect":
		return append([]byte(nil), e.inspect...), nil
	default:
		return nil, fmt.Errorf("unexpected Docker operation in read-only observation: %q", args)
	}
}

type localInstallCapture struct {
	*app.Service
	installRequest *viewmodel.SetupInstallRequest
	installCalls   int
	uiRunCalls     int
}

func (b *localInstallCapture) UIInstall(_ context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.installCalls++
	copy := request
	copy.Inputs = cloneSetupValues(request.Inputs)
	copy.DestinationIDs = append([]string(nil), request.DestinationIDs...)
	b.installRequest = &copy
	return viewmodel.OperationResult{Saved: true, Message: "captured local UIInstall"}, nil
}

func (b *localInstallCapture) UIRun(context.Context, string, string, string, string, string, string) (string, error) {
	b.uiRunCalls++
	return "", errors.New("legacy UIRun must not be called for a workspace action")
}

// TestUXSavedLocalhostRegistrationDoesNotBlockLocalWorkspaceApply drives the
// real target preview and workspace with the checked-in Cluster Inspector
// catalog entry. Only Docker observation and UIInstall's final side effect are
// replaced; the persisted state and HOME are isolated under t.TempDir.
func TestUXSavedLocalhostRegistrationDoesNotBlockLocalWorkspaceApply(t *testing.T) {
	_, testFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	pkg, err := catalog.Load(filepath.Join(repoRoot, "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.MCP == nil || pkg.MCP.Runtime != "docker" {
		t.Fatalf("expected the checked-in Cluster Inspector local Docker MCP, got %+v", pkg.MCP)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(root, "state")
	store, err := state.Open(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	key := state.Key{Source: "cluster-source", Package: pkg.ID, Environment: "home", Target: "pms15"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key, Name: pkg.Name}); err != nil {
		t.Fatal(err)
	}
	const endpoint = "http://127.0.0.1:18766/mcp"
	if err := store.Record(state.Installation{
		Key: key, AgentID: "opencode", AgentKind: "opencode", Component: "mcp",
		Mode: "registration", Destination: filepath.Join(root, "opencode.json"), URL: endpoint,
		Transport: pkg.MCP.Transport,
	}); err != nil {
		t.Fatal(err)
	}

	environmentRoot := filepath.Join(root, "environments")
	targetPath := filepath.Join(environmentRoot, "home", pkg.ID, "pms15.toml")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("[cluster]\napi_server = \"https://initial.example.test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := config.Source{
		ID: "cluster-source", Root: repoRoot, ManifestPath: filepath.Join(repoRoot, "aact.toml"),
		EnvironmentRoot: environmentRoot, Catalog: []catalog.Package{pkg},
		PackageDefaults: map[string]map[string]any{},
	}
	service := app.New(source, store, app.Options{})
	docker := &emptyDockerPS{}
	realRuntime := mcp.NewDockerRuntime(store)
	realRuntime.Executor = docker
	service.Options.Runtime = realRuntime
	backend := &localInstallCapture{Service: service}
	m := NewContext(t.Context(), backend)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if cmd := m.Init(); cmd != nil {
		m.Update(cmd())
	}
	if len(docker.calls) == 0 {
		t.Fatal("workspace load did not perform Docker List observation")
	}
	for _, call := range docker.calls {
		if !reflect.DeepEqual(call[:2], []string{"docker", "ps"}) {
			t.Fatalf("expected read-only Docker ps observation, got %q", call)
		}
	}

	request := viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}
	cmd := m.openTargetWorkspace(request, "Overview")
	if cmd == nil {
		t.Fatal("actual package target did not open a setup workspace")
	}
	m.Update(cmd())
	if m.workspace == nil || m.workspace.Profile == nil {
		t.Fatalf("persisted registration did not produce the target profile: %+v", m.workspace)
	}
	profile := m.workspace.Profile
	if profile.URL != endpoint || profile.Ownership != "local" || profile.RuntimeStatus != "never-started" || !profile.CanStart {
		t.Fatalf("saved localhost registration was mistaken for an external runtime: %+v", profile)
	}
	if got := m.form.Values()["host"]; got != "127.0.0.1" {
		t.Fatalf("unexpected initial host draft: %v", got)
	}
	if view := m.form.View().Content; !strings.Contains(view, "build, start, register") || !strings.Contains(view, "Save and apply") {
		t.Fatalf("local Docker target does not expose Save and Apply/build-start:\n%s", view)
	}

	// Edit the live form draft through its normal keyboard path before choosing
	// the Overview action, so the submission must use current unsaved values.
	m.form.SelectSection("Connection")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // host
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	for _, r := range "draft-listen-host" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.form.Values()["host"]; got != "draft-listen-host" {
		t.Fatalf("keyboard edit did not update the unsaved host draft: %v", got)
	}

	m.form.SelectSection("Agents")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) // Codex
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) // OpenCode
	if got := m.form.Values()["__aact_destinations"]; !containsStringFromValue(got, "opencode") {
		t.Fatalf("keyboard selection did not mark the OpenCode destination: %#v", got)
	}

	m.form.SelectSection("Overview")
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, actionCmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if actionCmd == nil {
		t.Fatal("selecting the enabled Overview apply action did not emit an action")
	}
	_, installCmd := m.Update(actionCmd())
	if installCmd == nil {
		t.Fatal("Overview action did not dispatch the local unified UIInstall operation")
	}
	m.Update(installCmd())
	if backend.installRequest == nil {
		t.Fatal("Overview action did not call UIInstall")
	}
	if backend.uiRunCalls != 0 {
		t.Fatalf("Overview action fell back to legacy UIRun %d times", backend.uiRunCalls)
	}
	got := backend.installRequest
	if got.SetupRequest != request || got.ExternalURL != "" || got.Inputs["host"] != "draft-listen-host" || !containsString(got.DestinationIDs, "opencode") {
		t.Fatalf("local Save and Apply did not submit the current draft/registration target: %+v; form=%#v", *got, m.form.Values())
	}

	// A real observed container owned by another installation must remain
	// guarded even though the registration uses a loopback URL.
	docker.listedIDs = "foreign-container\n"
	inspect, err := json.Marshal([]map[string]any{{
		"Id": "foreign-container", "Name": "/foreign-cluster-inspector",
		"Config": map[string]any{"Labels": map[string]string{
			"aact.managed": "1", "aact.key": key.ID(), "aact.source": key.Source,
			"aact.package": key.Package, "aact.environment": key.Environment, "aact.target": key.Target,
			"aact.owner": "another-installation", "aact.url": endpoint,
		}},
		"State": map[string]any{"Running": true, "Status": "running"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	docker.inspect = inspect
	foreignModel := NewContext(t.Context(), backend)
	foreignModel.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	if cmd := foreignModel.Init(); cmd != nil {
		foreignModel.Update(cmd())
	}
	foreignWorkspace := foreignModel.openTargetWorkspace(request, "Overview")
	if foreignWorkspace == nil {
		t.Fatal("observed foreign runtime target did not open its workspace")
	}
	foreignModel.Update(foreignWorkspace())
	foreignProfile := foreignModel.workspace.Profile
	if foreignProfile == nil || foreignProfile.Ownership != "other-aact" || foreignProfile.RuntimeStatus != "running" || foreignProfile.CanStart {
		t.Fatalf("observed foreign runtime was not guarded despite its loopback registration: %+v", foreignProfile)
	}
	foreignView := foreignModel.form.View().Content
	if !strings.Contains(foreignView, "Build and start MCP") || !strings.Contains(foreignView, "another AACT installation") {
		t.Fatalf("foreign runtime lifecycle action did not explain its disabled state:\n%s", foreignView)
	}
	foreignModel.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if backend.installCalls != 1 || !strings.Contains(foreignModel.output, "another AACT installation") {
		t.Fatalf("start shortcut bypassed observed foreign ownership guard: installs=%d output=%q", backend.installCalls, foreignModel.output)
	}
}

func containsStringFromValue(value any, want string) bool {
	values, ok := value.([]string)
	if !ok {
		return false
	}
	for _, item := range values {
		if item == want {
			return true
		}
	}
	return false
}
