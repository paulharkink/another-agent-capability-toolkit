package packages_test

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type publicManifest struct {
	SchemaVersion int    `toml:"schema_version"`
	ID            string `toml:"id"`
	Name          string `toml:"name"`
	Skill         struct {
		Name  string
		Files []string
	} `toml:"skill"`
	Templates []struct{ Source, Destination string } `toml:"templates"`
	Inputs    []struct {
		Name, Type, Label string
		Default           any
	} `toml:"inputs"`
	Generator any `toml:"generator"`
	MCP       *struct {
		Runtime       string
		Transport     string
		BuildContext  string `toml:"build_context"`
		Image         string
		ContainerPort int    `toml:"container_port"`
		EndpointPath  string `toml:"endpoint_path"`
		HostPortInput string `toml:"host_port_input"`
		Actions       map[string]struct {
			Command []string
			Windows struct{ Command []string }
		}
	} `toml:"mcp"`
}

func TestClusterKubeconfigInputIdentifiesImportSource(t *testing.T) {
	p, _ := loadPublic(t, "cluster-inspector")
	for _, input := range p.Inputs {
		if input.Name == "kubeconfig" {
			if input.Label != "Source kubeconfig" {
				t.Fatalf("kubeconfig input must identify the import source, got %q", input.Label)
			}
			return
		}
	}
	t.Fatal("kubeconfig input missing")
}

