package catalog

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestInvalidPatternIncludesInputAndManifest(t *testing.T) {
	dir := writeManifest(t, plainManifest+"[[inputs]]\nname = \"name\"\ntype = \"string\"\nregex = \"[\"\n")
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "name") || !strings.Contains(err.Error(), "package.toml") {
		t.Fatalf("malformed pattern accepted or not identified: %v", err)
	}
}

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

func TestLoadAndResolveHCLManifestExpressions(t *testing.T) {
	dir := writeManifest(t, `schema_version = 1
id = "hcl-capability"
name = "${ upper(\"guide\") }"
[skill]
name = "guidance"
[[inputs]]
name = "enabled"
type = "boolean"
default = "${ true }"
[[inputs]]
name = "token"
type = "secret"
regex = "${ \"^[A-Z]+$\" }"
[[inputs]]
name = "timeout"
type = "integer"
default = 45
[[inputs]]
name = "arguments"
type = "multichoice"
default = ["--verbose", "--safe"]
[[inputs]]
name = "require_consent"
type = "boolean"
default = false
[[inputs]]
name = "consent"
type = "boolean"
required = "${ inputs.require_consent }"
[mcp]
name = "service"
token_input = "token"
token_header = "${ base64encode(inputs.token) }"
registration_timeout_ms = "${ inputs.timeout }"
args = "${ inputs.arguments }"
[mcp.env]
TOKEN = "${ inputs.token }"
[mcp.actions.authenticate]
command = ["./helper", "${inputs.token}"]
`)
	pkg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "GUIDE" || pkg.Inputs[0].Default != true {
		t.Fatalf("static HCL results were not typed before schema decode: %#v", pkg)
	}
	if pkg.MCP.TokenHeader != `${ base64encode(inputs.token) }` {
		t.Fatalf("runtime expression was not retained for a selected profile: %q", pkg.MCP.TokenHeader)
	}
	resolved, err := ResolveExpressions(pkg, map[string]any{"token": "ada-token", "timeout": int64(45), "arguments": []string{"--verbose", "--safe"}, "require_consent": true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.MCP.TokenHeader != "YWRhLXRva2Vu" || resolved.MCP.Actions["authenticate"].Argv[1] != "ada-token" {
		t.Fatalf("runtime expressions did not use resolved inputs: %#v", resolved.MCP)
	}
	if resolved.MCP.RegistrationTimeoutMS != 45 || !resolved.Inputs[5].Required || !reflect.DeepEqual(resolved.MCP.Args, []string{"--verbose", "--safe"}) {
		t.Fatalf("typed HCL values not restored at resolution: package=%#v mcp=%#v", resolved.Inputs, resolved.MCP)
	}
	if resolved.Inputs[1].Regex != "^[A-Z]+$" || resolved.MCP.Env["TOKEN"] != "ada-token" {
		t.Fatalf("evaluated regex/env values: inputs=%#v env=%#v", resolved.Inputs, resolved.MCP.Env)
	}
	if pkg.MCP.TokenHeader != `${ base64encode(inputs.token) }` {
		t.Fatal("resolving the copy mutated the catalog package")
	}
}

func TestRuntimeMissingInputExpressionReturnsHCLDiagnostic(t *testing.T) {
	dir := writeManifest(t, plainManifest+`[mcp]
name = "service"
[mcp.actions.authenticate]
command = ["helper", "${ inputs.missing }"]
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveExpressions(p, map[string]any{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unsupported attribute") || !strings.Contains(err.Error(), "mcp.actions.authenticate.command[1]") {
		t.Fatalf("missing runtime reference error = %v", err)
	}
}

func TestDynamicPluginPathUsesManifestDirectoryAfterEvaluation(t *testing.T) {
	dir := writeManifest(t, plainManifest+`[[inputs]]
name = "plugin_root"
type = "directory"
[[plugins]]
name = "config"
format = "claude-code"
source = "${ inputs.plugin_root }"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveExpressions(p, map[string]any{"plugin_root": "configs"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resolved.Plugins[0].Source, filepath.Join(dir, "configs"); got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestChainedInputDefaultsResolveAndCyclesKeepHCLCause(t *testing.T) {
	dir := writeManifest(t, plainManifest+`[[inputs]]
name = "third"
type = "string"
default = "${ inputs.second }-3"
[[inputs]]
name = "second"
type = "string"
default = "${ inputs.first }-2"
[[inputs]]
name = "first"
type = "string"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := ResolveInputDefinitions(p, map[string]any{"first": "one"})
	if err != nil {
		t.Fatal(err)
	}
	if defs[0].Default != "one-2-3" {
		t.Fatalf("chained input default = %#v", defs[0].Default)
	}

	cycle := writeManifest(t, plainManifest+`[[inputs]]
name = "first"
type = "string"
default = "${ inputs.second }"
[[inputs]]
name = "second"
type = "string"
default = "${ inputs.first }"
`)
	p, err = Load(cycle)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveInputDefinitions(p, map[string]any{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unsupported attribute") {
		t.Fatalf("cycle did not retain HCL cause: %v", err)
	}
}

func TestResolveInputDefinitionsDoesNotMutateManifestAcrossProfiles(t *testing.T) {
	dir := writeManifest(t, plainManifest+`[[inputs]]
name = "handle"
type = "string"
[[inputs]]
name = "greeting"
type = "string"
default = "${ inputs.handle }"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"inputs": []any{
		map[string]any{"name": "handle", "type": "string"},
		map[string]any{"name": "greeting", "type": "string", "default": "${ inputs.handle }"},
	}}
	for _, tc := range []struct{ input, want string }{{"Ada", "Ada"}, {"Grace", "Grace"}} {
		defs, err := ResolveInputDefinitions(p, map[string]any{"handle": tc.input})
		if err != nil {
			t.Fatal(err)
		}
		if defs[1].Default != tc.want {
			t.Fatalf("profile %s default = %#v, want %q", tc.input, defs[1].Default, tc.want)
		}
	}
	if !reflect.DeepEqual(p.RawManifest["inputs"], original["inputs"]) {
		t.Fatalf("profile resolution mutated raw manifest: %#v", p.RawManifest["inputs"])
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
	if p.MCP == nil || p.MCP.Name != "plain" || p.MCP.BindIPInput != "" || p.MCP.AdvertisedHostInput != "" || p.Inputs[0].Default != int64(8765) || p.Generator.TimeoutSeconds != 300 {
		t.Fatalf("bad bundle: %#v", p)
	}
}

func TestBundledDockerManifestsDeclareSeparateRuntimeHosts(t *testing.T) {
	for _, name := range []string{"cluster-inspector", "grafana-inspector", "azure-inspector"} {
		t.Run(name, func(t *testing.T) {
			pkg, err := Load(filepath.Join("..", "..", "packages", name))
			if err != nil {
				t.Fatal(err)
			}
			definitions := pkg.MCPDefinitions()
			if len(definitions) != 1 {
				t.Fatalf("expected one Docker MCP definition, got %d", len(definitions))
			}
			definition := definitions[0]
			if definition.BindIPInput == "" || definition.AdvertisedHostInput == "" {
				t.Fatalf("bundled runtime host inputs are not independently declared: %+v", definition)
			}
			inputs := map[string]Input{}
			for _, input := range pkg.Inputs {
				inputs[input.Name] = input
			}
			if inputs[definition.BindIPInput].Type != "string" || inputs[definition.BindIPInput].Default != "127.0.0.1" {
				t.Fatalf("bind input is not numeric loopback by default: %+v", inputs[definition.BindIPInput])
			}
			if inputs[definition.AdvertisedHostInput].Type != "string" || inputs[definition.AdvertisedHostInput].Default != "localhost" {
				t.Fatalf("advertised-host input does not default to localhost: %+v", inputs[definition.AdvertisedHostInput])
			}
		})
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

func TestMCPRuntimeHostBindingsUseDeclaredInputNames(t *testing.T) {
	manifest := plainManifest + `[[inputs]]
name = "docker_interface"
type = "string"
default = "127.0.0.1"

[[inputs]]
name = "client_dns_name"
type = "string"
default = "localhost"

[mcp]
name = "plain"
bind_ip_input = "docker_interface"
advertised_host_input = "client_dns_name"
`
	p, err := Load(writeManifest(t, manifest))
	if err != nil {
		t.Fatal(err)
	}
	if p.MCP.BindIPInput != "docker_interface" || p.MCP.AdvertisedHostInput != "client_dns_name" {
		t.Fatalf("runtime host bindings were not loaded: %#v", p.MCP)
	}
}

func TestMCPRuntimeHostBindingsRequireDeclaredStringInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    string
		declared string
	}{
		{name: "missing bind input", input: `bind_ip_input = "missing"`},
		{name: "missing advertised input", input: `advertised_host_input = "missing"`},
		{name: "non-string input", input: `bind_ip_input = "port"`, declared: "[[inputs]]\nname = \"port\"\ntype = \"integer\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := plainManifest + tc.declared + "[mcp]\nname = \"plain\"\n" + tc.input + "\n"
			if _, err := Load(writeManifest(t, manifest)); err == nil {
				t.Fatal("invalid runtime host input reference accepted")
			}
		})
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

func TestLoadManifestUIAndMCPList(t *testing.T) {
	p, err := Load(writeManifest(t, plainManifest+`[[inputs]]
name = "auth_mode"
type = "choice"
label = "Authentication"
[[inputs]]
name = "token"
type = "secret"
label = "Token"
hint = "Provide a credential"
visible_when = { auth_mode = "api_token" }
[ui]
[[ui.sections]]
id = "connection"
title = "Connection"
fields = ["auth_mode", "token"]
[[mcps]]
name = "primary"
runtime = "docker"
[[mcps]]
name = "secondary"
runtime = "host"
`))
	if err != nil {
		t.Fatal(err)
	}
	if p.UI == nil || len(p.UI.Sections) != 1 || p.UI.Sections[0].Fields[1] != "token" {
		t.Fatalf("UI metadata lost: %#v", p.UI)
	}
	if len(p.Inputs[1].VisibleWhen) != 1 || p.Inputs[1].VisibleWhen["auth_mode"] != "api_token" {
		t.Fatalf("condition metadata lost: %#v", p.Inputs[1].VisibleWhen)
	}
	if p.Inputs[1].Hint != "Provide a credential" {
		t.Fatalf("input hint metadata lost: %#v", p.Inputs[1])
	}
	if !p.HasMCP() || len(p.MCPDefinitions()) != 2 || p.MCPDefinitions()[1].Name != "secondary" {
		t.Fatalf("MCP list metadata lost: %#v", p.MCPDefinitions())
	}
}

func TestRejectInvalidManifestUIAndMCPList(t *testing.T) {
	for _, tc := range []struct{ label, body, message string }{
		{"unknown section field", `[ui]\n[[ui.sections]]\nid="main"\ntitle="Main"\nfields=["missing"]`, "unknown input"},
		{"unassigned field", `[ui]\n[[ui.sections]]\nid="main"\ntitle="Main"\nfields=["first"]`, "unassigned"},
		{"duplicate section id", `[ui]\n[[ui.sections]]\nid="main"\ntitle="Main"\n[[ui.sections]]\nid="main"\ntitle="Also main"`, "duplicate ui section"},
		{"field assigned twice", `[ui]\n[[ui.sections]]\nid="main"\ntitle="Main"\nfields=["first"]\n[[ui.sections]]\nid="extra"\ntitle="Extra"\nfields=["first"]`, "assigned more than once"},
		{"unknown condition controller", `[[inputs]]\nname="first"\ntype="string"\nvisible_when={ missing="yes" }`, "unknown controller"},
		{"nonscalar condition value", `[[inputs]]\nname="first"\ntype="string"\nvisible_when={ second=["one", "two"] }`, "scalar equality"},
		{"mixed MCP forms", `[mcp]\nname="legacy"\n[[mcps]]\nname="new"`, "cannot mix"},
		{"duplicate MCP names", `[[mcps]]\nname="same"\n[[mcps]]\nname="same"`, "duplicate mcp"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			body := strings.ReplaceAll(tc.body, `\n`, "\n")
			if strings.Contains(body, `fields=["first"]`) && !strings.Contains(body, `name="first"`) {
				body += "\n[[inputs]]\nname=\"first\"\ntype=\"string\""
			}
			if strings.Contains(body, `name="first"`) || strings.Contains(body, `second=[`) || strings.Contains(body, `fields=["first"]`) {
				body += "\n[[inputs]]\nname=\"second\"\ntype=\"string\""
			}
			errBody := plainManifest + body
			if _, err := Load(writeManifest(t, errBody)); err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.message) {
				t.Fatalf("expected %q error; got %v", tc.message, err)
			}
		})
	}
}

func TestLegacyMCPProvidesUniformDefinitions(t *testing.T) {
	p, err := Load(writeManifest(t, plainManifest+"[mcp]\nname=\"legacy\"\nruntime=\"docker\""))
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasMCP() || len(p.MCPDefinitions()) != 1 || p.MCPDefinitions()[0].Name != "legacy" {
		t.Fatalf("legacy MCP helper result: %#v", p.MCPDefinitions())
	}
}

func TestMCPListRejectsExplicitZeroTimeout(t *testing.T) {
	manifest := plainManifest + `[[mcps]]
name = "listed"
[mcps.actions.prepare]
command = ["prepare"]
timeout_seconds = 0
`
	if _, err := Load(writeManifest(t, manifest)); err == nil || !strings.Contains(err.Error(), "timeout_seconds must be positive") {
		t.Fatalf("explicit zero timeout accepted: %v", err)
	}
}

func TestMCPRegistrationNameInputMustReferenceVisibleStringInput(t *testing.T) {
	for _, tc := range []struct{ name, inputs, want string }{
		{"unknown reference", ``, `registration_name_input "registration" is not declared`},
		{"non-string reference", `[[inputs]]
name="registration"
type="integer"`, `registration_name_input "registration" must reference a string input`},
		{"conditional reference", `[[inputs]]
name="registration"
type="string"
visible_when={mode="yes"}
[[inputs]]
name="mode"
type="string"`, `registration_name_input "registration" must be unconditionally visible`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := plainManifest + tc.inputs + `
[[mcps]]
name="child"
runtime="docker"
registration_name_input="registration"
`
			_, err := Load(writeManifest(t, manifest))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
	manifest := plainManifest + `
[[inputs]]
name="registration"
type="string"
config_key="mcp.registration_name"
default="service-name"
[[mcps]]
name="child"
runtime="docker"
registration_name_input="registration"
`
	p, err := Load(writeManifest(t, manifest))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.MCPDefinitions()[0].RegistrationNameInput; got != "registration" {
		t.Fatalf("registration name input = %q", got)
	}
}

func TestInputVisibleRequiresEveryTypedEqualityCondition(t *testing.T) {
	input := Input{VisibleWhen: map[string]any{"mode": "advanced", "count": int64(2)}}
	if !InputVisible(input, map[string]any{"mode": "advanced", "count": 2.0}) {
		t.Fatal("numeric-equivalent values should satisfy all visibility conditions")
	}
	if InputVisible(input, map[string]any{"mode": "basic", "count": 2}) {
		t.Fatal("a failed condition should hide the input")
	}
	if InputVisible(Input{VisibleWhen: map[string]any{"mode": "2"}}, map[string]any{"mode": 2}) {
		t.Fatal("visibility equality must not coerce strings into numbers")
	}
}

func TestVisibleInputsPreservesOrderAndDoesNotMutateValues(t *testing.T) {
	defs := []Input{
		{Name: "mode"},
		{Name: "basic", VisibleWhen: map[string]any{"mode": "basic"}},
		{Name: "advanced", VisibleWhen: map[string]any{"mode": "advanced"}},
	}
	values := map[string]any{"mode": "basic", "advanced": "saved but hidden"}
	visible := VisibleInputs(defs, values)
	if len(visible) != 2 || visible[0].Name != "mode" || visible[1].Name != "basic" {
		t.Fatalf("visible inputs/order: %#v", visible)
	}
	if values["advanced"] != "saved but hidden" {
		t.Fatalf("filter mutated values and erased inactive data: %#v", values)
	}
}

func TestPublicManifestsDeclareSetupPresentation(t *testing.T) {
	for _, name := range []string{"cluster-inspector", "grafana-inspector", "azure-inspector", "git-provider", "git-repo-map"} {
		t.Run(name, func(t *testing.T) {
			p, err := Load(filepath.Join("..", "..", "packages", name))
			if err != nil {
				t.Fatal(err)
			}
			if p.UI == nil || len(p.UI.Sections) == 0 {
				t.Fatalf("%s has no declared UI sections", name)
			}
			if name == "cluster-inspector" || name == "grafana-inspector" {
				if p.MCP == nil || p.MCP.RegistrationNameInput != "registration_name" {
					t.Fatalf("%s registration name is not an editable input: %#v", name, p.MCP)
				}
				var registration *Input
				for i := range p.Inputs {
					if p.Inputs[i].Name == "registration_name" {
						registration = &p.Inputs[i]
					}
				}
				if registration == nil || registration.Type != "string" || registration.Label != "MCP registration name" || !strings.Contains(registration.Hint, "User-editable") || registration.Default != nil {
					t.Fatalf("%s registration name input metadata: %#v", name, registration)
				}
			}
			if name == "grafana-inspector" {
				conditions := map[string]any{}
				for _, in := range p.Inputs {
					if in.VisibleWhen != nil {
						conditions[in.Name] = in.VisibleWhen["auth_mode"]
					}
				}
				if conditions["token"] != "api_token" || conditions["grafana_session"] != "session_cookie" {
					t.Fatalf("Grafana auth visibility missing: %#v", conditions)
				}
			}
		})
	}
}

func TestStandaloneForgejoPackageWasReplaced(t *testing.T) {
	legacy := filepath.Join("..", "..", "packages", "forgejo")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("standalone Forgejo capability still exists at %s (stat error: %v)", legacy, err)
	}
	provider, err := Load(filepath.Join("..", "..", "packages", "git-provider"))
	if err != nil {
		t.Fatalf("replacement Git Provider capability is missing: %v", err)
	}
	if provider.Skill == nil || len(provider.MCPs) == 0 {
		t.Fatalf("Git Provider replacement must retain its skill and provider MCPs: %+v", provider)
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
