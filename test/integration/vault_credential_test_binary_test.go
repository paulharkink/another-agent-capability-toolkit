package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func buildPublicCLIBinary(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("AACT_TEST_CLI_BINARY"); configured != "" {
		return configured
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source for CLI build")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	binary := filepath.Join(t.TempDir(), "aact")
	command := exec.Command("go", "build", "-o", binary, "./cmd/aact")
	command.Dir = filepath.Join(repoRoot, "main")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build public CLI: %v\n%s", err, output)
	}
	return binary
}
