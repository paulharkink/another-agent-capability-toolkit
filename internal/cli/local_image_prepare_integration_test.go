package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIPrepareLocalImageKeepsIntegerConfigThroughHelper(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
	root := t.TempDir()
	packRoot := filepath.Join(root, "packages")
	packageDir := filepath.Join(packRoot, "cluster-inspector")
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(packageDir, os.DirFS(filepath.Join(repo, "packages", "cluster-inspector"))); err != nil {
		t.Fatal(err)
	}
	helperDir := filepath.Join(packageDir, "bin")
	if err := os.MkdirAll(helperDir, 0700); err != nil {
		t.Fatal(err)
	}
	helperName := "inspector-helper"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	helper := filepath.Join(helperDir, helperName)
	buildHelper := exec.Command("go", "build", "-o", helper, "./cmd/inspector-helper")
	buildHelper.Dir = repo
	if output, err := buildHelper.CombinedOutput(); err != nil {
		t.Fatalf("build actual capability helper: %v\n%s", err, output)
	}

	shimDir := filepath.Join(root, "shim")
	if err := os.MkdirAll(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	shimSource := filepath.Join(shimDir, "docker.go")
	shim := `package main
import (
  "fmt"
  "os"
  "path/filepath"
  "strings"
)
func main() {
  args := os.Args[1:]
  if len(args) == 0 { os.Exit(2) }
  switch args[0] {
  case "build":
    for i, arg := range args {
      if arg == "--iidfile" && i+1 < len(args) {
        _ = os.WriteFile(args[i+1], []byte("sha256:"+strings.Repeat("a", 64)), 0600)
      }
    }
  case "run":
    for _, arg := range args {
      if strings.HasPrefix(arg, "type=bind,") && strings.Contains(arg, ",dst=/config") {
        source := strings.SplitN(strings.SplitN(arg, "src=", 2)[1], ",", 2)[0]
        data, err := os.ReadFile(filepath.Join(source, "target.toml"))
        if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
        if err = os.WriteFile(os.Getenv("AACT_TEST_CAPTURED_CONFIG"), data, 0600); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
      }
    }
    fmt.Println("{\"auth_required\":false}")
  default:
    fmt.Fprintf(os.Stderr, "unexpected docker command %q", args[0])
    os.Exit(2)
  }
}`
	if err := os.WriteFile(shimSource, []byte(shim), 0600); err != nil {
		t.Fatal(err)
	}
	dockerName := "docker"
	if runtime.GOOS == "windows" {
		dockerName += ".exe"
	}
	docker := filepath.Join(shimDir, dockerName)
	buildShim := exec.Command("go", "build", "-o", docker, shimSource)
	buildShim.Dir = repo
	if output, err := buildShim.CombinedOutput(); err != nil {
		t.Fatalf("build disposable Docker shim: %v\n%s", err, output)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AACT_BUNDLED_ROOT", packRoot)
	capturedConfig := filepath.Join(root, "captured-target.toml")
	t.Setenv("AACT_TEST_CAPTURED_CONFIG", capturedConfig)

	stateDir := filepath.Join(root, "state")
	instanceDir := filepath.Join(root, "instances")
	var out, stderr bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		if code := Run(context.Background(), args, nil, &out, &stderr); code != 0 {
			t.Fatalf("aact %s failed (%d): %s", strings.Join(args, " "), code, stderr.String())
		}
	}
	run("config", "set-image-source", "local", "--state-dir", stateDir)
	run("instance", "create", "cluster-inspector", "smoke", "--state-dir", stateDir, "--instance-directory", instanceDir)
	run("mcp", "prepare", "cluster-inspector", "--instance", "smoke",
		"--set", "api_server=https://127.0.0.1:7443", "--set", "registration_name=aact-integration-smoke", "--set", "local_port=38765",
		"--state-dir", stateDir, "--instance-directory", instanceDir)

	data, err := os.ReadFile(capturedConfig)
	if err != nil {
		t.Fatalf("Docker helper did not receive staged config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "local_port = 38765\n") || strings.Contains(text, "local_port = 38765.0") {
		t.Fatalf("CLI → helper prepare changed the declared integer port type; staged config:\n%s", text)
	}
}
