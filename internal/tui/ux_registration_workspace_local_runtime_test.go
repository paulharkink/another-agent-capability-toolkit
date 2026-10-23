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
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/install"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type emptyDockerPS struct {
	calls     [][]string
	listedIDs string
	inspect   []byte
}

func TestUXInputsSaveVisibleAndUsesSaveOnlyRouteAtMinimumSize(t *testing.T) {
	for _, useMouse := range []bool{false, true} {
		name := "keyboard"
		if useMouse {
			name = "mouse"
		}
		t.Run(name, func(t *testing.T) {
			m, backend := openSetupInteraction(t)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
			if !m.form.SelectSectionID(sectionInputsID) {
				t.Fatal("workspace did not expose Inputs")
			}
			m.form.FocusSection()
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "[ Save ]") || !strings.Contains(view, "[ Cancel ]") {
				t.Fatalf("minimum-size Inputs screen does not show both primary controls:\n%s", view)
			}
			var cmd tea.Cmd
			if useMouse {
				ox, oy, _, _, ok := m.setupOverlayBounds()
				if !ok {
					t.Fatal("workspace overlay bounds unavailable")
				}
				found := false
				for row, line := range strings.Split(ansi.Strip(m.form.View().Content), "\n") {
					col := strings.Index(line, "[ Save ]")
					if col < 0 {
						continue
					}
					_, cmd = m.Update(tea.MouseClickMsg{X: ox + col + 3, Y: oy + row, Button: tea.MouseLeft})
					found = true
					break
				}
				if !found {
					t.Fatal("Save is not present inside the rendered form for mouse activation")
				}
			} else {
				_, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			}
			if cmd == nil {
				t.Fatal("Save did not start input persistence")
			}
			m.Update(cmd())
			if backend.savedInputRequest == nil || backend.installRequest != nil {
				t.Fatalf("Save must persist inputs only: saved=%+v install=%+v", backend.savedInputRequest, backend.installRequest)
			}
		})
	}
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

func (b *localInstallCapture) UIRun(context.Context, string, string, string, string, string, string, string) (string, error) {
	b.uiRunCalls++
	return "", errors.New("legacy UIRun must not be called for a workspace action")
}

