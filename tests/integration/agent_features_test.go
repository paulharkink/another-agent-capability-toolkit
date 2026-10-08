package integration_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAgentPolicyLivesInAdapters(t *testing.T) {
	forbidden := map[string]bool{"codex": true, "opencode": true, "claude": true, "claude-code": true, "copilot": true, "copilot-cli": true, "copilot-intellij": true, "intellij": true, "hermes": true, "claude-desktop": true, "opencode-desktop": true, "kubeconfig": true}
	for _, dir := range []string{"internal/app", "internal/cli", "internal/tui"} {
		root := filepath.Join("..", "..", dir)
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, e.Name())
			fs := token.NewFileSet()
			f, err := parser.ParseFile(fs, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					value, _ := strconv.Unquote(lit.Value)
					if forbidden[value] {
						t.Errorf("%s: agent/capability-specific value %q outside adapter/package", fs.Position(lit.Pos()), value)
					}
					if strings.HasSuffix(value, "/internal/install") {
						t.Errorf("%s: core directly installs agent skills", fs.Position(lit.Pos()))
					}
				}
				return true
			})
		}
	}
}
