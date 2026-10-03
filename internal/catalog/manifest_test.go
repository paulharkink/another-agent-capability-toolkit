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
