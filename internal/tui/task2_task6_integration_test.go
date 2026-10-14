package tui

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type integratedAuthActionExecutor struct{ diagnostic bool }

type capturedProfileAuthExecutor struct {
	request mcp.ActionRequest
}

func (e *capturedProfileAuthExecutor) Run(_ context.Context, _ []string, _ string, input []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	if err := json.Unmarshal(input, &e.request); err != nil {
		return nil, err
	}
	cookie, _ := e.request.Inputs["grafana_session"].(string)
	return json.Marshal(mcp.ActionResult{AuthRequired: true, Diagnostic: "Grafana rejected session " + cookie})
}

func (e integratedAuthActionExecutor) Run(_ context.Context, _ []string, _ string, input []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	var request mcp.ActionRequest
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	secret, _ := request.Inputs["token"].(string)
	if stderr != nil {
		logs := strings.Repeat("Docker probe logs: checking provider endpoint and image state\n", 500)
		stderr([]byte(logs + "secondary output " + secret))
	}
	diagnostic := ""
	if e.diagnostic {
		diagnostic = "Kubernetes API rejected token " + secret + " (HTTP 401)."
	}
	return json.Marshal(mcp.ActionResult{AuthRequired: true, Diagnostic: diagnostic})
}

type integratedAuthRuntime struct{ starts int }

