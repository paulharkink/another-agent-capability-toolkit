//go:build docker_integration

package sessionsearch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchSyntheticHistoryInActualContainer(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "30")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-30T12-00-00-fx1.jsonl")
	content := `{"type":"session_meta","payload":{"id":"fx1","cwd":"/fixture/synthetic-project","git":{"branch":"feature/fixture"},"timestamp":"2026-09-30T12:00:00Z"}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"We discussed postgres fixture evidence"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	packageDir, err := filepath.Abs(filepath.Join("..", "..", "packages", "find-session"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	s := Search{PackageDir: packageDir, Stdout: &stdout, Stderr: &stderr}
	if err = s.Run(context.Background(), []string{"-g", "postgres"}, home, false); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "postgres") || !strings.Contains(stdout.String(), "fx1") {
		t.Fatalf("search result missing: %s", stdout.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != content {
		t.Fatal("history changed")
	}
}
