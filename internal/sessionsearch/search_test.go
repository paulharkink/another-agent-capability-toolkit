package sessionsearch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandRecorder struct{ Args [][]string }

func (r *commandRecorder) Run(_ context.Context, a []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	r.Args = append(r.Args, append([]string{}, a...))
	return nil
}
func TestFindSessionReadOnlyMounts(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{".claude", ".codex", ".local/share/opencode"} {
		os.MkdirAll(filepath.Join(home, filepath.FromSlash(p)), 0700)
	}
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), []string{"-g", "fixture"}, home, false); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(r.Args[1], " ")
	for _, p := range []string{".claude", ".codex", ".local/share/opencode"} {
		if !strings.Contains(args, "src="+filepath.Join(home, filepath.FromSlash(p))) {
			t.Fatal(args)
		}
	}
	if strings.Count(args, ",readonly") != 3 {
		t.Fatal(args)
	}
}
func TestMissingAgentHistorySkipped(t *testing.T) {
	home := t.TempDir()
	os.Mkdir(filepath.Join(home, ".codex"), 0700)
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), nil, home, false); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(r.Args[1], " ")
	if strings.Count(args, "--mount") != 1 || strings.Contains(args, ".claude") || strings.Contains(args, "opencode") {
		t.Fatal(args)
	}
}
func TestTTYOnlyWhenInteractive(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		r := &commandRecorder{}
		s := Search{Runner: r, PackageDir: t.TempDir()}
		if err := s.Run(context.Background(), nil, t.TempDir(), interactive); err != nil {
			t.Fatal(err)
		}
		args := strings.Join(r.Args[1], " ")
		if strings.Contains(args, "--tty") != interactive || strings.Contains(args, "--interactive") != interactive {
			t.Fatal(args)
		}
	}
}
func TestWindowsLauncherPaths(t *testing.T) {
	home := t.TempDir()
	os.Mkdir(filepath.Join(home, ".codex"), 0700)
	r := &commandRecorder{}
	packageDir := filepath.Join(t.TempDir(), "release with spaces", "find-session")
	s := Search{Runner: r, PackageDir: packageDir}
	if err := s.Run(context.Background(), nil, home, false); err != nil {
		t.Fatal(err)
	}
	if r.Args[0][len(r.Args[0])-1] != filepath.Join(packageDir, "container") {
		t.Fatal(r.Args)
	}
	if !strings.Contains(strings.Join(r.Args[1], " "), "src="+filepath.Join(home, ".codex")) {
		t.Fatal(r.Args)
	}
}
