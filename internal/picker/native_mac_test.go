package picker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeMacPickerCommands(t *testing.T, response string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	compileArgs := filepath.Join(dir, "compile-args")
	openArgs := filepath.Join(dir, "open-args")
	responseFile := filepath.Join(dir, "response")
	if err := os.WriteFile(responseFile, []byte(response), 0600); err != nil {
		t.Fatal(err)
	}
	compile := `#!/bin/sh
printf '%s\n' "$@" > "$AACT_TEST_COMPILE_ARGS"
if [ -n "$AACT_TEST_COMPILE_FAIL" ]; then echo 'compile failure' >&2; exit 6; fi
`
	launch := `#!/bin/sh
printf '%s\n' "$@" > "$AACT_TEST_OPEN_ARGS"
if [ -n "$AACT_TEST_OPEN_FAIL" ]; then echo 'launch failure' >&2; exit 7; fi
while [ "$#" -gt 0 ] && [ "$1" != '--args' ]; do shift; done
[ "$#" -gt 0 ] || exit 8
shift
if [ -z "$AACT_TEST_NO_RESULT" ]; then cp "$AACT_TEST_RESPONSE" "$1"; fi
`
	for name, data := range map[string]string{"osacompile": compile, "open": launch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AACT_TEST_COMPILE_ARGS", compileArgs)
	t.Setenv("AACT_TEST_OPEN_ARGS", openArgs)
	t.Setenv("AACT_TEST_RESPONSE", responseFile)
	return compileArgs, openArgs
}

func TestMacNativeReturnsSelectedFileAndRequestsHiddenFiles(t *testing.T) {
	compileArgs, openArgs := fakeMacPickerCommands(t, `{"status":"selected","path":"/private/tmp/kubeconfig"}`)
	path, err := selectMacNative(context.Background(), "file", "/private/tmp/old-config")
	if err != nil || path != "/private/tmp/kubeconfig" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	compiled, err := os.ReadFile(compileArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compiled), "setShowsHiddenFiles(true)") {
		t.Fatalf("helper does not show hidden files: %s", compiled)
	}
	launched, err := os.ReadFile(openArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launched), "\n--args\n") || !strings.Contains(string(launched), "\nfile\n/private/tmp\n") {
		t.Fatalf("incorrect launch arguments: %s", launched)
	}
}

func TestMacNativeSelectsDirectory(t *testing.T) {
	directory := t.TempDir()
	_, openArgs := fakeMacPickerCommands(t, `{"status":"selected","path":"`+directory+`"}`)
	path, err := selectMacNative(context.Background(), "directory", directory)
	if err != nil || path != directory {
		t.Fatalf("path=%q err=%v", path, err)
	}
	launched, err := os.ReadFile(openArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launched), "\ndirectory\n"+directory+"\n") {
		t.Fatalf("incorrect directory launch: %s", launched)
	}
}

func TestMacNativeCancel(t *testing.T) {
	fakeMacPickerCommands(t, `{"status":"cancel"}`)
	_, err := selectMacNative(context.Background(), "file", "")
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err=%v, want ErrCancelled", err)
	}
}

func TestMacNativeReportsCompileAndLaunchErrors(t *testing.T) {
	t.Run("compile", func(t *testing.T) {
		fakeMacPickerCommands(t, "")
		t.Setenv("AACT_TEST_COMPILE_FAIL", "1")
		_, err := selectMacNative(context.Background(), "file", "")
		if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "compile failure") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("launch", func(t *testing.T) {
		fakeMacPickerCommands(t, "")
		t.Setenv("AACT_TEST_OPEN_FAIL", "1")
		_, err := selectMacNative(context.Background(), "file", "")
		if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "launch failure") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestMacNativeContextCancellationWhileWaiting(t *testing.T) {
	fakeMacPickerCommands(t, "")
	t.Setenv("AACT_TEST_NO_RESULT", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := selectMacNative(ctx, "file", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want deadline exceeded", err)
	}
}