// TestUXSavedLocalhostRegistrationDoesNotBlockLocalWorkspaceApply drives the
// real profile preview and workspace with the checked-in Cluster Inspector
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
	key := state.Key{Source: "cluster-source", Package: pkg.ID, Target: "target-a"}
	if err := store.RecordProfile(state.ProfileRecord{Key: key, Name: pkg.Name}); err != nil {
		t.Fatal(err)
	}
	const endpoint = "http://127.0.0.1:18766/mcp"
	if err := store.Record(state.Installation{
		Key: key, AgentID: "opencode", AgentKind: "opencode", Component: "mcp",
		Mode: "registration", Destination: filepath.Join(root, "opencode.json"), RegistrationName: "cluster-inspector-target-a", URL: endpoint,
		Transport: pkg.MCP.Transport,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), []byte(`{"mcp":{"cluster-inspector-target-a":{"url":"`+endpoint+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// The selected installation item links both the skill and MCP. Seed the
	// skill with the real isolated installer so observedSelectionComplete sees
	// the same complete target that Agent Access presents as installed.
	generatedSkill := filepath.Join(root, "generated-skill")
	if err := os.MkdirAll(generatedSkill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generatedSkill, "SKILL.md"), []byte("# Cluster Inspector\n"), 0600); err != nil {
		t.Fatal(err)
	}

	profileRoot := filepath.Join(root, "profiles")
	source := config.Source{
		ID: "cluster-source", Root: repoRoot, ManifestPath: filepath.Join(repoRoot, "aact.toml"),
		ProfileRoot: profileRoot, Catalog: []catalog.Package{pkg},
		PackageDefaults: map[string]map[string]any{pkg.ID: {"registration_name": "cluster-inspector-target-a", "api_server": "https://initial.example.test"}},
	}
	probe, err := agents.DefaultDiscoveryProbe()
	if err != nil {
		t.Fatal(err)
	}
	probe.LookPath = func(name string) (string, error) {
		if name == "opencode" || name == "codex" {
			return filepath.Join(home, "bin", name), nil
		}
		return "", os.ErrNotExist
	}
	service := app.New(source, store, app.Options{DiscoveryProbe: &probe})
	if err := install.NewSkills(store).Install(t.Context(), pkg, install.SkillDestination{ID: "opencode", Kind: "opencode", Home: home, SkillsDir: filepath.Join(root, "config", "opencode", "skills")}, key, generatedSkill); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProfile(t.Context(), config.InstanceRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordInstance(state.InstanceRecord{Key: key, Name: pkg.Name, Local: true, Selection: &state.InstanceSelection{DestinationIDs: []string{"opencode"}}}); err != nil {
		t.Fatal(err)
	}
	previewCheck, err := service.PreviewInstance(t.Context(), app.InstanceRequest{Ref: config.InstanceRef{PackID: key.Source, CapabilityID: key.Package, Name: key.Target}})
	if err != nil {
		t.Fatal(err)
	}
	if previewCheck.Key != key {
		t.Fatalf("preview resolved a different instance key: seeded=%+v preview=%+v", key, previewCheck.Key)
	}
	currentRows, err := store.Instances()
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range previewCheck.Destinations {
		if destination.ID == "opencode" && !destination.Selected {
			t.Fatalf("persisted selection not reflected by direct preview: key=%+v records=%d selection=%+v destinations=%+v", key, len(currentRows), currentRows[0].Selection, previewCheck.Destinations)
		}
	}
	seeded, err := store.Instances()
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) != 1 || seeded[0].Key != key || seeded[0].Selection == nil || !containsString(seeded[0].Selection.DestinationIDs, "opencode") {
		t.Fatalf("could not seed exact persisted selection: key=%+v rows=%+v", key, seeded)
	}
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

	request := m.packInstanceRequest(key)
	cmd := m.openTargetWorkspace(request, "Overview")
	if cmd == nil {
		t.Fatal("actual package target did not open a setup workspace")
	}
	m.Update(cmd())
	m.form.FocusSection()
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.workspace == nil || m.workspace.Instance == nil {
		t.Fatalf("persisted registration did not produce the target profile: %+v", m.workspace)
	}
	if m.form == nil {
		t.Fatalf("workspace preview did not produce the Inputs stage: output=%q result=%+v", m.output, m.result)
	}
	profile := m.workspace.Instance
	if profile.URL != endpoint || profile.Ownership != "local" || profile.RuntimeStatus != "never-started" || !profile.CanStart {
		t.Fatalf("saved localhost registration was mistaken for an external runtime: %+v", profile)
	}
	if got := m.form.Values()["host"]; got != "127.0.0.1" {
		t.Fatalf("unexpected initial host draft: %v", got)
	}
	selectedBeforeSave, _ := m.form.Values()["__aact_destinations"].([]string)
	if !containsString(selectedBeforeSave, "opencode") {
		t.Fatalf("persisted OpenCode registration was not selected before Inputs Save: %v; destinations=%+v", selectedBeforeSave, m.pendingSetup.Destinations)
	}
	if view := m.form.View().Content; !strings.Contains(view, "Inputs") || !strings.Contains(view, "[ Save ]") || !strings.Contains(view, "Cancel") || !strings.Contains(view, "Runtime") {
		t.Fatalf("local Docker target does not expose the Inputs Save/Cancel stage and separate Runtime stage:\n%s", view)
	}
	_, saveCmd := m.Update(forms.ActionMsg{SectionID: sectionInputsID, ID: "save-inputs"})
	if saveCmd == nil {
		t.Fatalf("Inputs Save did not dispatch save-only operation: values=%#v view=%s", m.form.Values(), m.form.View().Content)
	}
	m.Update(saveCmd())
	if !m.workspace.InputsSaved || backend.installCalls != 0 {
		t.Fatalf("Inputs Save did not persist inputs separately: saved=%t installs=%d", m.workspace.InputsSaved, backend.installCalls)
	}
	selected, _ := m.form.Values()["__aact_destinations"].([]string)
	if !containsString(selected, "opencode") {
		t.Fatalf("persisted OpenCode registration was not selected in Agent access: %v", selected)
	}
	m.form.SelectSectionID(sectionAgentsID)
	m.form.FocusSection()
	_, installCmd := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if installCmd == nil {
		t.Fatalf("Agent access Install selected did not dispatch UIInstall: values=%#v view=%s", m.form.Values(), m.form.View().Content)
	}
	m.Update(runTeaCmd(t, m, installCmd))
	if backend.installRequest == nil {
		t.Fatal("Agent access Install selected did not call UIInstall")
	}
	if backend.uiRunCalls != 0 {
		t.Fatalf("Agent access Install selected fell back to legacy UIRun %d times", backend.uiRunCalls)
	}
	got := backend.installRequest
	if got.SetupRequest != request || got.ExternalURL != "" || !containsString(got.DestinationIDs, "opencode") {
		t.Fatalf("Agent access install did not submit the selected local registration target: %+v; form=%#v", *got, m.form.Values())
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
	foreignModel.form.FocusSection()
	foreignModel.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	foreignProfile := foreignModel.workspace.Instance
	if foreignProfile == nil || foreignProfile.Ownership != "other-aact" || foreignProfile.RuntimeStatus != "running" || foreignProfile.CanStart {
		t.Fatalf("observed foreign runtime was not guarded despite its loopback registration: %+v", foreignProfile)
	}
	foreignView := foreignModel.form.View().Content
	if !strings.Contains(foreignView, "Inputs") || !strings.Contains(foreignView, "[ Save ]") || !strings.Contains(foreignView, "Cancel") || strings.Contains(foreignView, "Build and start MCP") || strings.Contains(foreignView, "[ Start ]") {
		t.Fatalf("foreign runtime lost the separate Inputs Save stage or exposed a lifecycle shortcut:\n%s", foreignView)
	}
	foreignRow := ProfileRow{Key: foreignProfile.Key, Instance: foreignProfile, URL: foreignProfile.URL, Status: foreignProfile.RuntimeStatus}
	if reason := foreignModel.instanceActionReason(foreignRow, "s"); reason == "" {
		t.Fatal("foreign runtime lifecycle controls were not disabled")
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
