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
			for _, want := range []string{"Saved: yes", "Authentication: not confirmed", "Applied effects: none reported"} {
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
