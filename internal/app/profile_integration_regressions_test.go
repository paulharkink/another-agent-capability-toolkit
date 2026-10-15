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

func TestProfileApplyRegistersWithDeclaredHealthPathFalseAndSkipsProtocolInitialize(t *testing.T) {
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
			t.Errorf("Authorization header = %q, want Bearer fixture-token", got)
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
		if checks != 1 {
			t.Errorf("registration started after %d health checks, want one false observation", checks)
		}
		if initializes != 0 {
			t.Errorf("registration started after %d initialize requests, unauthenticated MCP should skip protocol initialize", initializes)
		}
	}

	result, err := s.ApplyProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationFailed {
		t.Fatalf("false health result status = %q, want AuthenticationFailed", result.AuthenticationStatus)
	}
	if checks != 1 || initializes != 0 {
		t.Fatalf("health checks = %d, initialize requests = %d; want one false probe and no initialize", checks, initializes)
	}
	if len(runtime.specs) != 1 || !runtime.specs[0].DeferProtocolHealth {
		t.Fatalf("runtime spec did not defer initialize readiness for auth gate: %+v", runtime.specs)
	}
}

type profileActionResultExecutor struct {
	output string
	order  *[]string
	calls  *int
}

func (e profileActionResultExecutor) Run(_ context.Context, _ []string, _ string, _ []byte, _ map[string]string, _ func([]byte)) ([]byte, error) {
	if e.calls != nil {
		(*e.calls)++
	}
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

func TestProfileApplyContinuesWithFalseHealthAfterHostAuthenticationAction(t *testing.T) {
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
		if checks != 1 || initializes != 0 {
			t.Errorf("registration did not continue after false health: health=%d initialize=%d", checks, initializes)
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
	if result.AuthenticationStatus != viewmodel.AuthenticationFailed {
		t.Fatalf("false health result status = %q, want AuthenticationFailed", result.AuthenticationStatus)
	}
	if checks != 1 || initializes != 0 {
		t.Fatalf("health checks = %d, initialize requests = %d; want one false probe and no initialize", checks, initializes)
	}
	if !reflect.DeepEqual(order, []string{"authenticate", "start"}) {
		t.Fatalf("host action/start order = %#v", order)
	}
	if len(progress) == 0 || !strings.Contains(fmt.Sprint(progress), "https://login.example/device") {
		t.Fatalf("login URL was not retained: %#v", progress)
	}
}

func TestProfileApplyContinuesAfterFalseExternalHealthObservation(t *testing.T) {
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
	if healthChecks != 1 {
		t.Fatalf("external health checks = %d, want one completed false-health observation", healthChecks)
	}
	if len(runtime.keys) != 0 {
		t.Fatalf("external MCP unexpectedly started locally: %#v", runtime.keys)
	}
}

type externalProfileActionRuntime struct {
	providerTestRuntime
	actionCalls int
}

func (r *externalProfileActionRuntime) Exec(context.Context, state.Key, []string, []byte, func([]byte)) ([]byte, error) {
	r.actionCalls++
	return []byte(`{}`), nil
}

func TestProfileApplyExternalURLSkipsLocalActionsAndStarts(t *testing.T) {
	for _, target := range []string{"none", "host", "container"} {
		t.Run(target, func(t *testing.T) {
			s, a, q := profileApplyFixture(t)
			p := &s.Source.Catalog[0]
			p.Skills, p.Skill, p.MCP = nil, nil, nil
			definition := catalog.MCP{Name: "alpha", Transport: "streamable-http", HealthPath: "/health", TokenInput: "token", TokenHeader: "Authorization", TokenPrefix: "Bearer "}
			if target != "none" {
				definition.Actions = map[string]catalog.Command{
					"prepare":      {Argv: []string{"prepare"}, Target: target},
					"authenticate": {Argv: []string{"authenticate"}, Target: target},
				}
			}
			p.MCPs = []catalog.MCP{definition}
			p.Inputs = append(p.Inputs, catalog.Input{Name: "token", Type: "secret"})
			p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
			q.ItemIDs = []string{"set:alpha-only"}
			q.Inputs = map[string]any{"label": "company", "enabled": true, "token": "fixture-token"}
			q.Interactive = true
			s.Options.Editor = func(_ context.Context, _ []catalog.Input, values map[string]any) (map[string]any, error) {
				return values, nil
			}

			healthChecks, initializes, actionCalls := 0, 0, 0
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
					t.Errorf("external request authorization = %q", got)
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/health":
					healthChecks++
					if healthChecks == 1 {
						_, _ = fmt.Fprint(w, `{"authenticated":false}`)
						return
					}
					_, _ = fmt.Fprint(w, `{"authenticated":true}`)
				case r.Method == http.MethodPost && r.URL.Path == "/mcp":
					initializes++
					_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
				default:
					t.Errorf("unexpected external MCP request = %s %s", r.Method, r.URL.Path)
				}
			}))
			defer endpoint.Close()
			q.ExternalURLs = map[string]string{"alpha": endpoint.URL + "/mcp"}
			runtime := &externalProfileActionRuntime{}
			s.Options.Runtime = runtime
			s.Options.Runner = profileActionResultExecutor{output: `{}`, calls: &actionCalls}
			a.inspect = func() {
				if healthChecks != 1 || initializes != 0 {
					t.Errorf("registration did not continue after false health: health=%d initialize=%d", healthChecks, initializes)
				}
			}

			result, err := s.ApplyProfile(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if result.AuthenticationStatus != viewmodel.AuthenticationFailed {
				t.Fatalf("authentication status = %q, want AuthenticationFailed", result.AuthenticationStatus)
			}
			if len(result.Changes) == 0 {
				t.Fatal("false health prevented registration")
			}
			if healthChecks != 1 || initializes != 0 {
				t.Fatalf("external health checks = %d, protocol initializes = %d; want one false probe and no initialize", healthChecks, initializes)
			}
			if actionCalls != 0 || runtime.actionCalls != 0 || len(runtime.keys) != 0 {
				t.Fatalf("external apply ran local work: host actions=%d container actions=%d starts=%v", actionCalls, runtime.actionCalls, runtime.keys)
			}
		})
	}
}

