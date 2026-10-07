package picker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveShellPathPreservesExistingLiteralSpaces(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "literal file.yaml")
	if err := os.WriteFile(want, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := testPathShell(t)
	got, err := resolveShellPath(context.Background(), "literal file.yaml", cwd, shell)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveShellPathUsesShellGlobExpansion(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, "config.yaml")
	if err := os.WriteFile(want, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := testPathShell(t)
	got, err := resolveShellPath(context.Background(), "config.*", cwd, shell)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveShellPathRejectsMultipleShellMatches(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"one.yaml", "two.yaml"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := resolveShellPath(context.Background(), "*.yaml", cwd, testPathShell(t))
	if err == nil || !strings.Contains(err.Error(), "matched 2 paths") {
		t.Fatalf("error = %v, want an ambiguity error", err)
	}
}

func TestResolveShellPathHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveShellPath(ctx, "anything", t.TempDir(), testPathShell(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestResolveShellPathReportsShellSyntaxFailure(t *testing.T) {
	shell := testPathShell(t)
	if shell.kind == shellCMD {
		t.Skip("cmd.exe does not have POSIX or PowerShell quote syntax")
	}
	_, err := resolveShellPath(context.Background(), "'unterminated", t.TempDir(), shell)
	if err == nil || !strings.Contains(err.Error(), "shell path expression") || !strings.Contains(err.Error(), shellDisplayName(shell)) {
		t.Fatalf("error = %v, want the selected shell and its diagnostic", err)
	}
}

func TestResolveShellPathHonorsContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	_, err := resolveShellPath(ctx, "anything", t.TempDir(), testPathShell(t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestResolveShellPathUsesAvailableInvokingShells(t *testing.T) {
	home := setShellHomeFixture(t)
	for _, name := range []string{"zsh", "bash", "sh"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Logf("skip %s integration: shell not installed", name)
			continue
		}
		shell, err := shellFromPath(path)
		if err != nil {
			t.Fatalf("identify %s: %v", name, err)
		}
		expressions := []string{"~", `"$HOME"`}
		if shell.kind == shellPowerShell {
			expressions = []string{"~", "$env:USERPROFILE", "$HOME"}
		} else if shell.kind == shellCMD {
			expressions = []string{"%USERPROFILE%"}
		}
		for _, expression := range expressions {
			got, err := resolveShellPath(context.Background(), expression, t.TempDir(), shell)
			if err != nil {
				t.Errorf("%s expression %q: %v", name, expression, err)
				continue
			}
			if !sameResolvedPath(got, home) {
				t.Errorf("%s expression %q = %q, want shell-expanded home %q", name, expression, got, home)
			}
		}
	}
}

func testPathShell(t *testing.T) shellSpec {
	t.Helper()
	if runtime.GOOS == "windows" {
		for _, name := range []string{"pwsh", "powershell"} {
			if path, err := exec.LookPath(name); err == nil {
				if shell, err := shellFromPath(path); err == nil {
					return shell
				}
			}
		}
		if path, err := exec.LookPath("cmd.exe"); err == nil {
			return shellSpec{path: path, kind: shellCMD}
		}
		t.Skip("PowerShell and cmd.exe are unavailable")
		return shellSpec{}
	}
	for _, name := range []string{"bash", "zsh", "sh"} {
		if path, err := exec.LookPath(name); err == nil {
			shell, err := shellFromPath(path)
			if err == nil {
				return shell
			}
		}
	}
	t.Skip("no supported POSIX shell is available")
	return shellSpec{}
}

func setShellHomeFixture(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "Home With Spaces")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestResolveShellPathRejectsOversizedShellOutput(t *testing.T) {
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	shell, err := shellFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveShellPath(context.Background(), `"$(printf '%70000s' x; printf y)"`, t.TempDir(), shell)
	if err == nil || !strings.Contains(err.Error(), "too many results") {
		t.Fatalf("error = %v, want bounded-output error", err)
	}
}

func TestResolveShellPathPowerShellEngine(t *testing.T) {
	path := availablePowerShell(t)
	shell, err := shellFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	home := setShellHomeFixture(t)
	cwd := t.TempDir()
	for _, expression := range []string{"~", "$HOME", "$env:USERPROFILE"} {
		got, err := resolveShellPath(context.Background(), expression, cwd, shell)
		if err != nil {
			t.Errorf("PowerShell expression %q: %v", expression, err)
			continue
		}
		if !sameResolvedPath(got, home) {
			t.Errorf("PowerShell expression %q = %q, want %q", expression, got, home)
		}
	}

	literal := filepath.Join(cwd, "path with spaces.yaml")
	if err := os.WriteFile(literal, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveShellPath(context.Background(), filepath.Base(literal), cwd, shell)
	if err != nil || !sameResolvedPath(got, literal) {
		t.Fatalf("PowerShell literal path with spaces = %q, %v; want %q", got, err, literal)
	}
	for _, name := range []string{"ps-one.yaml", "ps-two.yaml"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(cwd, "ps-one.yaml")
	got, err = resolveShellPath(context.Background(), "ps-one.*", cwd, shell)
	if err != nil || !sameResolvedPath(got, want) {
		t.Fatalf("PowerShell single wildcard = %q, %v; want %q", got, err, want)
	}
	if _, err := resolveShellPath(context.Background(), "ps-*.yaml", cwd, shell); err == nil || !strings.Contains(err.Error(), "matched 2 paths") {
		t.Fatalf("PowerShell wildcard error = %v, want ambiguity", err)
	}
}

func TestResolveShellPathCMDNativeExpansion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe integration runs on Windows")
	}
	path, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("cmd.exe is unavailable")
	}
	shell := shellSpec{path: path, kind: shellCMD}
	home := setShellHomeFixture(t)
	got, err := resolveShellPath(context.Background(), "%USERPROFILE%", t.TempDir(), shell)
	if err != nil || !sameResolvedPath(got, home) {
		t.Fatalf("cmd.exe %%USERPROFILE%% = %q, %v; want %q", got, err, home)
	}

	cwd := t.TempDir()
	literal := filepath.Join(cwd, "path with spaces.yaml")
	if err := os.WriteFile(literal, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = resolveShellPath(context.Background(), filepath.Base(literal), cwd, shell)
	if err != nil || !sameResolvedPath(got, literal) {
		t.Fatalf("cmd.exe literal path with spaces = %q, %v; want %q", got, err, literal)
	}
	for _, name := range []string{"cmd-one.yaml", "cmd-two.yaml"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(cwd, "cmd-one.yaml")
	got, err = resolveShellPath(context.Background(), "cmd-one.*", cwd, shell)
	if err != nil || !sameResolvedPath(got, want) {
		t.Fatalf("cmd.exe single wildcard = %q, %v; want %q", got, err, want)
	}
	if _, err := resolveShellPath(context.Background(), "cmd-*.yaml", cwd, shell); err == nil || !strings.Contains(err.Error(), "matched 2 paths") {
		t.Fatalf("cmd.exe wildcard error = %v, want ambiguity", err)
	}
}

func TestResolveShellPathWindowsPOSIXShellNativePaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Git Bash/Cygwin integration runs on Windows")
	}
	var path string
	for _, name := range []string{"bash.exe", "bash"} {
		if candidate, err := exec.LookPath(name); err == nil {
			path = candidate
			break
		}
	}
	if path == "" {
		t.Skip("Git Bash/Cygwin bash is unavailable")
	}
	shell, err := shellFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	home := setShellHomeFixture(t)
	for _, expression := range []string{"~", `"$HOME"`} {
		got, err := resolveShellPath(context.Background(), expression, t.TempDir(), shell)
		if err != nil || !sameResolvedPath(got, home) {
			t.Errorf("Windows POSIX expression %q = %q, %v; want %q", expression, got, err, home)
		}
	}
	cwd := filepath.Join(t.TempDir(), "Picker Work With Spaces")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	literal := filepath.Join(cwd, "path with spaces.yaml")
	if err := os.WriteFile(literal, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveShellPath(context.Background(), filepath.Base(literal), cwd, shell)
	if err != nil || !sameResolvedPath(got, literal) {
		t.Fatalf("Windows POSIX literal path with spaces = %q, %v; want %q", got, err, literal)
	}
	for _, name := range []string{"bash-one.yaml", "bash-two.yaml"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(cwd, "bash-one.yaml")
	got, err = resolveShellPath(context.Background(), "bash-one.*", cwd, shell)
	if err != nil || !sameResolvedPath(got, want) {
		t.Fatalf("Windows POSIX single wildcard = %q, %v; want %q", got, err, want)
	}
	if _, err := resolveShellPath(context.Background(), "bash-*.yaml", cwd, shell); err == nil || !strings.Contains(err.Error(), "matched 2 paths") {
		t.Fatalf("Windows POSIX wildcard error = %v, want ambiguity", err)
	}
}

func availablePowerShell(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell runtime is unavailable")
	return ""
}

func sameResolvedPath(got, want string) bool {
	gotInfo, gotErr := os.Stat(got)
	wantInfo, wantErr := os.Stat(want)
	return gotErr == nil && wantErr == nil && os.SameFile(gotInfo, wantInfo)
}

func TestLimitedBufferCapsWrites(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 2
	if _, err := buffer.Write([]byte("four")); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() != 2 || !buffer.truncated {
		t.Fatalf("len=%d truncated=%v, want len=2 and truncated", buffer.Len(), buffer.truncated)
	}
}

func TestShellResultsReadsCMDLines(t *testing.T) {
	got := shellResults([]byte("C:\\one.yaml\r\nC:\\two.yaml\r\n"), shellCMD)
	if len(got) != 2 || string(got[0]) != `C:\one.yaml` || string(got[1]) != `C:\two.yaml` {
		t.Fatalf("results = %q, want two CRLF-delimited Windows paths", got)
	}
}
