package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/versioninfo"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProfileApplyGatesNoActionMCPOnDeclaredHealthPath(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http", HealthPath: "/health", TokenInput: "token", TokenHeader: "Authorization", TokenPrefix: "Bearer "}}
	p.Inputs = append(p.Inputs, catalog.Input{Name: "token", Type: "secret"})
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true, "token": "fixture-token"}

	checks, initializes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			checks++
			if checks == 1 {
				_, _ = fmt.Fprint(w, `{"authenticated":false}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			initializes++
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	runtime := &providerTestRuntime{url: server.URL + "/mcp"}
	s.Options.Runtime = runtime
	a.inspect = func() {
		if checks != 2 {
			t.Errorf("registration started after %d health checks, want false then true", checks)
		}
		if initializes != 1 {
			t.Errorf("registration started after %d initialize requests, want final protocol readiness", initializes)
		}
	}

	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || initializes != 1 {
		t.Fatalf("health checks = %d, initialize requests = %d; want false, true, then one initialize", checks, initializes)
	}
	if len(runtime.specs) != 1 || !runtime.specs[0].DeferProtocolHealth {
		t.Fatalf("runtime spec did not defer initialize readiness for auth gate: %+v", runtime.specs)
	}
}

type profileActionResultExecutor struct {
	output string
	order  *[]string
}

func (e profileActionResultExecutor) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	if e.order != nil {
		*e.order = append(*e.order, "authenticate")
	}
	return []byte(e.output), nil
}

type orderedProfileRuntime struct {
	providerTestRuntime
	order *[]string
}

func (r *orderedProfileRuntime) Start(ctx context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	if r.order != nil {
		*r.order = append(*r.order, "start")
	}
	return r.providerTestRuntime.Start(ctx, key, spec)
}

func TestProfileApplyGatesHostAuthenticationActionOnDeclaredHealthPath(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http", HealthPath: "/health", TokenInput: "token", TokenHeader: "Authorization", TokenPrefix: "Bearer ", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"auth", "authenticate"}, Target: "host"},
	}}}
	p.Inputs = append(p.Inputs, catalog.Input{Name: "token", Type: "secret"})
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true, "token": "fixture-token"}

	checks, initializes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
			t.Errorf("request authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			checks++
			if checks == 1 {
				_, _ = fmt.Fprint(w, `{"authenticated":false}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			initializes++
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	var order []string
	runtime := &orderedProfileRuntime{providerTestRuntime: providerTestRuntime{url: server.URL + "/mcp"}, order: &order}
	s.Options.Runtime = runtime
	s.Options.Runner = profileActionResultExecutor{output: `{"auth_required":true,"diagnostic":"stale CLI status","login_url":"https://login.example/device"}`, order: &order}
	a.inspect = func() {
		if checks != 2 || initializes != 1 {
			t.Errorf("registration began before health/protocol readiness: health=%d initialize=%d", checks, initializes)
		}
	}

	var progress []viewmodel.OperationProgress
	ctx := viewmodel.WithOperationProgress(context.Background(), func(event viewmodel.OperationProgress) { progress = append(progress, event) })
	result, err := s.ApplyProfile(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) == 0 {
		t.Fatalf("health endpoint did not authorize registration: %#v", result)
	}
	if !reflect.DeepEqual(order, []string{"authenticate", "start"}) {
		t.Fatalf("host action/start order = %#v", order)
	}
	if len(progress) == 0 || !strings.Contains(fmt.Sprint(progress), "https://login.example/device") {
		t.Fatalf("login URL was not retained: %#v", progress)
	}
}

func TestProfileApplyGatesExternalMCPURLOnDeclaredHealthPath(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http", HealthPath: "/health"}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	healthChecks := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			healthChecks++
			if healthChecks == 1 {
				_, _ = fmt.Fprint(w, `{"authenticated":false}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
		default:
			t.Errorf("unexpected external MCP request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer endpoint.Close()
	q.ExternalURLs = map[string]string{"alpha": endpoint.URL + "/mcp"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	runtime := &providerTestRuntime{}
	s.Options.Runtime = runtime
	a.inspect = func() {
		if len(runtime.keys) != 0 {
			t.Errorf("external MCP unexpectedly started locally: %#v", runtime.keys)
		}
	}

	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if healthChecks != 2 {
		t.Fatalf("external health checks = %d, want false then true", healthChecks)
	}
	if len(runtime.keys) != 0 {
		t.Fatalf("external MCP unexpectedly started locally: %#v", runtime.keys)
	}
}

func TestProfileApplyExternalURLWithoutHealthPathKeepsLegacySkip(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"auth", "authenticate"}, Target: "host"},
	}}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	q.ExternalURLs = map[string]string{"alpha": "https://external.example/mcp"}
	var order []string
	s.Options.Runner = profileActionResultExecutor{output: `{}`, order: &order}
	runtime := &providerTestRuntime{}
	s.Options.Runtime = runtime
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 || len(runtime.keys) != 0 {
		t.Fatalf("external URL without health path changed legacy flow: actions=%v starts=%v", order, runtime.keys)
	}
}

func TestProfileApplyDoesNotRegisterWhileDeclaredHealthPathRemainsFalse(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http", HealthPath: "/health"}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/health" {
			t.Errorf("unexpected request before authentication: %s %s", r.Method, r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"authenticated":false}`)
	}))
	defer server.Close()
	runtime := &providerTestRuntime{url: server.URL + "/mcp"}
	s.Options.Runtime = runtime
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	result, err := s.ApplyProfile(ctx, q)
	if err == nil || (err != context.DeadlineExceeded && !strings.Contains(err.Error(), context.DeadlineExceeded.Error())) {
		t.Fatalf("false health response did not time out: result=%#v err=%v", result, err)
	}
	if len(a.calls) != 0 {
		t.Fatalf("registration proceeded while health stayed false: %#v", a.calls)
	}
}

