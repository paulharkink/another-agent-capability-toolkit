// Package sessionsearch launches the bundled Docker session search without
// requiring a host interpreter, while mounting existing history read-only.
package sessionsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) error
}
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) error {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
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
	hash := sha256.Sum256([]byte(packageDir))
	image := "aact/find-session-" + hex.EncodeToString(hash[:])[:12] + ":local"
	if err = runner.Run(ctx, []string{"docker", "build", "--quiet", "--tag", image, filepath.Join(packageDir, "container")}, nil, stderr, stderr); err != nil {
		return err
	}
	argv := []string{"docker", "run", "--rm"}
	for _, mapping := range []struct {
		parts       []string
		destination string
	}{
		{[]string{".claude"}, "/root/.claude"},
		{[]string{".codex"}, "/root/.codex"},
		{[]string{".local", "share", "opencode"}, "/root/.local/share/opencode"},
	} {
		source := filepath.Join(append([]string{home}, mapping.parts...)...)
		source, err = filepath.Abs(source)
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
		if strings.Contains(source, ",") {
			return errors.New("Docker history mount path cannot contain a comma")
		}
		argv = append(argv, "--mount", "type=bind,src="+source+",dst="+mapping.destination+",readonly")
	}
	if interactive {
		argv = append(argv, "--interactive", "--tty")
	}
	argv = append(argv, image)
	argv = append(argv, args...)
	return runner.Run(ctx, argv, stdin, stdout, stderr)
}
func Run(ctx context.Context, args []string, home string, interactive bool) error {
	return (&Search{}).Run(ctx, args, home, interactive)
}
