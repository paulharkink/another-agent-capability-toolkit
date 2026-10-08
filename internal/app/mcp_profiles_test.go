package app

import (
	"context"
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
}

func (r *providerTestRuntime) Start(_ context.Context, key state.Key, spec mcp.RunSpec) (mcp.Instance, error) {
	r.keys = append(r.keys, key)
	r.specs = append(r.specs, spec)
	return mcp.Instance{Key: key, URL: "http://127.0.0.1/mcp", Status: "running", Ownership: "local"}, nil
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
	if got := mcpProfileKey(base, legacy, legacy.MCPs[0]); got.MCP != "inspector" || got.Profile != "" {
		t.Fatalf("legacy MCP child key changed: %#v", got)
	}
	p := catalog.Package{MCPs: []catalog.MCP{{Name: "github", EnabledInput: "github_enabled"}, {Name: "gitlab", EnabledInput: "gitlab_enabled"}}}
	github := mcpProfileKey(base, p, p.MCPs[0])
	gitlab := mcpProfileKey(base, p, p.MCPs[1])
	if github.Profile != "github" || gitlab.Profile != "gitlab" || github.ID() == gitlab.ID() {
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
	if len(runtime.specs) != 1 || runtime.keys[0] != mcpProfileKey(key, *pkg, mcpProfile) || runtime.specs[0].HostPort != 18821 || !reflect.DeepEqual(runtime.specs[0].Args, []string{"--http"}) || runtime.specs[0].Env["API_URL"] != "https://bitbucket.example/api" || runtime.specs[0].SecretEnv["TOKEN"] != "secret-token" {
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
	if len(runtime.keys) != 2 || runtime.keys[0].Profile != "github" || runtime.keys[1].Profile != "bitbucket" {
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
		if record.Key.Profile != "" {
			configuredMCPs = append(configuredMCPs, record)
		}
	}
	if len(configuredMCPs) != 2 || configuredMCPs[0].Key.Profile == configuredMCPs[1].Key.Profile {
		t.Fatalf("configured MCP profiles = %#v (all profile records: %#v)", configuredMCPs, configured)
	}
}
