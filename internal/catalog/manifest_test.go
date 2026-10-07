package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const plainManifest = `schema_version = 1
id = "plain"
name = "Plain"
[skill]
name = "plain"
`

func TestLoadPlainSkill(t *testing.T) {
	for _, manifest := range []bool{true, false} {
		t.Run(map[bool]string{true: "manifest", false: "fallback"}[manifest], func(t *testing.T) {
			dir := t.TempDir()
			if manifest {
				dir = writeManifest(t, fixture(t, "plain.toml"))
			} else {
				dir = filepath.Join(dir, "plain")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Plain"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p, e := Load(dir)
			if e != nil {
				t.Fatal(e)
			}
			if p.SchemaVersion != 1 || p.ID != "plain" || p.Skill == nil || p.Skill.Name != "plain" {
				t.Fatalf("bad package: %#v", p)
			}
		})
	}
}

func TestOptionsFromRequiresMultiChoiceAndWildcard(t *testing.T) {
	for _, manifest := range []string{
		plainManifest + `[[inputs]]
name = "connections"
type = "string"
options_from = "dbms.*.tenants.*"
`,
		plainManifest + `[[inputs]]
name = "connections"
type = "multichoice"
options_from = "dbms.tenants"
`,
		plainManifest + `[[inputs]]
name = "connections"
type = "multichoice"
options_from = "dbms.foo*.tenants"
`,
	} {
		if _, err := Load(writeManifest(t, manifest)); err == nil || !strings.Contains(err.Error(), "options_from") {
			t.Fatalf("invalid options_from declaration accepted: %v", err)
		}
	}
}
func TestLoadBundle(t *testing.T) {
	p, e := Load(writeManifest(t, fixture(t, "bundle.toml")))
	if e != nil {
		t.Fatal(e)
	}
	if p.MCP == nil || p.MCP.Name != "plain" || p.Inputs[0].Default != int64(8765) || p.Generator.TimeoutSeconds != 300 {
		t.Fatalf("bad bundle: %#v", p)
	}
}

func TestRegistrationTimeoutMetadata(t *testing.T) {
	p, err := Load(writeManifest(t, plainManifest+`[mcp]
name = "plain"
registration_timeout_ms = 60000
`))
	if err != nil || p.MCP == nil || p.MCP.RegistrationTimeoutMS != 60000 {
		t.Fatalf("registration timeout metadata: %#v, %v", p.MCP, err)
	}
	if _, err := Load(writeManifest(t, plainManifest+`[mcp]
name = "plain"
registration_timeout_ms = -1
`)); err == nil {
		t.Fatal("negative registration timeout accepted")
	}
}

func TestLoadMultipleOptionalMCPProfiles(t *testing.T) {
	p, err := Load(writeManifest(t, plainManifest+`[[inputs]]
name = "enable_github"
type = "boolean"
default = false

[[inputs]]
name = "github_port"
type = "integer"
default = 8781

[[inputs]]
name = "enable_bitbucket"
type = "boolean"
default = false

[[inputs]]
name = "bitbucket_port"
type = "integer"
default = 8782

[[mcps]]
name = "github"
runtime = "docker"
image = "github/mcp:1"
transport = "streamable-http"
container_port = 8080
endpoint_path = "/mcp"
host_port_input = "github_port"
enabled_input = "enable_github"

[[mcps]]
name = "bitbucket"
runtime = "docker"
image = "bitbucket/mcp:1"
transport = "streamable-http"
container_port = 3001
endpoint_path = "/mcp"
host_port_input = "bitbucket_port"
enabled_input = "enable_bitbucket"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.MCPs) != 2 || p.MCPs[0].Name != "github" || p.MCPs[1].Name != "bitbucket" {
		t.Fatalf("MCP profiles not loaded in manifest order: %#v", p.MCPs)
	}
}

func TestLoadMCPProfileContainerArgumentsAndEnvironment(t *testing.T) {
	p, err := Load(writeManifest(t, plainManifest+`[[inputs]]
name = "api_url"
type = "string"

[[inputs]]
name = "access_token"
type = "secret"

[[mcps]]
name = "gitlab"
runtime = "docker"
image = "example/gitlab-mcp:1"
args = ["--http"]
env = { STREAMABLE_HTTP = "true" }
env_inputs = { GITLAB_API_URL = "api_url" }
secret_env_inputs = { GITLAB_TOKEN = "access_token" }
transport = "streamable-http"
container_port = 3002
endpoint_path = "/mcp"
`))
	if err != nil {
		t.Fatal(err)
	}
	profile := p.MCPs[0]
	if len(profile.Args) != 1 || profile.Args[0] != "--http" || profile.Env["STREAMABLE_HTTP"] != "true" || profile.EnvInputs["GITLAB_API_URL"] != "api_url" || profile.SecretEnvInputs["GITLAB_TOKEN"] != "access_token" {
		t.Fatalf("MCP profile launch configuration did not load: %#v", profile)
	}
}

func TestPackageHasMCPForLegacyAndProfileManifests(t *testing.T) {
	for name, p := range map[string]Package{
		"legacy":   {MCP: &MCP{Name: "legacy"}},
		"profiles": {MCPs: []MCP{{Name: "github"}, {Name: "gitlab"}}},
		"none":     {},
	} {
		want := name != "none"
		if got := p.HasMCP(); got != want {
			t.Errorf("%s: HasMCP()=%v, want %v", name, got, want)
		}
	}
}

func TestRejectDuplicateMCPProfileNames(t *testing.T) {
	manifest := plainManifest + `[[mcps]]
name = "github"
[[mcps]]
name = "github"
`
	if _, err := Load(writeManifest(t, manifest)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate MCP profile name should be rejected, got %v", err)
	}
}

func TestRejectMCPProfileWithUnknownEnableInput(t *testing.T) {
	manifest := plainManifest + `[[mcps]]
name = "github"
enabled_input = "missing_toggle"
`
	if _, err := Load(writeManifest(t, manifest)); err == nil || !strings.Contains(err.Error(), "enabled_input") {
		t.Fatalf("unknown MCP enable input should be rejected, got %v", err)
	}
}

func TestRejectMCPProfileEnableInputOfWrongType(t *testing.T) {
	manifest := plainManifest + `[[inputs]]
name = "enable_github"
type = "string"
[[mcps]]
name = "github"
enabled_input = "enable_github"
`
	if _, err := Load(writeManifest(t, manifest)); err == nil || !strings.Contains(err.Error(), "must be boolean") {
		t.Fatalf("non-boolean MCP enable input should be rejected, got %v", err)
	}
}

func TestRejectMCPProfileWithInvalidTokenHeader(t *testing.T) {
	manifest := plainManifest + `[[inputs]]
name = "token"
type = "secret"
[[mcps]]
name = "github"
token_input = "token"
token_header = "Authorization\nX-Injected: yes"
`
	if _, err := Load(writeManifest(t, manifest)); err == nil || !strings.Contains(err.Error(), "token_header") {
		t.Fatalf("invalid token header should be rejected by the manifest loader, got %v", err)
	}
}

func TestLoadExclusiveInputGroup(t *testing.T) {
	p, err := Load(writeManifest(t, plainManifest+`[[inputs]]
name = "token"
type = "secret"
exclusive_group = "cluster_credentials"
[[inputs]]
name = "kubeconfig"
type = "file"
exclusive_group = "cluster_credentials"
`))
	if err != nil || len(p.Inputs) != 2 || p.Inputs[0].ExclusiveGroup != "cluster_credentials" || p.Inputs[1].ExclusiveGroup != "cluster_credentials" {
		t.Fatalf("exclusive input metadata lost: %#v, %v", p.Inputs, err)
	}
}
func TestRejectUnsupportedVersion(t *testing.T) {
	_, e := Load(writeManifest(t, fixture(t, "invalid.toml")))
	if e == nil || !strings.Contains(e.Error(), "schema") {
		t.Fatalf("error %v", e)
	}
}
func TestRejectUnknownKey(t *testing.T) {
	_, e := Load(writeManifest(t, plainManifest+"mystery = true\n"))
	if e == nil || !strings.Contains(e.Error(), "package.toml") || !strings.Contains(e.Error(), "mystery") {
		t.Fatalf("error %v", e)
	}
}
func TestRejectDuplicateInputs(t *testing.T) {
	_, e := Load(writeManifest(t, plainManifest+`[[inputs]]
name="x"
type="string"
[[inputs]]
name="x"
type="boolean"
`))
	if e == nil || !strings.Contains(e.Error(), "duplicate") {
		t.Fatalf("error %v", e)
	}
}
func TestRejectInvalidManifest(t *testing.T) {
	for _, body := range []string{`schema_version=1
id="../escape"`, `schema_version=1
id="empty"`, plainManifest + `[[inputs]]
name="x"
type="imaginary"`, plainManifest + `[generator]
command=["run"]
timeout_seconds=-1`} {
		if _, e := Load(writeManifest(t, body)); e == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestSecretAndMultipleChoiceManifest(t *testing.T) {
	p, e := Load(writeManifest(t, plainManifest+`[[inputs]]
name="token"
type="secret"
[[inputs]]
name="teams"
type="multichoice"
default=["alpha","beta"]
[[inputs.options]]
value="alpha"
label="Alpha"
[[inputs.options]]
value="beta"
label="Beta"
`))
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Inputs) != 2 || p.Inputs[1].Type != "multichoice" {
		t.Fatalf("%#v", p.Inputs)
	}
}

func TestRejectDotIdentifiersAndExplicitZeroTimeout(t *testing.T) {
	for _, body := range []string{strings.Replace(plainManifest, `id = "plain"`, `id = "."`, 1), strings.Replace(plainManifest, `id = "plain"`, `id = ".."`, 1), plainManifest + `[generator]
command=["run"]
timeout_seconds=0
`} {
		if _, e := Load(writeManifest(t, body)); e == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, e := os.ReadFile(filepath.Join("testdata", name))
	if e != nil {
		t.Fatal(e)
	}
	return string(data)
}
