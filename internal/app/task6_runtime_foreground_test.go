package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type task6DockerFailureExecutor struct{}

func (task6DockerFailureExecutor) Run(_ context.Context, args []string, _ string, _ []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	if len(args) > 1 && args[1] == "inspect" {
		stderr([]byte("Error: No such object: container"))
		return nil, errors.New("exit status 1")
	}
	if len(args) > 1 && args[1] == "run" {
		stderr([]byte("Error response from daemon: bind rejected for private-secret"))
		return nil, errors.New("exit status 1")
	}
	return nil, errors.New("unexpected Docker command")
}

func TestRuntimeFailureKeepsDaemonCauseInForegroundAndRedactsSecrets(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := mcp.NewDockerRuntime(store)
	runtime.Executor = task6DockerFailureExecutor{}
	service := &Service{Store: store, Options: Options{Runtime: runtime}}
	var streamed strings.Builder
	scoped, ctx := service.withOperationProgress(context.Background(), func(event viewmodel.OperationProgress) {
		streamed.WriteString(event.Output)
	})
	key := state.Key{Source: "fixture", Package: "sample", Environment: "dev", Target: "test"}
	_, err = scoped.Options.Runtime.(*mcp.Runtime).Start(ctx, key, mcp.RunSpec{
		Image: "fixture", RegistrationName: "Inspector", HostPort: 8765, ContainerPort: 80,
		SecretEnv: map[string]string{"TOKEN": "private-secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "Error response from daemon: bind rejected") || !strings.Contains(err.Error(), "exit status 1") || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("failed Docker operation lost its cause: %v", err)
	}
	if !strings.Contains(streamed.String(), "Error response from daemon: bind rejected") || !strings.Contains(streamed.String(), "[redacted]") || strings.Contains(streamed.String(), "private-secret") {
		t.Fatalf("foreground output lost the daemon cause or leaked a secret: %q", streamed.String())
	}
}

var _ process.Executor = task6DockerFailureExecutor{}

type task6ProbeExecutor struct {
	inspectOutput string
	inspectErr    error
	runOutput     []byte
	runStderr     string
	runErr        error
	runCalled     bool
}

func (e *task6ProbeExecutor) Run(_ context.Context, args []string, _ string, _ []byte, _ map[string]string, stderr func([]byte)) ([]byte, error) {
	if len(args) > 1 && args[1] == "inspect" {
		if e.inspectOutput != "" {
			stderr([]byte(e.inspectOutput))
		}
		if e.inspectErr != nil {
			return nil, e.inspectErr
		}
		return nil, errors.New("exit status 1")
	}
	if len(args) > 1 && args[1] == "run" {
		e.runCalled = true
		if e.runStderr != "" {
			stderr([]byte(e.runStderr))
		}
		return e.runOutput, e.runErr
	}
	return nil, errors.New("unexpected Docker command")
}

func TestExpectedDockerInspectMissIsQuietButActualFailuresStayPrimary(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		inspectStderr string
		inspectErr    error
		runOutput     []byte
		runStderr     string
		runErr        error
		wantRun       bool
		wantErr       string
		wantProgress  string
	}{
		{name: "first start succeeds", inspectStderr: "Error: No such object: expected probe", runOutput: []byte("new-container-id"), wantRun: true},
		{name: "lowercase Docker Desktop probe", inspectStderr: "error: no such object: expected probe", runOutput: []byte("new-container-id"), wantRun: true},
		{name: "Docker run failure", inspectStderr: "Error: No such container: expected probe", runStderr: "Error response from daemon: address is already allocated", runErr: errors.New("exit status 1"), wantRun: true, wantErr: "address is already allocated", wantProgress: "address is already allocated"},
		{name: "inspect permission failure", inspectStderr: "Error response from daemon: permission denied", inspectErr: errors.New("exit status 1"), wantErr: "permission denied", wantProgress: "permission denied"},
		{name: "mixed daemon and not-found text", inspectStderr: "Error response from daemon: permission denied while resolving No such object: name", inspectErr: errors.New("exit status 1"), wantErr: "permission denied", wantProgress: "permission denied"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, err := state.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			executor := &task6ProbeExecutor{inspectOutput: scenario.inspectStderr, inspectErr: scenario.inspectErr, runOutput: scenario.runOutput, runStderr: scenario.runStderr, runErr: scenario.runErr}
			runtime := mcp.NewDockerRuntime(store)
			runtime.Executor = executor
			runtime.SkipHealth = true
			var priorStderr strings.Builder
			runtime.OnStderr = func(output []byte) { priorStderr.Write(output) }
			service := &Service{Store: store, Options: Options{Runtime: runtime}}
			var streamed strings.Builder
			scoped, ctx := service.withOperationProgress(context.Background(), func(event viewmodel.OperationProgress) {
				streamed.WriteString(event.Output)
			})
			key := state.Key{Source: "fixture", Package: "sample", Environment: "dev", Target: "test"}
			_, err = scoped.Options.Runtime.(*mcp.Runtime).Start(ctx, key, mcp.RunSpec{
				Image: "fixture", RegistrationName: "Inspector", HostPort: 8765, ContainerPort: 80,
			})
			if executor.runCalled != scenario.wantRun {
				t.Fatalf("docker run called=%t, want %t", executor.runCalled, scenario.wantRun)
			}
			if scenario.wantErr == "" {
				if err != nil {
					t.Fatalf("first start failed: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), scenario.wantErr) {
				t.Fatalf("primary Docker cause missing from result: %v", err)
			}
			if strings.Contains(streamed.String(), "expected probe") || (scenario.wantProgress != "" && !strings.Contains(streamed.String(), scenario.wantProgress)) {
				t.Fatalf("foreground output has probe noise or lost actual cause: %q", streamed.String())
			}
			if strings.Contains(priorStderr.String(), "expected probe") || (scenario.wantProgress != "" && !strings.Contains(priorStderr.String(), scenario.wantProgress)) {
				t.Fatalf("raw stderr callback has probe noise or lost actual cause: %q", priorStderr.String())
			}
		})
	}
}
