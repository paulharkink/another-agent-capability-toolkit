package sessionsearch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/versioninfo"
)

type commandRecorder struct{ Args [][]string }

func (r *commandRecorder) Run(_ context.Context, a []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	r.Args = append(r.Args, append([]string{}, a...))
	return nil
}
func TestFindSessionReadOnlyMounts(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{".claude", ".codex/sessions", ".local/share/opencode"} {
		os.MkdirAll(filepath.Join(home, filepath.FromSlash(p)), 0700)
	}
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), []string{"-g", "fixture"}, home, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".claude") + ":/root/.claude:ro",
		filepath.Join(home, ".codex", "sessions") + ":/root/.codex/sessions:ro",
		filepath.Join(home, ".local", "share", "opencode") + ":/root/.local/share/opencode:ro",
	}
	if got := volumeSpecs(r.Args[1]); !equalStrings(got, want) {
		t.Fatalf("volume specs = %#v, want %#v (argv %#v)", got, want, r.Args[1])
	}
}

func TestKubedockResourceLimitsAreExplicitForSessionSearch(t *testing.T) {
	previousVersion, previousOwner := versioninfo.ImageVersion, versioninfo.ImageOwner
	versioninfo.ImageVersion, versioninfo.ImageOwner = "0.7.2", "exampleowner"
	t.Cleanup(func() { versioninfo.ImageVersion, versioninfo.ImageOwner = previousVersion, previousOwner })
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), nil, t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker", "run", "--rm",
		"--label", "com.joyrex2001.kubedock.request-cpu=250m,2",
		"--label", "com.joyrex2001.kubedock.request-memory=1Gi,2Gi",
	}
	argv := r.Args[0]
	if len(argv) < len(want) || !equalStrings(argv[:len(want)], want) {
		t.Fatalf("docker run resource labels = %#v, want prefix %#v", argv, want)
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
	want := []string{}
	if got := volumeSpecs(r.Args[1]); !equalStrings(got, want) || containsArg(r.Args[1], ".claude") || containsArg(r.Args[1], "opencode") {
		t.Fatalf("unexpected docker mount args: %#v", r.Args[1])
	}
}

func TestCodexMountUsesOnlySessionHistory(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex", "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), nil, home, false); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, ".codex", "sessions") + ":/root/.codex/sessions:ro"}
	if got := volumeSpecs(r.Args[1]); !equalStrings(got, want) {
		t.Fatalf("Codex volume specs = %#v, want %#v", got, want)
	}
}

func TestVolumeMountPreservesCommaAndSpaceInPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home, with spaces")
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), nil, home, false); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, ".codex", "sessions") + ":/root/.codex/sessions:ro"}
	if got := volumeSpecs(r.Args[1]); !equalStrings(got, want) {
		t.Fatalf("volume specs = %#v, want %#v", got, want)
	}
}

func TestColonPathUsesMountSyntaxToPreserveSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths only permit a drive-letter colon")
	}
	home := filepath.Join(t.TempDir(), "home:with-colon")
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), nil, home, false); err != nil {
		t.Fatal(err)
	}
	argv := r.Args[1]
	want := "type=bind,src=" + filepath.Join(home, ".codex", "sessions") + ",dst=/root/.codex/sessions,readonly"
	if !containsValueAfter(argv, "--mount", want) {
		t.Fatalf("colon path was not preserved with mount syntax: %#v", argv)
	}
}

func TestColonAndCommaPathIsRejectedBeforeRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths cannot contain colons outside the drive prefix")
	}
	home := filepath.Join(t.TempDir(), "home:with,colon")
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: t.TempDir()}
	if err := s.Run(context.Background(), nil, home, false); err == nil {
		t.Fatal("ambiguous volume path should fail before docker run")
	}
	if len(r.Args) != 1 || r.Args[0][1] != "build" {
		t.Fatalf("docker run was invoked for an ambiguous path: %#v", r.Args)
	}
}

func TestWindowsVolumeSyntaxKeepsDriveLetterColon(t *testing.T) {
	got := mountArgs(`C:\Users\Alice\.codex\sessions`, "/root/.codex/sessions", "windows")
	want := []string{"-v", `C:\Users\Alice\.codex\sessions:/root/.codex/sessions:ro`}
	if !equalStrings(got, want) {
		t.Fatalf("Windows volume args = %#v, want %#v", got, want)
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
	os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700)
	r := &commandRecorder{}
	packageDir := filepath.Join(t.TempDir(), "release with spaces", "find-session")
	s := Search{Runner: r, PackageDir: packageDir}
	if err := s.Run(context.Background(), nil, home, false); err != nil {
		t.Fatal(err)
	}
	if r.Args[0][len(r.Args[0])-1] != filepath.Join(packageDir, "container") {
		t.Fatal(r.Args)
	}
	want := []string{filepath.Join(home, ".codex", "sessions") + ":/root/.codex/sessions:ro"}
	if got := volumeSpecs(r.Args[1]); !equalStrings(got, want) {
		t.Fatalf("volume specs = %#v, want %#v", got, want)
	}
}

func volumeSpecs(argv []string) []string {
	var specs []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" {
			specs = append(specs, argv[i+1])
		}
	}
	return specs
}

func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}
	return false
}

func containsValueAfter(argv []string, flag, want string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == want {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestReleaseBuildUsesVersionedPublishedImageWithoutBuilding(t *testing.T) {
	previousVersion, previousOwner := versioninfo.ImageVersion, versioninfo.ImageOwner
	versioninfo.ImageVersion, versioninfo.ImageOwner = "0.4.2", "ExampleOwner"
	t.Cleanup(func() { versioninfo.ImageVersion, versioninfo.ImageOwner = previousVersion, previousOwner })

	r := &commandRecorder{}
	s := Search{Runner: r, PackageDir: filepath.Join(t.TempDir(), "missing-package")}
	if err := s.Run(context.Background(), []string{"-g", "fixture"}, t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if len(r.Args) != 1 {
		t.Fatalf("commands = %#v, want only docker run", r.Args)
	}
	want := "ghcr.io/exampleowner/another-agent-capability-toolkit/aact-find-session:v0.4.2"
	if got := r.Args[0]; len(got) < 5 || got[0] != "docker" || got[1] != "run" || got[len(got)-3] != want || got[len(got)-2] != "-g" {
		t.Fatalf("docker run args = %#v, want published image %q", got, want)
	}
}
