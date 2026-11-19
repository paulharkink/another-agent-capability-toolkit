package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	integrationBinaryDir string
	integrationBinaryMu  sync.Mutex
	integrationBinaries  = map[string]string{}
)

func TestMain(m *testing.M) {
	var err error
	integrationBinaryDir, err = os.MkdirTemp("", "aact-integration-binaries-")
	if err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(integrationBinaryDir)
	os.Exit(code)
}

func buildIntegrationBinary(t *testing.T, name string, ldflags string) string {
	t.Helper()
	integrationBinaryMu.Lock()
	defer integrationBinaryMu.Unlock()
	if binary := integrationBinaries[name]; binary != "" {
		return binary
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test source for CLI build")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	binaryName := "aact"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(integrationBinaryDir, name, binaryName)
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatalf("create binary cache: %v", err)
	}
	args := []string{"build"}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "-o", binary, "./cmd/aact")
	command := exec.Command("go", args...)
	command.Dir = filepath.Join(repoRoot, "main")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %s: %v", name, output, err)
	}
	integrationBinaries[name] = binary
	return binary
}

func buildPublicCLIBinary(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("AACT_TEST_CLI_BINARY"); configured != "" {
		return configured
	}
	return buildIntegrationBinary(t, "public-cli", "")
}

func buildVersionCommandBinary(t *testing.T) string {
	return buildVersionedCLIBinary(t, "0.9.0")
}

func buildVersionedCLIBinary(t *testing.T, version string) string {
	ldflags := fmt.Sprintf("-X=github.com/paulharkink/another-agent-capability-toolkit/main/internal/cli.Version=%s -X=github.com/paulharkink/another-agent-capability-toolkit/main/internal/versioninfo.Version=%s", version, version)
	return buildIntegrationBinary(t, "version-command-"+version, ldflags)
}

func buildLegacyNoopBinary(t *testing.T) string {
	t.Helper()
	integrationBinaryMu.Lock()
	defer integrationBinaryMu.Unlock()
	if binary := integrationBinaries["legacy-noop"]; binary != "" {
		return binary
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test source for legacy executable build")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	binaryName := filepath.Join(integrationBinaryDir, "legacy-noop", "aact")
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(binaryName), 0700); err != nil {
		t.Fatalf("create legacy executable cache: %v", err)
	}
	command := exec.Command("go", "build", "-o", binaryName, "./integration/testdata/legacy_noop")
	command.Dir = filepath.Join(repoRoot, "test")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build legacy no-op executable: %s: %v", output, err)
	}
	integrationBinaries["legacy-noop"] = binaryName
	return binaryName
}
