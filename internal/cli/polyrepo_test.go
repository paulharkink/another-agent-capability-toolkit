package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/polyrepo"
)

func TestPolyrepoConfigurePersistsOptionalLocalWebhookSettings(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	repos := filepath.Join(root, "repos")
	worktrees := filepath.Join(root, "worktrees")
	secret := filepath.Join(root, "webhook-key")
	if err := os.MkdirAll(repos, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("fixture-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--state-dir", stateRoot, "polyrepo", "configure", "--root", repos, "--worktree-root", worktrees, "--webhook-host", "127.0.0.1", "--webhook-port", "4097", "--webhook-secret-file", secret}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), args, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("configure failed (exit %d): %s", code, stderr.String())
	}
	settings, err := polyrepo.Load(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Listener == nil || settings.Listener.Host != "127.0.0.1" || settings.Listener.Port != 4097 || settings.Listener.SecretFile != secret {
		t.Fatalf("local listener settings not persisted: %+v", settings.Listener)
	}
	data, err := os.ReadFile(filepath.Join(stateRoot, "manager", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fixture-secret") {
		t.Fatal("secret contents were copied into local AACT settings")
	}
}

func TestPolyrepoConfigureRejectsPartialWebhookSettings(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	repos := filepath.Join(root, "repos")
	if err := os.MkdirAll(repos, 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	args := []string{"--state-dir", stateRoot, "polyrepo", "configure", "--root", repos, "--webhook-host", "127.0.0.1"}
	if code := Run(context.Background(), args, strings.NewReader(""), &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "all of --webhook-host, --webhook-port, and --webhook-secret-file") {
		t.Fatalf("partial listener settings should fail clearly; exit=%d stderr=%q", code, stderr.String())
	}
}
