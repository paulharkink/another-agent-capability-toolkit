package polyrepo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshWritesNodeLocalInventoryAndPreservesManagerSettings(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	repo := filepath.Join(root, "repos", "alpha")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
	}
	run("init")
	run("config", "remote.origin.url", "https://fixture.invalid/team/alpha.git")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "fixture")
	if err := os.MkdirAll(filepath.Join(stateRoot, "manager"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsFile(stateRoot), []byte(`{"agents":["opencode"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Save(stateRoot, Settings{Roots: []string{filepath.Join(root, "repos")}, Hosts: []string{"fixture.invalid"}}); err != nil {
		t.Fatal(err)
	}
	inv, err := Refresh(context.Background(), stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Version != 1 || inv.NodeID == "" || len(inv.Repositories) != 1 {
		t.Fatalf("unexpected inventory: %+v", inv)
	}
	row := inv.Repositories[0]
	if row.Host != "fixture.invalid" || row.Name != "team/alpha" || row.RepoID == "" || len(row.Worktrees) != 1 {
		t.Fatalf("unexpected repository: %+v", row)
	}
	data, err := os.ReadFile(filepath.Join(stateRoot, "manager", InventoryName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fixture@example.invalid") {
		t.Fatal("inventory unexpectedly included unrelated git config")
	}
	var settings map[string]json.RawMessage
	b, _ := os.ReadFile(settingsFile(stateRoot))
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["agents"] == nil || settings["polyrepo"] == nil {
		t.Fatalf("manager settings not preserved: %s", b)
	}
}
