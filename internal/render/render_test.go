package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixtureExecutor struct {
	out      []byte
	err      error
	args     []string
	cwd      string
	input    []byte
	deadline bool
}

func (f *fixtureExecutor) Run(ctx context.Context, a []string, c string, in []byte, e map[string]string, cb func([]byte)) ([]byte, error) {
	f.args = a
	f.cwd = c
	f.input = in
	_, f.deadline = ctx.Deadline()
	if cb != nil {
		cb([]byte("progress\n"))
	}
	return f.out, f.err
}
func TestGeneratorReceivesTypedStdin(t *testing.T) {
	f := &fixtureExecutor{out: []byte(`{"items":[1,true]}`)}
	dir := t.TempDir()
	g := Generator{Executor: f}
	p := catalog.Package{Dir: dir, Generator: &catalog.Command{Argv: []string{"bin/helper", "arg"}, TimeoutSeconds: 1}}
	out, err := g.Generate(context.Background(), p, map[string]any{"roots": []string{"a", "b"}, "enabled": true}, config.Target{Name: "target"}, "stage")
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err = json.Unmarshal(f.input, &req); err != nil {
		t.Fatal(err)
	}
	if req["protocol_version"] != float64(1) || req["inputs"].(map[string]any)["enabled"] != true || !f.deadline || f.args[0] != filepath.Join(dir, "bin/helper") || f.cwd != dir || out["items"] == nil {
		t.Fatalf("bad request=%#v argv=%#v output=%#v", req, f.args, out)
	}
}
func TestWindowsCommandVariant(t *testing.T) {
	f := &fixtureExecutor{out: []byte(`{}`)}
	g := Generator{Executor: f, GOOS: "windows"}
	p := catalog.Package{Dir: t.TempDir(), Generator: &catalog.Command{Argv: []string{"bin/helper"}, Windows: &catalog.Command{Argv: []string{"bin/helper.exe"}}}}
	_, err := g.Generate(context.Background(), p, nil, config.Target{}, "")
	if err != nil || len(f.args) == 0 || !strings.HasSuffix(f.args[0], "helper.exe") {
		t.Fatalf("%v %v", f.args, err)
	}
}
func TestGeneratorRejectsSecondJSONValue(t *testing.T) {
	for _, out := range []string{`{} {}`, `[]`, `null`, `{`, `{} garbage`} {
		g := Generator{Executor: &fixtureExecutor{out: []byte(out)}}
		_, err := g.Generate(context.Background(), catalog.Package{Dir: t.TempDir(), Generator: &catalog.Command{Argv: []string{"helper"}}}, nil, config.Target{}, "")
		if err == nil {
			t.Errorf("accepted %q", out)
		}
	}
}
func TestGeneratorFailuresAndOutputLimit(t *testing.T) {
	for _, f := range []*fixtureExecutor{{err: errors.New("exit 1")}, {out: bytes.Repeat([]byte("x"), process.MaxStdout+1)}} {
		g := Generator{Executor: f}
		_, err := g.Generate(context.Background(), catalog.Package{Dir: t.TempDir(), Generator: &catalog.Command{Argv: []string{"helper"}}}, nil, config.Target{}, "")
		if err == nil {
			t.Fatal("missing generator failure")
		}
	}
}
func packageFixture(t *testing.T, template string) catalog.Package {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "SKILL.md.mustache"), []byte(template), 0644); err != nil {
		t.Fatal(err)
	}
	return catalog.Package{Dir: d, Skill: &catalog.Skill{Name: "sample"}, Templates: []catalog.Template{{Source: "SKILL.md.mustache", Destination: "SKILL.md"}}}
}
func TestTemplateOnlyUsesInputsWithoutGenerator(t *testing.T) {
	p := packageFixture(t, "---\nname: sample\n---\n{{#inputs.enabled}}Roots:{{#inputs.roots}}\n- {{{.}}}{{/inputs.roots}}{{/inputs.enabled}}\n{{^generated.items}}No generated items.{{/generated.items}}\n")
	stage, err := Stage(context.Background(), p, map[string]any{"enabled": true, "roots": []string{"a&b", "c"}}, config.Target{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if string(b) != "---\nname: sample\n---\nRoots:\n- a&b\n- c\nNo generated items.\n" {
		t.Fatalf("got %q", b)
	}
}
func TestComputedDataIsNamespaced(t *testing.T) {
	p := packageFixture(t, "{{inputs.name}}/{{generated.name}}/{{name}}")
	p.Generator = &catalog.Command{Argv: []string{"helper"}}
	r := Renderer{Generator: &Generator{Executor: &fixtureExecutor{out: []byte(`{"name":"computed"}`)}}}
	stage, err := r.Stage(context.Background(), p, map[string]any{"name": "input"}, config.Target{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if string(b) != "input/computed/" {
		t.Fatalf("got %q", b)
	}
}
func TestPlainSupportingFilesCopied(t *testing.T) {
	p := packageFixture(t, "skill")
	os.Mkdir(filepath.Join(p.Dir, "scripts"), 0755)
	os.WriteFile(filepath.Join(p.Dir, "scripts", "helper"), []byte("helper"), 0755)
	p.Skill.Files = []string{"scripts"}
	stage, err := Stage(context.Background(), p, nil, config.Target{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(stage, "scripts", "helper"))
	if err != nil || string(b) != "helper" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err = os.Stat(filepath.Join(stage, "SKILL.md.mustache")); !os.IsNotExist(err) {
		t.Fatal("template leaked")
	}
}
func TestTemplateDestinationEscapeRejected(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:\\escape", "..\\escape"} {
		p := packageFixture(t, "skill")
		p.Templates[0].Destination = name
		if _, err := Stage(context.Background(), p, nil, config.Target{}, t.TempDir()); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
func TestSourceSymlinkEscapeRejected(t *testing.T) {
	p := packageFixture(t, "skill")
	outside := filepath.Join(t.TempDir(), "private")
	os.WriteFile(outside, []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(p.Dir, "escape")); err != nil {
		t.Skip(err)
	}
	p.Skill.Files = []string{"escape"}
	if _, err := Stage(context.Background(), p, nil, config.Target{}, t.TempDir()); err == nil {
		t.Fatal("copied outside package")
	}
}
func TestFailedGenerateKeepsPreviousFiles(t *testing.T) {
	p := packageFixture(t, "new")
	p.Generator = &catalog.Command{Argv: []string{"helper"}}
	parent := t.TempDir()
	old := filepath.Join(parent, "published")
	os.Mkdir(old, 0755)
	os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("old"), 0644)
	r := Renderer{Generator: &Generator{Executor: &fixtureExecutor{err: errors.New("fail")}}}
	_, err := r.Stage(context.Background(), p, nil, config.Target{}, parent)
	if err == nil {
		t.Fatal("generation succeeded")
	}
	b, _ := os.ReadFile(filepath.Join(old, "SKILL.md"))
	if string(b) != "old" {
		t.Fatal("previous files changed")
	}
}
func TestPublishReplacesCompleteDirectory(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "stage"), filepath.Join(root, "dest")
	os.Mkdir(a, 0755)
	os.Mkdir(b, 0755)
	os.WriteFile(filepath.Join(a, "SKILL.md"), []byte("new"), 0644)
	os.WriteFile(filepath.Join(b, "old"), []byte("old"), 0644)
	if err := Publish(a, b); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(b, "SKILL.md"))
	if string(got) != "new" {
		t.Fatal("not published")
	}
	if _, err := os.Stat(filepath.Join(b, "old")); !os.IsNotExist(err) {
		t.Fatal("stale file")
	}
}
func TestWindowsReplacementRollback(t *testing.T) {
	root := t.TempDir()
	stage, dest := filepath.Join(root, "missing"), filepath.Join(root, "dest")
	os.Mkdir(dest, 0755)
	os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("old"), 0644)
	if err := Publish(stage, dest); err == nil {
		t.Fatal("missing stage accepted")
	}
	got, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if string(got) != "old" {
		t.Fatal("previous output lost")
	}
}

var _ = reflect.DeepEqual
var _ = time.Second

func TestFailedStageRemovesTemporaryDirectory(t *testing.T) {
	p := packageFixture(t, "skill")
	p.Templates[0].Destination = "../outside"
	parent := t.TempDir()
	if _, err := Stage(context.Background(), p, nil, config.Target{}, parent); err == nil {
		t.Fatal("expected failure")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatalf("failed staging leaked %v", entries)
	}
}

func TestPublishRollbackAfterSecondRenameFailure(t *testing.T) {
	root := t.TempDir()
	stage, dest := filepath.Join(root, "stage"), filepath.Join(root, "dest")
	os.Mkdir(stage, 0755)
	os.Mkdir(dest, 0755)
	os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte("new"), 0644)
	os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("old"), 0644)
	rename := func(a, b string) error {
		if a == stage {
			return errors.New("Windows sharing violation")
		}
		return os.Rename(a, b)
	}
	if err := publishWithRename(stage, dest, rename); err == nil {
		t.Fatal("rename failure hidden")
	}
	b, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if string(b) != "old" {
		t.Fatal("old output not restored")
	}
}

type symlinkGenerator struct{ outside string }

func (f symlinkGenerator) Run(ctx context.Context, args []string, cwd string, in []byte, env map[string]string, cb func([]byte)) ([]byte, error) {
	var req struct {
		Context struct {
			Staging string `json:"staging_dir"`
		} `json:"context"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	return []byte(`{}`), os.Symlink(f.outside, filepath.Join(req.Context.Staging, "docs"))
}
func TestGeneratorResourceEscapeRejected(t *testing.T) {
	outside := t.TempDir()
	p := packageFixture(t, "skill")
	p.Templates = append(p.Templates, catalog.Template{Source: "SKILL.md.mustache", Destination: "docs/outside.md"})
	p.Generator = &catalog.Command{Argv: []string{"helper"}}
	r := Renderer{Generator: &Generator{Executor: symlinkGenerator{outside}}}
	if _, err := r.Stage(context.Background(), p, nil, config.Target{}, t.TempDir()); err == nil {
		t.Fatal("generator symlink escaped staging")
	}
	if _, err := os.Stat(filepath.Join(outside, "outside.md")); !os.IsNotExist(err) {
		t.Fatal("outside resource changed")
	}
}