func (r *integratedAuthRuntime) Start(context.Context, state.Key, mcp.RunSpec) (mcp.Instance, error) {
	r.starts++
	return mcp.Instance{}, os.ErrInvalid
}
func (*integratedAuthRuntime) Stop(context.Context, state.Key) error        { return nil }
func (*integratedAuthRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*integratedAuthRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type integratedAuthAdapter struct{}

func (integratedAuthAdapter) ID() string                  { return "fixture-agent" }
func (integratedAuthAdapter) Name() string                { return "Fixture agent" }
func (integratedAuthAdapter) Features() agents.FeatureSet { return agents.FeatureSet{MCPs: true} }
func (integratedAuthAdapter) Detect(context.Context, agents.Scope) (agents.Detection, error) {
	return agents.Detection{Installed: true, State: "installed"}, nil
}
func (integratedAuthAdapter) Observe(context.Context, agents.Scope, agents.ObservationRequest) (agents.Observation, error) {
	return agents.Observation{}, nil
}

type integratedAuthAdapters struct{ adapter agents.Adapter }

func (a integratedAuthAdapters) Adapter(string) (agents.Adapter, error) { return a.adapter, nil }
func (a integratedAuthAdapters) Adapters() []agents.Adapter             { return []agents.Adapter{a.adapter} }

func TestIntegratedUIInstallAuthRequiredFeedbackSeparatesEffectsAndRedactsDiagnostic(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		withDiagnostic bool
		prepareOnly    bool
		wantError      bool
	}{
		{name: "legacy auth-required result"},
		{name: "provider diagnostic", withDiagnostic: true, wantError: true},
		{name: "prepare-only provider diagnostic", withDiagnostic: true, prepareOnly: true, wantError: true},
		{name: "prepare-only legacy fallback", prepareOnly: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			const secret = "integrated-fixture-secret"
			root := t.TempDir()
			home := filepath.Join(root, "home")
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			pkgDir := filepath.Join(root, "package")
			if err := os.MkdirAll(pkgDir, 0700); err != nil {
				t.Fatal(err)
			}
			pkg := catalog.Package{
				ID: "demo", Name: "Demo", Dir: pkgDir,
				Inputs: []catalog.Input{{Name: "token", Label: "Token", Type: "secret", Required: true}},
				MCP:    &catalog.MCP{Name: "demo", Image: "fixture/image", Transport: "streamable-http", ContainerPort: 9000, EndpointPath: "/mcp", Actions: map[string]catalog.Command{}},
			}
			if scenario.prepareOnly {
				pkg.MCP.Actions["prepare"] = catalog.Command{Argv: []string{"fixture-prepare"}}
			} else {
				pkg.MCP.Actions["authenticate"] = catalog.Command{Argv: []string{"fixture-auth"}}
			}
			store, err := state.Open(filepath.Join(root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			runtime := &integratedAuthRuntime{}
			service := app.New(config.Source{
				ID: "fixture-pack", Root: root, ProfileRoot: filepath.Join(root, "profiles"),
				Catalog: []catalog.Package{pkg}, PackageDefaults: map[string]map[string]any{},
			}, store, app.Options{
				Runner: integratedAuthActionExecutor{diagnostic: scenario.withDiagnostic}, Runtime: runtime,
				Adapters: integratedAuthAdapters{adapter: integratedAuthAdapter{}},
			})
			ref := config.ProfileRef{PackID: "fixture-pack", CapabilityID: "demo", Name: "default"}
			if err := service.CreateProfile(t.Context(), ref); err != nil {
				t.Fatal(err)
			}

			model := NewContext(t.Context(), service)
			model.width, model.height = 120, 32
			key := state.Key{Source: ref.PackID, Package: ref.CapabilityID, Target: ref.Name}
			model.pendingSetup = &viewmodel.SetupPreview{
				Key:    key,
				Inputs: []viewmodel.SetupInput{{Definition: pkg.Inputs[0], Editable: true}},
			}
			model.pendingSetupField = "__destinations"
			model.pendingSetupItemsField = "__items"
			cmd := model.applySetup(map[string]any{
				"__destinations": []string{"fixture-agent"},
				"__items":        []string{"mcp:demo"},
				"token":          secret,
			})
			batch, ok := cmd().(tea.BatchMsg)
			if !ok || len(batch) == 0 {
				t.Fatalf("setup did not start the real UIInstall operation: %T", batch)
			}
			message, ok := batch[0]().(operationMsg)
			if !ok {
				t.Fatalf("setup operation returned %T, want operationMsg", batch[0]())
			}
			if scenario.wantError != (message.err != nil) {
				t.Fatalf("unexpected operation error contract: error=%v wantError=%t", message.err, scenario.wantError)
			}
			for len(model.setupProgressEvents) > 0 {
				progress := <-model.setupProgressEvents
				model.Update(setupProgressMsg{setupID: message.setupID, progress: progress})
			}
			model.Update(message)
			if runtime.starts != 0 {
				t.Fatalf("auth-required response proceeded to runtime start: %d starts", runtime.starts)
			}
			rows, err := store.Installations()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Component == "mcp" {
					t.Fatalf("auth-required response persisted a registration effect: %+v", row)
				}
			}
			if model.result == nil || !model.result.Failed {
				t.Fatalf("auth-required operation appeared successful: %+v", model.result)
			}
			joined := strings.Join(model.result.Rows, "\n")
			for _, want := range []string{"Saved: yes", "Authentication: not confirmed", "Applied effects: none reported", "aact mcp authenticate"} {
				if !strings.Contains(joined, want) {
					t.Errorf("foreground result omitted %q:\n%s", want, joined)
				}
			}
			if strings.Contains(joined, "registered") {
				t.Fatalf("auth-required save claimed a registration effect:\n%s", joined)
			}
			if strings.Contains(joined, secret) {
				t.Fatal("secret reached TUI result")
			}
			if scenario.withDiagnostic {
				cause := "Kubernetes API rejected token [redacted] (HTTP 401)."
				causeAt, logsAt := strings.Index(joined, cause), strings.Index(joined, "Docker probe logs:")
				if causeAt < 0 || logsAt < 0 || causeAt > logsAt {
					t.Fatalf("provider cause was not primary before long output: cause=%d logs=%d\n%s", causeAt, logsAt, joined)
				}
				if !strings.Contains(joined, "aact mcp authenticate demo") {
					t.Fatalf("retry guidance missing from foreground result:\n%s", joined)
				}
			} else if !strings.Contains(joined, "demo authentication required; run aact mcp authenticate demo") {
				t.Fatalf("legacy auth-required action guidance missing:\n%s", joined)
			}
		})
	}
}

