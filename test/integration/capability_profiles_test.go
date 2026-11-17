package integration_test

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/tui"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Runtime is injected; all skill/config/state files are real isolated native files.
type integratedRuntime struct {
	store     *state.Store
	instances []mcp.Instance
}

type rotatingRuntime struct {
	store     *state.Store
	instances []mcp.Instance
	starts    int
}

type failingListRuntime struct{ listCalls int }

func (*failingListRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	return mcp.Instance{}, fmt.Errorf("unexpected runtime start")
}
func (*failingListRuntime) Stop(context.Context, state.Key) error { return nil }
func (r *failingListRuntime) List(context.Context) ([]mcp.Instance, error) {
	r.listCalls++
	return nil, fmt.Errorf("Docker inventory unavailable")
}
func (*failingListRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return nil, fmt.Errorf("unexpected runtime logs")
}

func (r *rotatingRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	port := 42000 + r.starts
	instance := mcp.Instance{Key: key, ID: fmt.Sprintf("runtime-%d", r.starts), Name: "reference-api", Status: "running", Ownership: "local", URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", port), ConfiguredHostPort: spec.HostPort}
	r.instances = []mcp.Instance{instance}
	if err := r.store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", URL: instance.URL, Destination: instance.ID, SourcePath: instance.ID, Mode: "docker"}); err != nil {
		return mcp.Instance{}, err
	}
	return instance, nil
}
func (r *rotatingRuntime) Stop(context.Context, state.Key) error { return nil }
func (r *rotatingRuntime) List(context.Context) ([]mcp.Instance, error) {
	return append([]mcp.Instance(nil), r.instances...), nil
}
func (r *rotatingRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("synthetic runtime logs")), nil
}
func (r *rotatingRuntime) StartBrowser(context.Context, state.Key) (string, error) {
	return "http://localhost:49001", nil
}

func (r *integratedRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	i := mcp.Instance{Key: key, ID: key.ID(), Status: "running", Ownership: "local", URL: "http://127.0.0.1:39991/mcp"}
	found := false
	for n, old := range r.instances {
		if old.Key == key {
			r.instances[n] = i
			found = true
		}
	}
	if !found {
		r.instances = append(r.instances, i)
	}
	return i, r.store.Record(state.Installation{Key: key, Component: "runtime", URL: i.URL, Destination: i.ID})
}
func (r *integratedRuntime) Stop(_ context.Context, key state.Key) error {
	for n, i := range r.instances {
		if i.Key == key {
			r.instances[n].Status = "stopped"
		}
	}
	return nil
}
func (r *integratedRuntime) List(context.Context) ([]mcp.Instance, error) { return r.instances, nil }
func (r *integratedRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("native fixture logs")), nil
}

