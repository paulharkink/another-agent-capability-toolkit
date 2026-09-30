package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cbroglie/mustache"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

// Escaped Mustache variables corrupt Markdown paths containing ampersands or
// angle brackets. Inverted sections must explain an empty inventory.
func TestMustacheGoldenRendering(t *testing.T) {
	template, err := os.ReadFile(filepath.Join("..", "..", "SKILL.md.mustache"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		rows []Repository
	}{
		{"populated", []Repository{{"git.example", "team/widget", "/work/research & development/<widget>"}}},
		{"empty", []Repository{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(result{tc.rows})
			if err != nil {
				t.Fatal(err)
			}
			var generated map[string]any
			if err := json.Unmarshal(encoded, &generated); err != nil {
				t.Fatal(err)
			}
			actual, err := mustache.Render(string(template), map[string]any{"inputs": map[string]any{"scan_roots": []string{"/work/research & development"}, "hosts": []string{}}, "generated": generated})
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", tc.name+".golden.md"))
			if err != nil {
				t.Fatal(err)
			}
			if actual != string(want) {
				t.Fatalf("rendered skill differs from golden:\n%s", actual)
			}
		})
	}
}

func TestPackageCatalogContract(t *testing.T) {
	pkg, err := catalog.Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Skill == nil || pkg.Generator == nil || pkg.Generator.Windows == nil || len(pkg.Templates) != 1 {
		t.Fatalf("incomplete catalog package: %#v", pkg)
	}
	// This exercises the manager's strict manifest reader, including typed form
	// metadata and both native command variants.
	if len(pkg.Inputs) != 2 {
		t.Fatalf("expected root and host form inputs: %#v", pkg.Inputs)
	}
	roots := pkg.Inputs[0]
	if roots.Type != "directory" || !roots.Multiple || !roots.Required || roots.MinItems == nil || *roots.MinItems != 1 {
		t.Fatalf("invalid root collection: %#v", roots)
	}
	if pkg.Inputs[1].Type != "string" || !pkg.Inputs[1].Multiple || pkg.Inputs[1].Required {
		t.Fatalf("invalid optional host collection: %#v", pkg.Inputs[1])
	}
}
