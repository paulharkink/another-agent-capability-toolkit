package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type providerTestRuntime struct {
	keys  []state.Key
	specs []mcp.RunSpec
	url   string
}

func (r *providerTestRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	r.keys = append(r.keys, key)
	r.specs = append(r.specs, spec)
	url := r.url
	if url == "" {
		url = "http://127.0.0.1/mcp"
	}
	return mcp.Instance{Key: key, URL: url, Status: "running", Ownership: "local"}, nil
}
func (*providerTestRuntime) Stop(context.Context, state.Key) error        { return nil }
func (*providerTestRuntime) List(context.Context) ([]mcp.Instance, error) { return nil, nil }
func (*providerTestRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(nilReader{}), nil
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestPackageMCPProfilesHonorsOptionalProviderInputs(t *testing.T) {
	p := catalog.Package{MCPs: []catalog.MCP{
		{Name: "github", EnabledInput: "github_enabled"},
		{Name: "gitlab", EnabledInput: "gitlab_enabled"},
		{Name: "bitbucket"},
	}}
	got := packageMCPProfiles(p, map[string]any{"github_enabled": true, "gitlab_enabled": false})
	if len(got) != 2 || got[0].Name != "github" || got[1].Name != "bitbucket" {
		t.Fatalf("enabled profiles = %#v", got)
	}
}

func TestMCPProfileKeysSeparateProfilesAndPreserveLegacyKeys(t *testing.T) {
	base := state.Key{Source: "source", Package: "git-provider", Environment: "sample-env", Target: "target-a"}
	legacy := catalog.Package{MCPs: []catalog.MCP{{Name: "inspector"}}}
	if got := mcpDefinitionKey(base, legacy, legacy.MCPs[0]); got.MCP != "inspector" || got.MCPDefinition != "" {
		t.Fatalf("legacy MCP child key changed: %#v", got)
	}
	p := catalog.Package{MCPs: []catalog.MCP{{Name: "github", EnabledInput: "github_enabled"}, {Name: "gitlab", EnabledInput: "gitlab_enabled"}}}
	github := mcpDefinitionKey(base, p, p.MCPs[0])
	gitlab := mcpDefinitionKey(base, p, p.MCPs[1])
	if github.MCPDefinition != "github" || gitlab.MCPDefinition != "gitlab" || github.ID() == gitlab.ID() {
		t.Fatalf("provider profiles not separated: github=%#v gitlab=%#v", github, gitlab)
	}
}

func TestResolveProviderTokenFromRawFileOrEnvironment(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_TEST_PROVIDER_TOKEN", "environment-token")
	cases := []struct {
		name    string
		profile catalog.MCP
		values  map[string]any
		want    string
	}{
		{"raw", catalog.MCP{TokenInput: "raw"}, map[string]any{"raw": "raw-token"}, "raw-token"},
		{"file", catalog.MCP{TokenFileInput: "file"}, map[string]any{"file": tokenFile}, "file-token"},
		{"environment", catalog.MCP{TokenEnvInput: "env"}, map[string]any{"env": "AACT_TEST_PROVIDER_TOKEN"}, "environment-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveProfileToken(tc.profile, tc.values)
			if err != nil || got != tc.want {
				t.Fatalf("resolveProfileToken() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := resolveProfileToken(catalog.MCP{TokenInput: "raw", TokenEnvInput: "env"}, map[string]any{"raw": "x", "env": "AACT_TEST_PROVIDER_TOKEN"}); err == nil {
		t.Fatal("multiple token sources should be rejected")
	}
}

func TestRunProfileMCPStartAppliesContainerArgsEnvironmentAndSecret(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	mcpProfile := catalog.MCP{
		Name: "bitbucket", Image: "example/bitbucket:1", Transport: "streamable-http", ContainerPort: 8080, HostPortInput: "port",
		Args: []string{"--http"}, Env: map[string]string{"STREAMABLE_HTTP": "true"},
		EnvInputs: map[string]string{"API_URL": "api_url"}, SecretEnvInputs: map[string]string{"TOKEN": "token"},
	}
	pkg := &svc.Source.Catalog[0]
	pkg.Skill = nil
	pkg.Skills = nil
	pkg.MCP = nil
	pkg.MCPs = []catalog.MCP{mcpProfile}
	pkg.Inputs = []catalog.Input{{Name: "port", Type: "integer", Required: true}, {Name: "api_url", Type: "string", Required: true}, {Name: "token", Type: "secret", Required: true}}
	ref := writeProfileForTest(t, svc, pkg.ID, "runtime", "")
	key, err := svc.Store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.RunProfileMCP(context.Background(), "start", ProfileRequest{Ref: ref, Inputs: map[string]any{
		"port": int64(18821), "api_url": "https://bitbucket.example/api", "token": "secret-token",
	}}, "bitbucket")
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.specs) != 1 || runtime.keys[0] != mcpDefinitionKey(key, *pkg, mcpProfile) || runtime.specs[0].HostPort != 18821 || !reflect.DeepEqual(runtime.specs[0].Args, []string{"--http"}) || runtime.specs[0].Env["API_URL"] != "https://bitbucket.example/api" || runtime.specs[0].SecretEnv["TOKEN"] != "secret-token" {
		t.Fatalf("profile runtime launch was incomplete: keys=%#v specs=%#v", runtime.keys, runtime.specs)
	}
}

func TestInstallRunsEnabledProviderProfilesAndInstallsSkillOnce(t *testing.T) {
	svc, env, store := fixture(t)
	env.ID = "generic:temporary"
	env.Kind = "opencode"
	env.ConfigPath = filepath.Join(t.TempDir(), "agent.json")
	env.SkillsDir = filepath.Join(t.TempDir(), "skills")
	profiles := []catalog.MCP{
		{Name: "github", EnabledInput: "github_enabled", Image: "example/github:1", Transport: "streamable-http", ContainerPort: 8080, EndpointPath: "/mcp", HostPortInput: "github_port"},
		{Name: "bitbucket", EnabledInput: "bitbucket_enabled", Image: "example/bitbucket:1", Transport: "streamable-http", ContainerPort: 8080, EndpointPath: "/mcp", HostPortInput: "bitbucket_port"},
		{Name: "gitlab", EnabledInput: "gitlab_enabled", Image: "example/gitlab:1", Transport: "streamable-http", ContainerPort: 8080, EndpointPath: "/mcp", HostPortInput: "gitlab_port"},
	}
	svc.Source.Catalog[0].MCPs = profiles
	svc.Source.Catalog[0].Inputs = []catalog.Input{
		{Name: "github_enabled", Type: "boolean", Default: true}, {Name: "github_port", Type: "integer", Default: int64(18101)},
		{Name: "bitbucket_enabled", Type: "boolean", Default: true}, {Name: "bitbucket_port", Type: "integer", Default: int64(18102)},
		{Name: "gitlab_enabled", Type: "boolean", Default: false}, {Name: "gitlab_port", Type: "integer", Default: int64(18103)},
	}
	runtime := &providerTestRuntime{}
	svc.Options.Runtime = runtime
	result, err := svc.applyProfileFixture(context.Background(), InstallRequest{Package: "demo", Agents: []agents.Environment{env}})
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.keys) != 2 || runtime.keys[0].MCPDefinition != "github" || runtime.keys[1].MCPDefinition != "bitbucket" {
		t.Fatalf("started profiles = %#v", runtime.keys)
	}
	skillCount, mcpCount := 0, 0
	for _, row := range result.Changes {
		switch row.Component {
		case "skill":
			skillCount++
		case "mcp":
			mcpCount++
		}
	}
	if skillCount != 1 || mcpCount != 2 {
		t.Fatalf("expected one skill and two MCP registrations: changes=%#v", result.Changes)
	}
	configured, err := store.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	var configuredMCPs []state.ProfileRecord
	for _, record := range configured {
		if record.Key.MCPDefinition != "" {
			configuredMCPs = append(configuredMCPs, record)
		}
	}
	if len(configuredMCPs) != 2 || configuredMCPs[0].Key.MCPDefinition == configuredMCPs[1].Key.MCPDefinition {
		t.Fatalf("configured MCP profiles = %#v (all profile records: %#v)", configuredMCPs, configured)
	}
}

type authenticationTestRuntime struct {
	providerTestRuntime
	actions    []string
	actionKeys []state.Key
	stateDirs  []string
	requests   []mcp.ActionRequest
	results    []string
}

func (r *authenticationTestRuntime) Exec(_ context.Context, key state.Key, argv []string, stdin []byte, stderr func([]byte)) ([]byte, error) {
	r.actions = append(r.actions, argv[len(argv)-1])
	r.actionKeys = append(r.actionKeys, key)
	var request mcp.ActionRequest
	if err := json.Unmarshal(stdin, &request); err != nil {
		return nil, err
	}
	r.requests = append(r.requests, request)
	r.stateDirs = append(r.stateDirs, request.StateDir)
	if len(r.results) == 0 {
		return []byte(`{}`), nil
	}
	result := r.results[0]
	r.results = r.results[1:]
	return []byte(result), nil
}

func TestRunProfileMCPAuthenticateReturnsURLAndUsesOwnedContainer(t *testing.T) {
	svc, _, _ := fixture(t)
	runtime := &authenticationTestRuntime{results: []string{`{"login_url":"https://login.example/device"}`}}
	svc.Options.Runtime = runtime
	pkg := &svc.Source.Catalog[0]
	pkg.Skill, pkg.Skills, pkg.MCP = nil, nil, nil
	definition := catalog.MCP{Name: "alpha", Image: "example/alpha:1", HealthPath: "/health", Actions: map[string]catalog.Command{
		"authenticate": {Argv: []string{"auth", "authenticate"}, Target: "container"},
	}}
	pkg.MCPs = []catalog.MCP{definition}
	pkg.Inputs = nil
	ref := writeProfileForTest(t, svc, pkg.ID, "runtime", "")
	result, err := svc.RunProfileMCP(context.Background(), "authenticate", ProfileRequest{Ref: ref}, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if result.LoginURL != "https://login.example/device" || len(result.Instances) != 1 {
		t.Fatalf("authenticate result = %#v", result)
	}
	if len(runtime.specs) != 1 || runtime.specs[0].DeferProtocolHealth {
		t.Fatalf("direct authenticate unexpectedly deferred normal runtime readiness: %+v", runtime.specs)
	}
	if !reflect.DeepEqual(runtime.actions, []string{"authenticate"}) {
		t.Fatalf("container actions = %#v", runtime.actions)
	}
	key, err := svc.Store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := mcpDefinitionKey(key, *pkg, definition)
	if len(runtime.actionKeys) != 1 || runtime.actionKeys[0] != wantKey {
		t.Fatalf("container action keys = %#v, want %#v", runtime.actionKeys, wantKey)
	}
	if len(runtime.stateDirs) != 1 || runtime.stateDirs[0] != "/state" {
		t.Fatalf("auth state dirs = %#v", runtime.stateDirs)
	}
	for _, forbidden := range []string{"authentication_status", "cancel_authentication"} {
		if _, err := svc.RunProfileMCP(context.Background(), forbidden, ProfileRequest{Ref: ref}, "alpha"); err == nil {
			t.Fatalf("obsolete action %q remained available", forbidden)
		}
	}
}