func TestClusterInspectorDatabaseInputUsesTargetChoices(t *testing.T) {
	p, err := catalog.Load(filepath.Join("..", "..", "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range p.Inputs {
		if input.Name != "connections" {
			continue
		}
		if input.Type != "multichoice" || input.OptionsFrom != "dbms.*.tenants.*" || input.Label != "Read-only database queries (optional)" {
			t.Fatalf("database selection is not a target-backed checkbox list: %+v", input)
		}
		return
	}
	t.Fatal("connections input missing")
}

func TestClusterInspectorRejectsBothCredentialMethods(t *testing.T) {
	p, err := catalog.Load(filepath.Join("..", "..", "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"registration_name": "cluster-inspector-test", "api_server": "https://cluster.example", "token": "private", "kubeconfig": "/tmp/config"}
	if err := forms.Validate(p.Inputs, values); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("public package accepted incompatible credentials: %v", err)
	}
}

func loadPublic(t *testing.T, name string) (publicManifest, string) {
	t.Helper()
	dir := filepath.Join("..", "..", "packages", name)
	b, err := os.ReadFile(filepath.Join(dir, "package.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var p publicManifest
	if err = toml.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != name || p.SchemaVersion != 1 || p.Skill.Name != name {
		t.Fatalf("invalid package identity: %+v", p)
	}
	return p, dir
}

func TestInspectorPublicRuntimeContracts(t *testing.T) {
	for _, tc := range []struct {
		name, transport, path string
		port                  int
	}{
		{"cluster-inspector", "streamable-http", "/mcp", 8765},
		{"grafana-inspector", "streamable-http", "/mcp", 8765},
		{"azure-inspector", "sse", "/sse", 8084},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, dir := loadPublic(t, tc.name)
			if p.MCP == nil {
				t.Fatal("missing MCP component")
			}
			m := p.MCP
			if m.Runtime != "docker" || m.Transport != tc.transport || m.EndpointPath != tc.path || m.ContainerPort != tc.port {
				t.Fatalf("changed runtime: %+v", m)
			}
			for _, action := range []string{"prepare", "authenticate"} {
				a, ok := m.Actions[action]
				if !ok {
					t.Fatalf("missing %s", action)
				}
				if len(a.Command) != 3 || a.Command[0] != "bin/inspector-helper" || a.Command[1] != tc.name || a.Command[2] != action {
					t.Fatalf("nonportable action: %+v", a)
				}
				if len(a.Windows.Command) != 3 || a.Windows.Command[0] != "bin/inspector-helper.exe" {
					t.Fatalf("missing Windows action: %+v", a)
				}
			}
			found := false
			for _, in := range p.Inputs {
				if in.Name == m.HostPortInput {
					found = true
					if in.Type != "integer" || in.Default != int64(tc.port) {
						t.Fatalf("port default: %+v", in)
					}
				}
			}
			if !found {
				t.Fatal("missing port form input")
			}
			if m.BuildContext != "" {
				if _, err := os.Stat(filepath.Join(dir, m.BuildContext, "Dockerfile")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPlainSkillNoGenerator(t *testing.T) {
	p, dir := loadPublic(t, "non-interactive-ready-planning")
	if p.Generator != nil || len(p.Templates) != 0 || len(p.Inputs) != 0 || p.MCP != nil {
		t.Fatal("plain skill unexpectedly generates content")
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestFindSessionReleaseResources(t *testing.T) {
	p, dir := loadPublic(t, "find-session")
	if p.Generator != nil || p.MCP != nil {
		t.Fatal("session tool must be a plain skill")
	}
	for _, path := range []string{"SKILL.md", "container/Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"bin/find-session", "bin/find-session.exe"} {
		if !strings.Contains(string(b), binary) {
			t.Fatalf("missing native launcher documentation %s", binary)
		}
	}
}

func TestGitProviderInspectorDeclaresOptionalProviderServers(t *testing.T) {
	p, err := catalog.Load(filepath.Join("..", "..", "packages", "git-provider"))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "git-provider" || p.Name != "Git Provider Inspector" || p.Skill.Name != "git-provider" {
		t.Fatalf("unexpected package identity: id=%q name=%q skill=%q", p.ID, p.Name, p.Skill.Name)
	}
	profiles := p.MCPProfiles()
	if len(profiles) != 3 {
		t.Fatalf("want three provider MCP profiles, got %d", len(profiles))
	}
	want := []string{"github", "gitlab", "bitbucket"}
	for i, profile := range profiles {
		if profile.Name != want[i] {
			t.Fatalf("profile %d = %q, want %q", i, profile.Name, want[i])
		}
		if profile.EnabledInput == "" || profile.RegistrationNameInput == "" {
			t.Fatalf("%s must be independently optional and have an editable registration name: %+v", profile.Name, profile)
		}
		if profile.Runtime != "docker" {
			t.Fatalf("%s should launch as a local Docker MCP server, got runtime %q", profile.Name, profile.Runtime)
		}
		if profile.TokenInput == "" || profile.TokenFileInput == "" || profile.TokenEnvInput == "" {
			t.Fatalf("%s must accept a raw token, token file, or environment variable", profile.Name)
		}
		for _, inputName := range []string{profile.TokenInput, profile.TokenFileInput, profile.TokenEnvInput} {
			input, ok := findInput(p.Inputs, inputName)
			if !ok {
				t.Fatalf("%s refers to missing token input %q", profile.Name, inputName)
			}
			if input.Default != nil && input.Default != "" {
				t.Fatalf("%s token input %q must not contain a default secret", profile.Name, inputName)
			}
		}
	}
	bitbucketURL, ok := findInput(p.Inputs, "bitbucket_url")
	if !ok || bitbucketURL.Default != nil && bitbucketURL.Default != "" {
		t.Fatalf("Bitbucket URL must be configurable without a package-wide default: %+v", bitbucketURL)
	}
}

func findInput(inputs []catalog.Input, name string) (catalog.Input, bool) {
	for _, input := range inputs {
		if input.Name == name {
			return input, true
		}
	}
	return catalog.Input{}, false
}

func TestAuthenticationInputsAreSecret(t *testing.T) {
	for _, name := range []string{"cluster-inspector", "grafana-inspector", "git-provider"} {
		p, _ := loadPublic(t, name)
		found := false
		for _, in := range p.Inputs {
			if in.Name == "token" || strings.HasSuffix(in.Name, "_token") || in.Name == "grafana_session" || in.Name == "oauth_refresh" {
				found = true
				if in.Type != "secret" {
					t.Errorf("%s %s exposes credentials as %s", name, in.Name, in.Type)
				}
			}
		}
		if !found {
			t.Errorf("%s missing auth input", name)
		}
	}
}

func TestStrictShippedPublicCatalog(t *testing.T) {
	for _, name := range []string{"cluster-inspector", "grafana-inspector", "azure-inspector", "git-provider", "find-session", "non-interactive-ready-planning"} {
		t.Run(name, func(t *testing.T) {
			if _, err := catalog.Load(filepath.Join("..", "..", "packages", name)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
