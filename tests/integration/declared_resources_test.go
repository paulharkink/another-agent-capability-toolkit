package integration_test

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Check the release build declarations rather than assuming checkout-only helpers
// will be present in the published archive. Task 17 checks built archives too.
func TestDeclaredResourcesHaveReleaseBuilds(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), "_") {
			continue
		}
		p, err := catalog.Load(filepath.Join(root, "packages", d.Name()))
		if err != nil {
			t.Fatal(err)
		}
		commands := []catalog.Command{}
		if p.Generator != nil {
			commands = append(commands, *p.Generator)
		}
		for _, m := range p.MCPDefinitions() {
			for _, c := range m.Actions {
				commands = append(commands, c)
			}
		}
		for _, c := range commands {
			variants := []catalog.Command{c}
			if c.Windows != nil {
				variants = append(variants, *c.Windows)
			}
			for _, v := range variants {
				if len(v.Argv) == 0 || !strings.HasPrefix(v.Argv[0], "bin/") {
					continue
				}
				name := strings.TrimSuffix(v.Argv[0], ".exe")
				needle := "binary: packages/" + d.Name() + "/" + name
				if !strings.Contains(string(b), needle) {
					t.Errorf("%s not declared for release", needle)
				}
			}
		}
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-deps", "./cmd/aact")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if strings.Contains(string(out), "/internal/packagehelpers") {
		t.Fatal("capability helpers linked into AACT")
	}
}