func TestTUISaveApplyAuthForwardsSelectedProfileAndResolvedInputsWithoutEnvironment(t *testing.T) {
	const (
		profileCookie = "fixture-saved-cookie"
		formCookie    = "fixture-form-cookie"
		profileExpiry = "fixture-saved-expiry"
	)
	for _, name := range []string{"GRAFANA_SESSION", "GRAFANA_SESSION_EXPIRY"} {
		previous, hadValue := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if hadValue {
				_ = os.Setenv(name, previous)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pkgDir := filepath.Join(root, "package")
	if err := os.MkdirAll(pkgDir, 0700); err != nil {
		t.Fatal(err)
	}
	profileRoot := filepath.Join(root, "profiles")
	if err := os.MkdirAll(filepath.Join(profileRoot, "grafana"), 0700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileRoot, "grafana", "production.toml")
	if err := os.WriteFile(profilePath, []byte("[inputs]\ngrafana_session = \"fixture-profile-default\"\nsession_expiry = \"fixture-expiry-default\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := catalog.Package{
		ID: "grafana", Name: "Grafana",
		Dir: pkgDir,
		Inputs: []catalog.Input{
			{Name: "grafana_session", Label: "Grafana session cookie", Type: "secret", Required: true},
			{Name: "session_expiry", Label: "Session expiry", Type: "string", Required: true},
		},
		MCP: &catalog.MCP{Name: "grafana", Transport: "streamable-http", ContainerPort: 9000, Actions: map[string]catalog.Command{
			"authenticate": {Argv: []string{"fixture-grafana-auth"}},
		}},
	}
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	executor := &capturedProfileAuthExecutor{}
	service := app.New(config.Source{
		ID: "fixture-pack", Root: root, ProfileRoot: profileRoot,
		Catalog: []catalog.Package{pkg}, PackageDefaults: map[string]map[string]any{},
	}, store, app.Options{
		Runner: executor, Runtime: &integratedAuthRuntime{},
		Adapters: integratedAuthAdapters{adapter: integratedAuthAdapter{}},
	})
	ref := config.ProfileRef{PackID: "fixture-pack", CapabilityID: "grafana", Name: "production"}
	key := state.Key{Source: ref.PackID, Package: ref.CapabilityID, Target: ref.Name}
	if err := store.SaveAnswers(key, map[string]any{"grafana_session": profileCookie, "session_expiry": profileExpiry}); err != nil {
		t.Fatal(err)
	}
	model := NewContext(t.Context(), service)
	model.width, model.height = 120, 32
	open := model.openTargetWorkspace(viewmodel.SetupRequest{Ref: ref}, "Authentication")
	if open == nil {
		t.Fatal("opening selected profile did not request its setup form")
	}
	model.Update(open())
	if model.form == nil || model.pendingSetup == nil {
		t.Fatalf("selected profile did not open its form: form=%v preview=%+v", model.form != nil, model.pendingSetup)
	}
	values := model.form.Values()
	if values["grafana_session"] != profileCookie || values["session_expiry"] != profileExpiry {
		t.Fatalf("saved profile inputs were not loaded into form: %#v", values)
	}
	formOverrides := map[string]any{"grafana_session": formCookie}
	if model.pendingSetupField != "" {
		formOverrides[model.pendingSetupField] = []string{"fixture-agent"}
	}
	if model.pendingSetupItemsField != "" {
		formOverrides[model.pendingSetupItemsField] = []string{"mcp:grafana"}
	}
	if err := model.form.ApplyValues(formOverrides); err != nil {
		t.Fatal(err)
	}
	values = model.form.Values()
	if values["grafana_session"] != formCookie || values["session_expiry"] != profileExpiry {
		t.Fatalf("form overrides were not retained: %#v", values)
	}
	cmd := model.applySetup(values)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("Save & Apply did not start UIInstall: %T", batch)
	}
	message, ok := batch[0]().(operationMsg)
	if !ok {
		t.Fatalf("Save & Apply returned %T, want operationMsg", batch[0]())
	}
	if executor.request.Action != "authenticate" {
		t.Fatalf("action helper did not receive auth request: %+v; operation err=%v output=%q", executor.request, message.err, message.output)
	}
	if executor.request.Profile.Ref != ref || executor.request.Target.Environment != ref.PackID || executor.request.Target.Name != ref.Name || executor.request.Target.Path != profilePath {
		t.Fatalf("authentication request lost selected profile identity: profile=%+v target=%+v", executor.request.Profile, executor.request.Target)
	}
	if executor.request.Inputs["grafana_session"] != formCookie || executor.request.Inputs["session_expiry"] != profileExpiry {
		t.Fatalf("authentication request lost resolved form overrides: %#v", executor.request.Inputs)
	}
	if message.err == nil && !message.failed {
		t.Fatalf("expected fixture auth-required response to be reported as failure: %+v", message)
	}
	diagnostic := message.output
	if message.err != nil {
		diagnostic += "\n" + message.err.Error()
	}
	for _, value := range []string{profileCookie, formCookie, profileExpiry} {
		if strings.Contains(diagnostic, value) {
			t.Fatalf("credential value leaked to TUI diagnostic: %q", value)
		}
	}
}