func TestProfileWithoutPrepareHonorsImageSourceSelection(t *testing.T) {
	for _, source := range []string{"release", "local"} {
		t.Run(source, func(t *testing.T) {
			s, _, q := profileApplyFixture(t)
			p := &s.Source.Catalog[0]
			p.MCPs = p.MCPs[:1]
			p.MCPs[0].Image = "aact/alpha:local"
			p.MCPs[0].BuildContext = "./docker"
			p.MCPs[0].ReleaseImage = "ghcr.io/example/alpha:v{aact_version}"
			p.MCPs[0].Actions = nil
			p.Dir = t.TempDir()
			if err := os.MkdirAll(filepath.Join(p.Dir, "docker"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := s.UISetMCPImageSource(context.Background(), source); err != nil {
				t.Fatal(err)
			}
			oldVersion := versioninfo.Version
			versioninfo.Version = "1.2.3"
			t.Cleanup(func() { versioninfo.Version = oldVersion })
			runtime := &providerTestRuntime{}
			s.Options.Runtime = runtime

			if _, err := s.RunProfileMCP(context.Background(), "start", q, "alpha"); err != nil {
				t.Fatal(err)
			}
			if len(runtime.specs) != 1 {
				t.Fatalf("runtime specs = %#v", runtime.specs)
			}
			got := runtime.specs[0]
			if source == "release" && (got.Image != "ghcr.io/example/alpha:v1.2.3" || got.BuildContext != "") {
				t.Fatalf("release selection was ignored: %+v", got)
			}
			if source == "local" && (got.Image != "" || got.BuildContext != filepath.Join(p.Dir, "docker")) {
				t.Fatalf("source selection did not use the configured build context: %+v", got)
			}
		})
	}
}

type releasePrepareExecutor struct{ request mcp.ActionRequest }

func (e *releasePrepareExecutor) Run(_ context.Context, _ []string, _ string, input []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	if err := json.Unmarshal(input, &e.request); err != nil {
		return nil, err
	}
	return []byte(`{"runtime":{"image":"ghcr.io/paulharkink/another-agent-capability-toolkit/cluster-inspector-mcp:v1.2.3","host":"127.0.0.1","host_port":9000,"container_port":8765,"transport":"streamable-http","endpoint_path":"/mcp"}}`), nil
}

func TestPreparedProfileRunPassesReleaseImageThroughToRuntime(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCPs = p.MCPs[:1]
	p.MCPs[0].ReleaseImage = "ghcr.io/paulharkink/another-agent-capability-toolkit/cluster-inspector-mcp:v{aact_version}"
	p.MCPs[0].BuildContext = "./mcp"
	p.MCPs[0].Actions = map[string]catalog.Command{"prepare": {Argv: []string{"fixture-prepare"}}}
	oldVersion := versioninfo.Version
	versioninfo.Version = "1.2.3"
	t.Cleanup(func() { versioninfo.Version = oldVersion })
	executor := &releasePrepareExecutor{}
	s.Options.Runner = executor
	runtime := &providerTestRuntime{}
	s.Options.Runtime = runtime
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	if _, err := s.RunProfileMCP(context.Background(), "start", q, "alpha"); err != nil {
		t.Fatal(err)
	}
	if executor.request.ImageSource != "release" || executor.request.ReleaseImage != "ghcr.io/paulharkink/another-agent-capability-toolkit/cluster-inspector-mcp:v1.2.3" {
		t.Fatalf("prepared action did not receive release choice: %+v", executor.request)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].Image != executor.request.ReleaseImage || runtime.specs[0].BuildContext != "" {
		t.Fatalf("prepared runtime did not preserve prebuilt image: %+v", runtime.specs)
	}
}

func TestLocalProfileNameUsesPackProfileGrammar(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	for _, name := range []string{"bad:name", "a b", ".hidden", "bad\rname"} {
		q.Ref.Name = name
		if err := s.CreateProfile(context.Background(), q.Ref); err == nil {
			t.Errorf("accepted unusable local profile %q", name)
		}
	}
}

func TestProfilePreservesAbsoluteDockerBuildContext(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCPs = p.MCPs[:1]
	p.Sets[0].MCPs = []string{"alpha"}
	build := t.TempDir()
	p.MCPs[0].BuildContext = build
	runtime := &providerTestRuntime{}
	s.Options.Runtime = runtime
	if _, err := s.RunProfileMCP(context.Background(), "start", q, "alpha"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].BuildContext != build {
		t.Fatalf("absolute context rewritten: %+v", runtime.specs)
	}
}

func TestProfileReusesDeclaredCredentialsWithUnchangedSource(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.MCPs = p.MCPs[:1]
	p.Sets[0].MCPs = []string{"alpha"}
	p.MCPs[0].Actions = map[string]catalog.Command{"authenticate": {Argv: []string{"fixture-auth"}}}
	p.MCPs[0].CredentialFiles = []string{"credential.bin"}
	p.Inputs = append(p.Inputs, catalog.Input{Name: "source_file", Type: "file"})
	q.Inputs = map[string]any{"source_file": filepath.Join(t.TempDir(), "deleted-original")}
	key, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	s.Store.SaveAnswers(key, q.Inputs)
	child := mcpProfileKey(key, *p, p.MCPs[0])
	os.MkdirAll(s.Store.AuthDir(child), 0700)
	os.WriteFile(filepath.Join(s.Store.AuthDir(child), "credential.bin"), []byte("managed-fixture"), 0600)
	runner := &countingPrepareExecutor{}
	s.Options.Runner = runner
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 {
		t.Fatal("unchanged missing source was imported again")
	}
}

func TestProfileApplyWaitsForWholeOperationStateLock(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Store.WithLock(context.Background(), func() error { close(held); <-release; return nil })
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := s.ApplyProfile(ctx, q)
	close(release)
	<-done
	if err == nil || len(a.calls) > 0 {
		t.Fatalf("mutation escaped operation lock: %v %v", err, a.calls)
	}
}
func TestProfileCLIPathsResolvedBeforeSaving(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	s.Source.Catalog[0].Inputs = append(s.Source.Catalog[0].Inputs, catalog.Input{Name: "folder", Type: "directory", Multiple: true})
	q.Inputs = map[string]any{"folder": []string{"./relative"}}
	q.ItemIDs = []string{}
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	key, _ := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	answers, _ := s.Store.Answers(key)
	got, _ := answers["folder"].([]any)
	if len(got) != 1 || !filepath.IsAbs(got[0].(string)) {
		t.Fatalf("relative CLI path persisted: %v", answers)
	}
}
func TestProfileRegistrationNamesValidatedBeforeEffects(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	for n := range p.MCPs {
		p.MCPs[n].RegistrationNameInput = "label"
	}
	_, err := s.ApplyProfile(context.Background(), q)
	if err == nil || len(a.calls) != 0 || s.Options.Runtime.(*fakeRuntime).starts != 0 {
		t.Fatalf("duplicate names reached effects: err=%v calls=%v", err, a.calls)
	}
}

func TestProfileApplyHoldsLockDuringAdapterEffects(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	a.inspect = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := s.Store.WithLock(ctx, func() error { return nil }); err == nil {
			t.Error("operation lock released before adapter effects")
		}
	}
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedProfilePreservesStandardImageSelectionPortsAndState(t *testing.T) {
	for _, imageSource := range []string{"release", "local"} {
		t.Run(imageSource, func(t *testing.T) {
			s, _, q := profileApplyFixture(t)
			p := &s.Source.Catalog[0]
			p.Dir = t.TempDir()
			contextDir := filepath.Join(p.Dir, "mcp")
			if err := os.MkdirAll(contextDir, 0700); err != nil {
				t.Fatal(err)
			}
			p.MCPs = p.MCPs[:1]
			p.MCPs[0].Image = ""
			p.MCPs[0].BuildContext = "mcp"
			p.MCPs[0].ReleaseImage = "ghcr.io/example/server:v{aact_version}"
			p.MCPs[0].Actions = map[string]catalog.Command{"prepare": {Argv: []string{"fixture-prepare"}}}
			p.MCPs[0].AdditionalPorts = []catalog.ConditionalPort{{ContainerPort: 7443, HostPortInput: "callback_port", When: map[string]any{"enabled": true}}}
			p.Inputs = append(p.Inputs, catalog.Input{Name: "callback_port", Type: "integer", Required: true})
			q.Inputs = map[string]any{"label": "company", "enabled": true, "callback_port": int64(17443)}
			if imageSource == "local" {
				if err := s.UISetMCPImageSource(context.Background(), "local"); err != nil {
					t.Fatal(err)
				}
			}
			oldVersion := versioninfo.Version
			versioninfo.Version = "1.2.3"
			t.Cleanup(func() { versioninfo.Version = oldVersion })
			executor := &releasePrepareExecutor{}
			s.Options.Runner = executor
			runtime := &providerTestRuntime{}
			s.Options.Runtime = runtime
			if _, err := s.RunProfileMCP(context.Background(), "start", q, "alpha"); err != nil {
				t.Fatal(err)
			}
			if len(runtime.specs) != 1 {
				t.Fatalf("expected one runtime spec: %+v", runtime.specs)
			}
			spec := runtime.specs[0]
			if imageSource == "release" && (spec.Image != "ghcr.io/example/server:v1.2.3" || spec.BuildContext != "") {
				t.Fatalf("release base selection was lost: %+v", spec)
			}
			if imageSource == "local" && (spec.Image != "" || spec.BuildContext != contextDir) {
				t.Fatalf("local base selection was lost: %+v", spec)
			}
			if len(spec.AdditionalPorts) != 1 || spec.AdditionalPorts[0] != (mcp.PortMapping{HostPort: 17443, ContainerPort: 7443}) {
				t.Fatalf("conditional ports were lost: %+v", spec.AdditionalPorts)
			}
			if len(spec.Mounts) != 1 || spec.Mounts[0].Destination != "/state" || spec.Mounts[0].ReadOnly {
				t.Fatalf("private state mount was lost: %+v", spec.Mounts)
			}
		})
	}
}