func TestCapabilityProfilesIntegratedImportApplyObserveRemove(t *testing.T) {
	ctx := context.Background()
	sourceRoot, err := filepath.Abs("../../examples/capability-pack")
	if err != nil {
		t.Fatal(err)
	}
	root := copyCapabilityPackTree(t, sourceRoot)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	rt := &integratedRuntime{store: store}
	svc := app.New(pack.LegacySource(), store, app.Options{Runtime: rt, DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	ref := config.ProfileRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}
	before, err := svc.ProfileSnapshot(ctx, ref.CapabilityID)
	if err != nil || len(before.Profiles) != 2 {
		t.Fatalf("discovery: %+v %v", before, err)
	}
	q := app.ProfileRequest{Ref: ref, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference", "skill:review"}, Inputs: map[string]any{"project": "INVALID", "enabled": false}}
	out, err := svc.ApplyProfile(ctx, q)
	if err != nil || len(out.Changes) != 3 {
		t.Fatalf("apply: %+v %v", out, err)
	}
	for _, name := range []string{"guide", "review"} {
		data, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "skills", name, "SKILL.md"))
		if err != nil || !strings.Contains(string(data), "example-project") {
			t.Fatalf("rendered %s: %s %v", name, data, err)
		}
	}
	snapshot, err := svc.ProfileSnapshot(ctx, ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	row := snapshot.Profiles[0]
	if row.ConfigStatus != "configured" || len(row.Components) != 3 || len(row.MCPs) != 1 || row.MCPs[0].Status != "running" {
		t.Fatalf("actual snapshot: %+v", row)
	}
	for _, c := range row.Components {
		if c.Status != "installed" {
			t.Fatalf("not observed: %+v", c)
		}
	}
	// Deselect the independent review skill, keeping linked guide + MCP together.
	q.ItemIDs = []string{"set:reference"}
	if _, err := svc.ApplyProfile(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "review")); !os.IsNotExist(err) {
		t.Fatalf("deselected skill remains: %v", err)
	}
	// Unrelated user config/skills survive removal.
	unrelated := filepath.Join(home, ".config", "opencode", "skills", "other")
	os.MkdirAll(unrelated, 0700)
	os.WriteFile(filepath.Join(unrelated, "SKILL.md"), []byte("untouched"), 0600)
	if _, err := svc.RemoveProfile(ctx, app.ProfileRequest{Ref: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "environments", "company-guidance", "ota.toml")); err != nil {
		t.Fatalf("deleting the instance removed the pack-authored environment TOML: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unrelated, "SKILL.md")); err != nil {
		t.Fatal("unrelated skill removed:", err)
	}
	snapshot, err = svc.ProfileSnapshot(ctx, ref.CapabilityID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profiles[0].MCPs[0].Status != "stopped" {
		t.Fatalf("runtime removal: %+v", snapshot.Profiles[0])
	}
	// Local profiles have editable inputs, unlike these pack-fixed project names.
	q.Ref.Name = "local"
	if err := svc.CreateProfile(ctx, q.Ref); err != nil {
		t.Fatal(err)
	}
	q.Inputs = map[string]any{"project": "BAD VALUE", "enabled": false}
	prior := len(rt.instances)
	if _, err := svc.ApplyProfile(ctx, q); err == nil || !strings.Contains(err.Error(), "BAD VALUE") || len(rt.instances) != prior {
		t.Fatalf("regex acceptance: %v", err)
	}
	q.Inputs = map[string]any{"project": "local-team", "enabled": false}
	q.SkillsOnly = true
	if _, err := svc.ApplyProfile(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pack.ProfileRoot, ref.CapabilityID, "local.toml")); !os.IsNotExist(err) {
		t.Fatal("wrote pack profile")
	}
}

func TestStartingMCPRefreshesExistingAgentRegistrationToActualRuntimeURL(t *testing.T) {
	for _, sourceLineEnding := range []struct {
		name string
		crlf bool
	}{{name: "LF"}, {name: "CRLF", crlf: true}} {
		t.Run(sourceLineEnding.name, func(t *testing.T) {
			testStartingMCPRefreshesExistingAgentRegistrationToActualRuntimeURL(t, sourceLineEnding.crlf)
		})
	}
}

func testStartingMCPRefreshesExistingAgentRegistrationToActualRuntimeURL(t *testing.T, crlf bool) {
	ctx := context.Background()
	root := copyCapabilityPackTree(t, "../../examples/capability-pack")
	packagePath := filepath.Join(root, "vendor", "neutral-tools", "guidance", "package.toml")
	manifest, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	// Normalize the source first so either checkout line ending works. The CRLF
	// subtest models Windows while avoiding a second conversion of existing CRLF.
	manifestText := strings.ReplaceAll(string(manifest), "\r\n", "\n")
	if crlf {
		manifestText = strings.ReplaceAll(manifestText, "\n", "\r\n")
	}
	manifestText = strings.ReplaceAll(manifestText, "\r\n", "\n")
	manifestText = strings.Replace(manifestText, "[mcp]\n", "[[inputs]]\nname = \"local_port\"\nlabel = \"Local port\"\ntype = \"integer\"\nrequired = true\ndefault = 18871\n[mcp]\n", 1)
	manifestText = strings.Replace(manifestText, "container_port = 8080", "container_port = 8080\nhost_port_input = \"local_port\"", 1)
	manifestText = strings.Replace(manifestText, "fields = [\"project\", \"enabled\"]", "fields = [\"project\", \"enabled\", \"local_port\"]", 1)
	if err := os.WriteFile(packagePath, []byte(manifestText), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	otherHome := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	runtime := &rotatingRuntime{store: store}
	svc := app.New(pack.LegacySource(), store, app.Options{Runtime: runtime, DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	ref := config.InstanceRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}
	initial, err := svc.ApplyInstance(ctx, app.InstanceRequest{Ref: ref, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference"}, Inputs: map[string]any{"project": "example-project", "enabled": false, "local_port": 18871}})
	if err != nil {
		t.Fatalf("initial apply: %+v %v", initial, err)
	}
	configPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	readRegistration := func() string {
		t.Helper()
		data, err := os.ReadFile(configPath)
		if err != nil {
			rows, _ := store.Installations()
			t.Fatalf("read agent config: %v; apply=%+v; rows=%+v", err, initial, rows)
		}
		var document struct {
			MCP map[string]struct {
				URL string `json:"url"`
			} `json:"mcp"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		for _, registration := range document.MCP {
			return registration.URL
		}
		return ""
	}
	if got := readRegistration(); got != "http://127.0.0.1:42001/mcp" {
		t.Fatalf("initial registration URL = %q", got)
	}
	otherProbe := probe
	otherProbe.Home = otherHome
	otherSvc := app.New(pack.LegacySource(), store, app.Options{Runtime: runtime, DiscoveryProbe: &otherProbe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: otherHome, ExplicitHome: true}}})
	_, err = otherSvc.ApplyInstance(ctx, app.InstanceRequest{Ref: ref, InstallOnly: true, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference"}, Inputs: map[string]any{"project": "example-project", "enabled": false, "local_port": 18871}})
	if err != nil {
		t.Fatalf("install second isolated agent destination: %v", err)
	}
	otherConfigPath := filepath.Join(otherHome, ".config", "opencode", "opencode.jsonc")
	readOtherRegistration := func() string {
		t.Helper()
		data, err := os.ReadFile(otherConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			MCP map[string]struct {
				URL string `json:"url"`
			} `json:"mcp"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		for _, registration := range document.MCP {
			return registration.URL
		}
		return ""
	}
	if got := readOtherRegistration(); got != "http://127.0.0.1:42001/mcp" {
		t.Fatalf("second destination initial registration URL = %q", got)
	}
	if _, err := otherSvc.RunInstanceMCP(ctx, "start", app.InstanceRequest{Ref: ref}, "reference-api"); err != nil {
		t.Fatalf("restart MCP: %v", err)
	}
	if got := readRegistration(); got != "http://127.0.0.1:42002/mcp" {
		t.Fatalf("registration after restart = %q, want actual runtime endpoint http://127.0.0.1:42002/mcp", got)
	}
	if got := readOtherRegistration(); got != "http://127.0.0.1:42002/mcp" {
		t.Fatalf("second destination registration after restart = %q, want actual runtime endpoint http://127.0.0.1:42002/mcp", got)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	seenDestinations := map[string]bool{}
	for _, row := range rows {
		if row.Component == "mcp" && row.AgentID == "opencode" {
			if row.URL != "http://127.0.0.1:42002/mcp" {
				t.Fatalf("registration ledger URL = %q", row.URL)
			}
			if row.Destination == "" {
				t.Fatalf("registration destination missing: %+v", row)
			}
			seenDestinations[row.Destination] = true
		}
	}
	if len(seenDestinations) != 2 {
		t.Fatalf("refreshed ledger destinations = %v, want both isolated agent homes", seenDestinations)
	}
}

func TestInstallOnlyExternalMCPRegistrationDoesNotRequireRuntimeInventory(t *testing.T) {
	ctx := context.Background()
	root := copyCapabilityPackTree(t, "../../examples/capability-pack")
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	runtime := &failingListRuntime{}
	svc := app.New(pack.LegacySource(), store, app.Options{Runtime: runtime, DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	ref := config.InstanceRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}
	_, err = svc.ApplyInstance(ctx, app.InstanceRequest{Ref: ref, InstallOnly: true, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference"}, Inputs: map[string]any{"project": "example-project", "enabled": false}, ExternalURLs: map[string]string{"reference-api": "https://mcp.example.test/mcp"}})
	if err != nil {
		t.Fatalf("install external MCP registration: %v", err)
	}
	if runtime.listCalls != 0 {
		t.Fatalf("runtime inventory was called %d times for an external endpoint", runtime.listCalls)
	}
	rows, err := store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Component == "mcp" {
			if row.URL != "https://mcp.example.test/mcp" || !row.ExternalRegistration {
				t.Fatalf("external registration was not preserved: %+v", row)
			}
			return
		}
	}
	t.Fatal("external MCP registration was not recorded")
}

func TestTUIOffersGenericBrowserLoginAndShowsViewerURL(t *testing.T) {
	root := copyCapabilityPackTree(t, "../../examples/capability-pack")
	packagePath := filepath.Join(root, "vendor", "neutral-tools", "guidance", "package.toml")
	manifest, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	manifestText := strings.Replace(string(manifest), "[[ui.sections]]", "[[inputs]]\nname = \"browser_volume\"\nlabel = \"Browser profile volume\"\ntype = \"string\"\nrequired = true\ndefault = \"aact-login-fixture\"\n[[ui.sections]]", 1)
	manifestText = strings.Replace(manifestText, "fields = [\"project\", \"enabled\"]", "fields = [\"project\", \"enabled\", \"browser_volume\"]", 1)
	manifestText = strings.Replace(manifestText, "endpoint_path = \"/mcp\"", "endpoint_path = \"/mcp\"\n[mcp.browser_controller]\nprofile_volume_input = \"browser_volume\"\nstartup_url = \"https://example.test\"", 1)
	manifestText += "\n[mcp.actions.authenticate]\ncommand = [\"sh\", \"-c\", \"printf '{}'\"]\n"
	if err := os.WriteFile(packagePath, []byte(manifestText), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	runtime := &rotatingRuntime{store: store, instances: []mcp.Instance{{Key: state.Key{Source: pack.ID, Package: "company-guidance", Target: "ota"}, ID: "synthetic-runtime", Name: "reference-api", Status: "running", Ownership: "local", URL: "http://127.0.0.1:18871/mcp"}}}
	svc := app.New(pack.LegacySource(), store, app.Options{Runtime: runtime, DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	if _, err := svc.ApplyInstance(context.Background(), app.InstanceRequest{Ref: config.InstanceRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference"}, Inputs: map[string]any{"project": "example-project", "enabled": false}}); err != nil {
		t.Fatalf("prepare an installed running instance for the TUI login action: %v", err)
	}
	model := tui.New(svc)
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("TUI did not load its initial catalog")
	}
	model, _ = model.Update(cmd())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("opening the selected Capability Instance did not request its workspace")
	}
	model, _ = model.Update(cmd())
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "Log in to reference-api") || !strings.Contains(view, "Authenticate reference-api") {
		t.Fatalf("TUI did not show both generic browser login and pack authentication actions:\n%s", view)
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("activating the visible login action did not produce a UI event:\n%s", ansi.Strip(model.View().Content))
	}
	model, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("the login UI event did not invoke the backend")
	}
	model, _ = model.Update(cmd())
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "http://localhost:49001") {
		t.Fatalf("TUI did not show the helper viewer URL:\n%s", view)
	}
}

func TestTUIInvokesHostAuthenticationWhenBrowserMCPIsStopped(t *testing.T) {
	root := copyCapabilityPackTree(t, "../../examples/capability-pack")
	packagePath := filepath.Join(root, "vendor", "neutral-tools", "guidance", "package.toml")
	manifest, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "host-auth-invoked")
	helperSource := filepath.Join(t.TempDir(), "host_auth.go")
	if err := os.WriteFile(helperSource, []byte(fmt.Sprintf("package main\nimport (\"fmt\"; \"os\")\nfunc main() { if err := os.WriteFile(%q, []byte(\"invoked\"), 0600); err != nil { panic(err) }; fmt.Println(\"{}\") }\n", marker)), 0600); err != nil {
		t.Fatal(err)
	}
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	manifestText := strings.Replace(string(manifest), "[[ui.sections]]", "[[inputs]]\nname = \"browser_volume\"\nlabel = \"Browser profile volume\"\ntype = \"string\"\nrequired = true\ndefault = \"aact-login-fixture\"\n[[ui.sections]]", 1)
	manifestText = strings.Replace(manifestText, "fields = [\"project\", \"enabled\"]", "fields = [\"project\", \"enabled\", \"browser_volume\"]", 1)
	manifestText = strings.Replace(manifestText, "endpoint_path = \"/mcp\"", "endpoint_path = \"/mcp\"\n[mcp.browser_controller]\nprofile_volume_input = \"browser_volume\"\nstartup_url = \"https://example.test\"", 1)
	manifestText += fmt.Sprintf("\n[mcp.actions.authenticate]\ncommand = [%q, %q, %q]\n", goCommand, "run", helperSource)
	if err := os.WriteFile(packagePath, []byte(manifestText), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := config.DiscoverPack(root, filepath.Join(root, "aact.toml"), "", store.Root())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	probe := agents.DiscoveryProbe{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) {
		if name == "opencode" {
			return "injected-detection-evidence", nil
		}
		return "", fmt.Errorf("not installed")
	}}
	runtime := &rotatingRuntime{store: store, instances: []mcp.Instance{{Key: state.Key{Source: pack.ID, Package: "company-guidance", Target: "ota"}, ID: "synthetic-runtime", Name: "reference-api", Status: "running", Ownership: "local", URL: "http://127.0.0.1:18871/mcp"}}}
	svc := app.New(pack.LegacySource(), store, app.Options{Runtime: runtime, DiscoveryProbe: &probe, AgentScopes: map[string]agents.Scope{"opencode": {ID: "opencode", Home: home, ExplicitHome: true}}})
	ref := config.InstanceRef{PackID: pack.ID, CapabilityID: "company-guidance", Name: "ota"}
	if _, err := svc.ApplyInstance(context.Background(), app.InstanceRequest{Ref: ref, DestinationIDs: []string{"opencode"}, ItemIDs: []string{"set:reference"}, Inputs: map[string]any{"project": "example-project", "enabled": false, "browser_volume": "aact-login-fixture"}}); err != nil {
		t.Fatalf("prepare installed instance: %v", err)
	}
	// Model the public runtime observation after the MCP was stopped.
	runtime.instances[0].Status = "stopped"
	model := tui.New(svc)
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("TUI did not load its initial catalog")
	}
	model, _ = model.Update(cmd())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("opening the selected Capability Instance did not request its workspace")
	}
	model, _ = model.Update(cmd())
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Log in to reference-api") || !strings.Contains(view, "Authenticate reference-api") {
		t.Fatalf("TUI did not expose both login actions for the stopped MCP:\n%s", view)
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("activating host authentication did not produce a UI event:\n%s", ansi.Strip(model.View().Content))
	}
	model, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("host authentication UI event did not invoke the backend")
	}
	model, _ = model.Update(cmd())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("host authentication command was not invoked while MCP was stopped: %v\n%s", err, ansi.Strip(model.View().Content))
	}
}

func copyCapabilityPackTree(t *testing.T, sourceRoot string) string {
	t.Helper()
	destinationRoot := filepath.Join(t.TempDir(), "capability-pack")
	err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(destinationRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, destination)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy capability pack to isolated test tree: %v", err)
	}
	return destinationRoot
}
