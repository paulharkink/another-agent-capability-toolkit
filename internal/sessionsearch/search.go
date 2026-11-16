// Package sessionsearch launches the bundled Docker session search without
// requiring a host interpreter, while requesting read-only history mounts.
package sessionsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/versioninfo"
)

type CommandRunner interface {
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) error
}
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) error {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Env = process.EnvironmentWithoutVaultPassword(os.Environ())
	command.Stdin = in
	command.Stdout = out
	command.Stderr = diagnostics
	return command.Run()
}

type Search struct {
	Runner         CommandRunner
	PackageDir     string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func PackageDirectory() (string, error) {
	if configured := os.Getenv("AACT_FIND_SESSION_PACKAGE_DIR"); configured != "" {
		return filepath.Abs(configured)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(executable)), nil
}
func (s *Search) Run(ctx context.Context, args []string, home string, interactive bool) error {
	runner := s.Runner
	if runner == nil {
		runner = OSRunner{}
	}
	stdin := s.Stdin
	if stdin == nil && interactive {
		stdin = os.Stdin
	}
	stdout := s.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := s.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	imageReference := "ghcr.io/" + strings.ToLower(versioninfo.ImageOwner) + "/another-agent-capability-toolkit/aact-find-session:v{aact_version}"
	image, releaseErr := versioninfo.ResolveImage(imageReference)
	if releaseErr != nil {
		packageDir := s.PackageDir
		if packageDir == "" {
			var err error
			packageDir, err = PackageDirectory()
			if err != nil {
				return err
			}
		}
		packageDir, err := filepath.Abs(packageDir)
		if err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(packageDir))
		image = "aact/find-session-" + hex.EncodeToString(hash[:])[:12] + ":local"
		if err = runner.Run(ctx, []string{"docker", "build", "--quiet", "--tag", image, filepath.Join(packageDir, "container")}, nil, stderr, stderr); err != nil {
			return err
		}
	}
	argv := []string{
		"docker", "run", "--rm",
		"--label", "com.joyrex2001.kubedock.request-cpu=250m,2",
		"--label", "com.joyrex2001.kubedock.request-memory=1Gi,2Gi",
	}
	for _, mapping := range []struct {
		parts       []string
		destination string
	}{
		{[]string{".claude"}, "/root/.claude"},
		{[]string{".codex", "sessions"}, "/root/.codex/sessions"},
		{[]string{".local", "share", "opencode"}, "/root/.local/share/opencode"},
	} {
		source := filepath.Join(append([]string{home}, mapping.parts...)...)
		source, err := filepath.Abs(source)
		if err != nil {
			return err
		}
		info, err := os.Stat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			continue
		}
		if runtime.GOOS != "windows" && strings.Contains(source, ":") && strings.Contains(source, ",") {
			return fmt.Errorf("Docker cannot represent history path %q containing both ':' and ','", source)
		}
		argv = append(argv, mountArgs(source, mapping.destination, runtime.GOOS)...)
	}
	if interactive {
		argv = append(argv, "--interactive", "--tty")
	}
	argv = append(argv, image)
	argv = append(argv, args...)
	return runner.Run(ctx, argv, stdin, stdout, stderr)
}

func mountArgs(source, destination, goos string) []string {
	// The volume syntax works with Docker-compatible runtimes such as
	// Kubedock. A colon in a POSIX source path is ambiguous in that syntax, so
	// retain --mount for that uncommon path shape. Windows drive-letter colons
	// are a recognized part of Windows volume paths.
	if goos != "windows" && strings.Contains(source, ":") {
		return []string{"--mount", "type=bind,src=" + source + ",dst=" + destination + ",readonly"}
	}
	return []string{"-v", source + ":" + destination + ":ro"}
}

func Run(ctx context.Context, args []string, home string, interactive bool) error {
	return (&Search{}).Run(ctx, args, home, interactive)
}