func TestProfileApplyExternalURLHealthFalseSkipsInitializeAndRegisters(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http", HealthPath: "/health"}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	healthChecks, initializes := 0, 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			healthChecks++
			_, _ = fmt.Fprint(w, `{"authenticated":false}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			initializes++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected external MCP request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer endpoint.Close()
	q.ExternalURLs = map[string]string{"alpha": endpoint.URL + "/mcp"}
	runtime := &providerTestRuntime{}
	s.Options.Runtime = runtime
	result, err := s.ApplyProfile(context.Background(), q)
	if err != nil {
		t.Fatalf("unauthenticated but healthy MCP blocked installation: %v", err)
	}
	if healthChecks != 1 || initializes != 0 {
		t.Fatalf("health checks = %d, initialize requests = %d; want one false health observation and no initialize", healthChecks, initializes)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationFailed {
		t.Fatalf("authentication status = %q, want AuthenticationFailed", result.AuthenticationStatus)
	}
	if len(result.Changes) == 0 || len(a.calls) == 0 {
		t.Fatalf("false health prevented registration: result=%+v calls=%v", result, a.calls)
	}
	if len(runtime.keys) != 0 {
		t.Fatalf("external MCP unexpectedly started locally: %v", runtime.keys)
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

func TestProfileApplyDoesNotRegisterWhenAuthenticatedProtocolInitializeFails(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http", HealthPath: "/health"}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	healthSeen, initializeSeen := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			healthSeen = true
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			initializeSeen = true
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	runtime := &providerTestRuntime{url: server.URL + "/mcp"}
	s.Options.Runtime = runtime
	result, err := s.ApplyProfile(context.Background(), q)
	if err == nil || !strings.Contains(err.Error(), "MCP health HTTP 503") {
		t.Fatalf("non-auth initialize failure was not surfaced: result=%#v err=%v", result, err)
	}
	if !healthSeen || !initializeSeen {
		t.Fatalf("requests: health=%t initialize=%t; want both probes", healthSeen, initializeSeen)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationComplete {
		t.Fatalf("authentication status = %q, want AuthenticationComplete", result.AuthenticationStatus)
	}
	if len(a.calls) != 0 {
		t.Fatalf("registration proceeded despite MCP protocol failure: %#v", a.calls)
	}
}

func TestProfileWithoutPrepareHonorsImageSourceSelection(t *testing.T) {
	for _, tc := range []struct {
		source, version string
	}{{"release", "1.2.3"}, {"local", "dev"}} {
		t.Run(tc.source, func(t *testing.T) {
			source := tc.source
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
			versioninfo.Version = tc.version
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
	child := mcpDefinitionKey(key, *p, p.MCPs[0])
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

func TestProfileApplyHealthProbeErrorReportsAuthenticationFailedAndKeepsCause(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{name: "HTTP error", body: `provider diagnostic fixture`, status: http.StatusServiceUnavailable},
		{name: "malformed payload", body: `{"authenticated":"unknown"}`, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, q := profileApplyFixture(t)
			p := &s.Source.Catalog[0]
			p.Skills, p.Skill, p.MCP = nil, nil, nil
			p.MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http", HealthPath: "/health"}}
			p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
			q.ItemIDs = []string{"set:alpha-only"}
			q.Inputs = map[string]any{"label": "company", "enabled": true}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/health" {
					t.Errorf("unexpected request after failed auth health: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer endpoint.Close()
			q.ExternalURLs = map[string]string{"alpha": endpoint.URL + "/mcp"}
			result, err := s.ApplyProfile(context.Background(), q)
			if err == nil || !strings.Contains(err.Error(), "MCP health") {
				t.Fatalf("probe failure lost its health diagnostic: result=%#v err=%v", result, err)
			}
			if result.AuthenticationStatus != "failed" {
				t.Fatalf("probe failure status = %q, want failed; error=%v", result.AuthenticationStatus, err)
			}
		})
	}
}

func TestProfileApplyProtocolFailureKeepsConfirmedAuthenticationComplete(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http", HealthPath: "/health"}}
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/mcp" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer endpoint.Close()
	q.ExternalURLs = map[string]string{"alpha": endpoint.URL + "/mcp"}
	result, err := s.ApplyProfile(context.Background(), q)
	if err == nil || !strings.Contains(err.Error(), "MCP health check failed") {
		t.Fatalf("protocol failure diagnostic missing: result=%#v err=%v", result, err)
	}
	if result.AuthenticationStatus != "complete" {
		t.Fatalf("protocol failure changed confirmed authentication status: %q", result.AuthenticationStatus)
	}
}

func TestProfileApplyMultiMCPHealthFailureDominatesEarlierSuccess(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Transport: "streamable-http", HealthPath: "/health"}, {Name: "beta", Transport: "streamable-http", HealthPath: "/health"}}
	p.Sets = []catalog.ComponentSet{{Name: "both", MCPs: []string{"alpha", "beta"}}}
	q.ItemIDs = []string{"set:both"}
	q.Inputs = map[string]any{"label": "company", "enabled": true}
	alpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			_, _ = fmt.Fprint(w, `{"authenticated":true}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/mcp" {
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`)
			return
		}
		t.Errorf("unexpected alpha request: %s %s", r.Method, r.URL.Path)
	}))
	defer alpha.Close()
	beta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer beta.Close()
	q.ExternalURLs = map[string]string{"alpha": alpha.URL + "/mcp", "beta": beta.URL + "/mcp"}
	result, err := s.ApplyProfile(context.Background(), q)
	if err == nil || result.AuthenticationStatus != "failed" {
		t.Fatalf("later MCP failure did not dominate earlier success: result=%#v err=%v", result, err)
	}
}

func TestProfileApplyKeepsUnauthenticatedHealthEnabledMCPRegisteredWithoutInitialize(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{
		Name: "alpha", Image: "fixture", Transport: "streamable-http",
		HealthPath: "/health", TokenInput: "token", TokenHeader: "Authorization", TokenPrefix: "Bearer ",
	}}
	p.Inputs = append(p.Inputs, catalog.Input{Name: "token", Type: "secret"})
	p.Sets = []catalog.ComponentSet{{Name: "alpha-only", MCPs: []string{"alpha"}}}
	q.ItemIDs = []string{"set:alpha-only"}
	q.Inputs = map[string]any{"label": "company", "enabled": true, "token": "invalid-token"}

	healthSeen, initializeSeen := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer invalid-token" {
			t.Errorf("Authorization = %q, want bearer credential", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			healthSeen = true
			_, _ = fmt.Fprint(w, `{"authenticated":false}`)
		case r.Method == http.MethodPost && r.URL.Path == "/":
			initializeSeen = true
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("initialize Content-Type = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "application/json, text/event-stream" {
				t.Errorf("initialize Accept = %q", got)
			}
			var initialize struct {
				JSONRPC string `json:"jsonrpc"`
				ID      int    `json:"id"`
				Method  string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&initialize); err != nil {
				t.Errorf("decode initialize request: %v", err)
			} else if initialize.JSONRPC != "2.0" || initialize.ID != 1 || initialize.Method != "initialize" {
				t.Errorf("initialize request = %#v", initialize)
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	q.ExternalURLs = map[string]string{"alpha": server.URL + "/"}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := s.ApplyProfile(ctx, q)
	if err != nil {
		t.Fatalf("unauthenticated MCP prevented install/registration: result=%#v err=%v", result, err)
	}
	if !healthSeen || initializeSeen {
		t.Fatalf("requests: health=%t initialize=%t; want false health observation with protocol initialize skipped", healthSeen, initializeSeen)
	}
	if result.AuthenticationStatus != viewmodel.AuthenticationFailed {
		t.Fatalf("authentication status = %q, want AuthenticationFailed", result.AuthenticationStatus)
	}
	if len(a.calls) == 0 || !strings.Contains(a.calls[len(a.calls)-1], "alpha") {
		t.Fatalf("MCP registration did not proceed after authentication failure: %v", a.calls)
	}
}

func TestRunInstanceMCPStartKeepsHealthFalseAsRunning(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	p := &s.Source.Catalog[0]
	p.Skills, p.Skill, p.MCP = nil, nil, nil
	p.MCPs = []catalog.MCP{{Name: "alpha", Image: "fixture", Transport: "streamable-http", HealthPath: "/health", TokenInput: "token", TokenHeader: "Authorization", TokenPrefix: "Bearer "}}
	p.Inputs = append(p.Inputs, catalog.Input{Name: "token", Type: "secret"})
	q.Inputs["token"] = "invalid-token"

	checks, initializes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer invalid-token" {
			t.Errorf("authorization header = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			checks++
			_, _ = fmt.Fprint(w, `{"authenticated":false}`)
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			initializes++
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `unauthorized`)
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	runtime := &providerTestRuntime{url: server.URL + "/mcp"}
	s.Options.Runtime = runtime

	result, err := s.RunInstanceMCP(context.Background(), "start", InstanceRequest{Ref: q.Ref, Inputs: q.Inputs}, "alpha")
	if err != nil {
		t.Fatalf("unauthenticated MCP start should preserve running container: %v", err)
	}
	if len(result.Instances) != 1 || result.Instances[0].Status != "running" {
		t.Fatalf("runtime result = %#v, want one running MCP", result.Instances)
	}
	if checks != 1 || initializes != 0 {
		t.Fatalf("health requests = %d, initialize requests = %d, want one health request and no protocol initialize while unauthenticated", checks, initializes)
	}
	if len(runtime.specs) != 1 || !runtime.specs[0].DeferProtocolHealth {
		t.Fatalf("runtime did not defer startup initialize until auth health was checked: %#v", runtime.specs)
	}
}
