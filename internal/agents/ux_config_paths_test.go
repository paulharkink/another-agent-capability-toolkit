package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUXResolveOpenCodeWritePathMatchesAdapter(t *testing.T) {
	for _, mode := range []string{"none", "json", "jsonc", "both"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			base := filepath.Join(home, ".config", "opencode", "opencode")
			jsonPath, jsoncPath := base+".json", base+".jsonc"
			if mode == "json" || mode == "both" {
				if err := os.MkdirAll(filepath.Dir(jsonPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(jsonPath, []byte(`{"theme":"json","mcp":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "jsonc" || mode == "both" {
				if err := os.MkdirAll(filepath.Dir(jsoncPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(jsoncPath, []byte(`{"theme":"jsonc","mcp":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env, err := ResolveEnvironment("opencode", "opencode", home)
			if err != nil {
				t.Fatal(err)
			}
			planned, err := ResolveConfigWritePath(env)
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := For("opencode", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := adapter.Register(context.Background(), env, Registration{Name: "ux-fixture", URL: "http://127.0.0.1:8765/mcp", TimeoutMS: 30000}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(planned); err != nil {
				t.Fatalf("planned adapter output %q missing: %v", planned, err)
			}
			for _, path := range []string{jsonPath, jsoncPath} {
				contents, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if filepath.Clean(path) == filepath.Clean(planned) {
					if !strings.Contains(string(contents), "ux-fixture") {
						t.Fatalf("planned file lacks registration: %s", contents)
					}
					if mode == "both" && !strings.Contains(string(contents), `"theme":"jsonc"`) {
						t.Fatalf("effective JSONC unrelated field lost: %s", contents)
					}
				} else if strings.Contains(string(contents), "ux-fixture") {
					t.Fatalf("adapter changed unplanned sibling %s: %s", path, contents)
				}
			}
		})
	}
}
