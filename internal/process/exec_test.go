package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessFixture(t *testing.T) {
	if os.Getenv("AACT_PROCESS_FIXTURE") != "1" {
		return
	}
	mode := os.Getenv("AACT_PROCESS_MODE")
	switch mode {
	case "echo":
		b, _ := io.ReadAll(os.Stdin)
		if len(b) == 0 {
			fmt.Print(os.Getenv("AACT_VALUE"))
		} else {
			fmt.Print(string(b))
		}
		fmt.Fprint(os.Stderr, "progress: running\n")
	case "fail":
		fmt.Fprint(os.Stderr, "expected diagnostic\n")
		os.Exit(7)
	case "wait":
		time.Sleep(30 * time.Second)
	case "large":
		fmt.Print(strings.Repeat("x", 17<<20))
	}
	os.Exit(0)
}

func fixtureEnv(mode string) map[string]string {
	return map[string]string{"AACT_PROCESS_FIXTURE": "1", "AACT_PROCESS_MODE": mode}
}
func fixtureArgv() []string { return []string{os.Args[0], "-test.run=^TestProcessFixture$"} }

func TestRunReceivesStdinAndStreamsStderr(t *testing.T) {
	var diag strings.Builder
	got, err := Run(context.Background(), fixtureArgv(), t.TempDir(), []byte(`{"inputs":{"enabled":true}}`), fixtureEnv("echo"), func(b []byte) { diag.Write(b) })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"inputs":{"enabled":true}}` {
		t.Fatalf("stdout=%q", got)
	}
	if !strings.Contains(diag.String(), "progress") {
		t.Fatalf("stderr=%q", diag.String())
	}
}
func TestRunNonzeroExit(t *testing.T) {
	var diag strings.Builder
	_, err := Run(context.Background(), fixtureArgv(), t.TempDir(), nil, fixtureEnv("fail"), func(b []byte) { diag.Write(b) })
	if err == nil || !strings.Contains(diag.String(), "expected diagnostic") {
		t.Fatalf("err=%v stderr=%q", err, diag.String())
	}
}
func TestRunCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	before := time.Now()
	_, err := Run(ctx, fixtureArgv(), t.TempDir(), nil, fixtureEnv("wait"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(before) > 3*time.Second {
		t.Fatal("child was not cancelled promptly")
	}
}
func TestRunRejectsOversizedStdout(t *testing.T) {
	_, err := Run(context.Background(), fixtureArgv(), t.TempDir(), nil, fixtureEnv("large"), nil)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("err=%v", err)
	}
}
func TestRunEmptyArgv(t *testing.T) {
	if _, err := Run(context.Background(), nil, t.TempDir(), nil, nil, nil); err == nil {
		t.Fatal("empty argv accepted")
	}
}
func TestExecutorUsesSameContract(t *testing.T) {
	var executor Executor = OSExecutor{}
	env := fixtureEnv("echo")
	env["AACT_VALUE"] = "unchanged"
	got, err := executor.Run(context.Background(), fixtureArgv(), t.TempDir(), nil, env, nil)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
