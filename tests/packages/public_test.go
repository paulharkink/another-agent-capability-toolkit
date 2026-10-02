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

func TestClusterInspectorRejectsBothCredentialMethods(t *testing.T) {
	p, err := catalog.Load(filepath.Join("..", "..", "packages", "cluster-inspector"))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"api_server": "https://cluster.example", "token": "private", "kubeconfig": "/tmp/config"}
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
		{"forgejo", "streamable-http", "/mcp", 8080},
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

func TestAuthenticationInputsAreSecret(t *testing.T) {
	for _, name := range []string{"cluster-inspector", "grafana-inspector", "forgejo"} {
		p, _ := loadPublic(t, name)
		found := false
		for _, in := range p.Inputs {
			if in.Name == "token" || in.Name == "grafana_session" || in.Name == "oauth_refresh" {
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
	for _, name := range []string{"cluster-inspector", "grafana-inspector", "azure-inspector", "forgejo", "find-session", "non-interactive-ready-planning"} {
		t.Run(name, func(t *testing.T) {
			if _, err := catalog.Load(filepath.Join("..", "..", "packages", name)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
