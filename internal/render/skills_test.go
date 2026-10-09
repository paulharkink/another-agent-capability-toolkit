package render

import (
	"context"
	"encoding/json"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageSkillGeneratorChild(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "aact-stage-generator-child" {
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		var request map[string]any
		if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
			os.Exit(3)
		}
		input, _ := request["inputs"].(map[string]any)
		if input["fail"] == true {
			os.Stderr.WriteString("generator fixture deliberately failed")
			os.Exit(4)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"directory": cwd}); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	source := filepath.Join(root, "generated")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md.mustache"), []byte("{{generated.directory}}"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	skill := catalog.Skill{Name: "generated", Source: source, Templates: []catalog.Template{{Source: "SKILL.md.mustache", Destination: "SKILL.md"}}, Generator: &catalog.Command{Argv: []string{executable, "-test.run=^TestStageSkillGeneratorChild$", "aact-stage-generator-child"}}}
	stage, err := StageSkill(context.Background(), catalog.Package{Dir: root}, skill, nil, config.Profile{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	canonical, canonicalErr := filepath.EvalSymlinks(source)
	generatedDir := string(data)
	generatedInfo, generatedErr := os.Stat(generatedDir)
	sourceInfo, sourceErr := os.Stat(canonical)
	if err != nil || canonicalErr != nil || generatedErr != nil || sourceErr != nil || !os.SameFile(generatedInfo, sourceInfo) {
		t.Fatalf("generator used wrong skill working directory: %q (source %q; errors: read=%v resolve=%v generated=%v source=%v)", generatedDir, canonical, err, canonicalErr, generatedErr, sourceErr)
	}
	if _, err := StageSkill(context.Background(), catalog.Package{Dir: root}, skill, map[string]any{"fail": true}, config.Profile{}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "deliberately failed") {
		t.Fatalf("generator failure not explained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "SKILL.md")); err != nil {
		t.Fatal("generator failure removed another stage")
	}
}

func TestStageSkillMultipleIndependentTemplateRoots(t *testing.T) {
	root := t.TempDir()
	parent := t.TempDir()
	pkg := catalog.Package{ID: "guidance", Dir: root}
	outputs := []string{}
	for _, name := range []string{"review", "release"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md.mustache"), []byte(name+" {{#inputs.enabled}}yes{{/inputs.enabled}}{{^inputs.enabled}}no{{/inputs.enabled}}"), 0600); err != nil {
			t.Fatal(err)
		}
		skill := catalog.Skill{Name: name, Source: dir, Templates: []catalog.Template{{Source: "SKILL.md.mustache", Destination: "SKILL.md"}}}
		stage, err := StageSkill(context.Background(), pkg, skill, map[string]any{"enabled": false}, config.Profile{Ref: config.ProfileRef{Name: "ota"}}, parent)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
		if err != nil || string(data) != name+" no" {
			t.Fatalf("template context/root: %s %v", data, err)
		}
		outputs = append(outputs, stage)
	}
	if outputs[0] == outputs[1] {
		t.Fatal("skills share staging directory")
	}
	if _, err := StageSkill(context.Background(), pkg, catalog.Skill{Name: "broken", Source: filepath.Join(root, "absent")}, nil, config.Profile{}, parent); err == nil {
		t.Fatal("missing skill unexpectedly staged")
	}
	if _, err := os.Stat(filepath.Join(outputs[0], "SKILL.md")); err != nil {
		t.Fatal("another skill's failure removed successful stage")
	}
}

func TestStagePluginPreservesNativeManifestAndRejectsEscape(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "native")
	if err := os.MkdirAll(filepath.Join(src, ".claude-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".claude-plugin", "plugin.json"), []byte(`{"name":"guidance"}`), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := catalog.Package{ID: "guidance", Dir: root}
	stage, err := StagePlugin(context.Background(), pkg, catalog.Plugin{Name: "native", Source: src, Format: "claude-code"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stage, ".claude-plugin", "plugin.json"))
	if err != nil || string(data) != `{"name":"guidance"}` {
		t.Fatalf("native manifest lost: %s %v", data, err)
	}
	if _, err := StagePlugin(context.Background(), pkg, catalog.Plugin{Name: "escape", Source: t.TempDir()}, t.TempDir()); err == nil {
		t.Fatal("plugin source escaped capability root")
	}
}
