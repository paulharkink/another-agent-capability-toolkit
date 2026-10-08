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
	code := Run(context.Background(), []string{"install", "demo", "--config", cfg, "--state-dir", st, "--profile", "ota", "--agent", "all", "--agent-home", home, "--json"}, nil, &out, &errout)
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
func TestCLIProfileChoicesAndLegacySelectors(t *testing.T) {
	cfg, st, home := namedProfileFixture(t)
	os.WriteFile(filepath.Join(filepath.Dir(cfg), "environments", "demo", "prod.toml"), []byte("[inputs]\nlabel='prod'"), 0600)
	for _, extra := range [][]string{nil, {"--environment", "home", "--target", "ota"}} {
		var out, errout bytes.Buffer
		args := append([]string{"install", "demo", "--config", cfg, "--state-dir", st, "--agent", "all", "--agent-home", home}, extra...)
		code := Run(context.Background(), args, nil, &out, &errout)
		if code != 2 || !strings.Contains(errout.String(), "profile") {
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
	args := []string{"install", "demo", "--config", cfg, "--state-dir", st, "--profile", "ota", "--agent", "all", "--agent-home", home, "--set", "enabled=false", "--set", "address=bad"}
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
	f, err := parse([]string{"install", "guide", "--profile", "ota", "--mcp", "server", "--item", "skill:one", "--item", "skill:two", "--profile-directory", "/profiles"})
	if err != nil || f.profile != "ota" || f.mcp != "server" || len(f.items) != 2 || f.envroot != "/profiles" {
		t.Fatalf("%+v %v", f, err)
	}
}
