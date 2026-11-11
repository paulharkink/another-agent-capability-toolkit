package cli

import (
	"bytes"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func namedProfileFixture(t *testing.T) (string, string, string) {
	cfg, st, home := cliFixture(t)
	dir := filepath.Join(filepath.Dir(cfg), "environments", "demo")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "ota.toml"), []byte("[inputs]\nlabel='company'\n"), 0600)
	return cfg, st, home
}
func TestCLIProfileInstallAndInference(t *testing.T) {
	cfg, st, home := namedProfileFixture(t)
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"install", "demo", "--config", cfg, "--state-dir", st, "--instance", "ota", "--agent", "all", "--agent-home", home, "--json"}, nil, &out, &errout)
	if code != 0 {
		t.Fatalf("%d %s", code, errout.String())
	}
	b, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"))
	if err != nil || string(b) != "Hello company" {
		t.Fatalf("%s %v", b, err)
	}
	out.Reset()
	errout.Reset()
	code = Run(context.Background(), []string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "all", "--agent-home", home}, nil, &out, &errout)
	if code != 0 {
		t.Fatalf("inference %d %s", code, errout.String())
	}
}

func TestCLIInstallKeepsLegacyMCPSelectionAfterAddingLinkedSkill(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "atlassian")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version = 1\nid = \"atlassian\"\nname = \"Atlassian\"\n[skill]\nname = \"atlassian-confluence\"\n[mcp]\nname = \"atlassian\"\nexternal_url = \"https://fixture.invalid/mcp\"\ntransport = \"streamable-http\"\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "package.toml"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "SKILL.md"), []byte("---\nname: atlassian-confluence\ndescription: fixture\n---\nUse Atlassian MCP.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "aact.toml")
	if err := os.WriteFile(cfg, []byte("schema_version = 1\nsource_id = \"fixture\"\n[[catalog]]\nid = \"atlassian\"\nsource = \"./atlassian\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	store, err := state.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.ResolveInstanceKey("fixture", "atlassian", "atlassian-dso")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordInstance(state.ProfileRecord{
		Key: key, Name: "atlassian-dso", Local: true,
		Selection: &state.ProfileSelection{ItemIDs: []string{"mcp:atlassian"}, DestinationIDs: []string{"generic"}},
	}); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "agent-home")
	var out, errout bytes.Buffer
	args := []string{"install", "atlassian", "--config", cfg, "--state-dir", stateDir, "--instance", "atlassian-dso", "--agent", "generic", "--agent-home", home, "--skills-only"}
	if code := Run(context.Background(), args, nil, &out, &errout); code != 0 {
		t.Fatalf("install without --item failed (%d): %s", code, errout.String())
	}
	installed, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "atlassian-confluence", "SKILL.md"))
	if err != nil || !strings.Contains(string(installed), "Use Atlassian MCP.") {
		t.Fatalf("installed skill = %q, %v", installed, err)
	}
	records, err := store.Instances()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Key == key && record.Selection != nil {
			if len(record.Selection.ItemIDs) != 1 || record.Selection.ItemIDs[0] != "set:atlassian-confluence" {
				t.Fatalf("CLI did not persist normalized selection: %v", record.Selection.ItemIDs)
			}
			if len(record.Selection.DestinationIDs) != 1 || record.Selection.DestinationIDs[0] != "generic" {
				t.Fatalf("CLI changed selected agent destinations: %v", record.Selection.DestinationIDs)
			}
			return
		}
	}
	t.Fatal("CLI install removed the saved instance selection")
}

func TestCLIInstanceChoicesAndLegacyEnvironmentSelectors(t *testing.T) {
	cfg, st, home := namedProfileFixture(t)
	os.WriteFile(filepath.Join(filepath.Dir(cfg), "environments", "demo", "prod.toml"), []byte("[inputs]\nlabel='prod'"), 0600)
	for _, extra := range [][]string{nil, {"--environment", "home", "--target", "ota"}} {
		var out, errout bytes.Buffer
		args := append([]string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "all", "--agent-home", home}, extra...)
		code := Run(context.Background(), args, nil, &out, &errout)
		if code != 2 || !strings.Contains(errout.String(), "instance") {
			t.Fatalf("%d %s", code, errout.String())
		}
		if extra == nil && (!strings.Contains(errout.String(), "ota") || !strings.Contains(errout.String(), "prod")) {
			t.Fatal(errout.String())
		}
	}
}
func TestCLIProfileRegexBooleanAndSettings(t *testing.T) {
	cfg, st, home := namedProfileFixture(t)
	p := filepath.Join(filepath.Dir(cfg), "demo", "package.toml")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append(b, []byte("[[inputs]]\nname='enabled'\ntype='boolean'\nrequired=true\n[[inputs]]\nname='address'\ntype='string'\nregex='^ok$'\n")...), 0600)
	var out, errout bytes.Buffer
	args := []string{"install", "demo", "--config", cfg, "--state-dir", st, "--instance", "ota", "--agent", "all", "--agent-home", home, "--set", "enabled=false", "--set", "address=bad"}
	code := Run(context.Background(), args, nil, &out, &errout)
	if code != 2 || !strings.Contains(errout.String(), "address") || !strings.Contains(errout.String(), "bad") {
		t.Fatalf("%d %s", code, errout.String())
	}
	args[len(args)-1] = "address=ok"
	errout.Reset()
	if code = Run(context.Background(), args, nil, &out, &errout); code != 0 {
		t.Fatalf("%d %s", code, errout.String())
	}
	s, _ := state.Open(st)
	key, _ := s.ResolveProfileKey("fixture", "demo", "ota")
	values, _ := s.Answers(key)
	if values["enabled"] != false {
		t.Fatal(values)
	}
	out.Reset()
	Run(context.Background(), []string{"settings", "--config", cfg, "--state-dir", st}, nil, &out, &errout)
	if !strings.Contains(out.String(), "capability-pack") || strings.Contains(out.String(), `"checkout"`) {
		t.Fatal(out.String())
	}
}
func TestParseProfileItems(t *testing.T) {
	f, err := parse([]string{"install", "guide", "--instance", "ota", "--mcp", "server", "--item", "skill:one", "--item", "skill:two", "--instance-directory", "/instances"})
	if err != nil || f.instance != "ota" || f.mcp != "server" || len(f.items) != 2 || f.envroot != "/instances" {
		t.Fatalf("%+v %v", f, err)
	}
}
