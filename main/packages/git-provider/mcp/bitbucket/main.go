package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"
)

func Run() int {
	provider := os.Getenv("MCP_HEALTH_PROVIDER")
	childArgs := os.Args[1:]
	if len(childArgs) == 0 {
		childArgs = defaultChild(provider)
	} else if executable := os.Getenv("MCP_HEALTH_CHILD"); executable != "" && !strings.ContainsRune(childArgs[0], '/') {
		childArgs = append([]string{executable}, childArgs...)
	}
	if len(childArgs) == 0 {
		return 1
	}
	vaultSecrets, err := waitForVaultSecrets(os.Environ(), vaultHandoffPath, 30*time.Second)
	if err != nil {
		return 1
	}
	providerToken := vaultSecrets[os.Getenv("AACT_VAULT_AUTH_ENV")]
	child, childDone, err := startChildWithEnv(childArgs, vaultSecrets)
	if err != nil {
		return 1
	}
	var childAlive atomic.Bool
	childAlive.Store(true)

	config := Config{
		Provider: provider, UpstreamURL: os.Getenv("MCP_HEALTH_UPSTREAM"),
		GitHubHost: os.Getenv("GITHUB_HOST"), GitLabURL: os.Getenv("GITLAB_API_URL"),
		BitbucketURL: os.Getenv("BITBUCKET_URL"), BitbucketToken: providerToken,
		ProviderToken: providerToken, ChildReady: childAlive.Load,
	}
	server := &http.Server{Addr: listenAddress(), Handler: New(config), ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ListenAndServe() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, wrapperShutdownSignals()...)
	defer signal.Stop(signals)
	select {
	case <-childDone:
		childAlive.Store(false)
		child = nil
		childDone = nil
		// Keep the front process available so health remains a stable endpoint
		// and reports false while the owned MCP backend is unavailable.
		select {
		case <-signals:
		case err := <-serveDone:
			if !errors.Is(err, http.ErrServerClosed) {
				return 1
			}
			return 0
		}
	case <-signals:
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			stopChild(child, childDone)
			return 1
		}
		return 0
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = server.Shutdown(shutdownCtx)
	cancel()
	stopChild(child, childDone)
	return 0
}

func defaultChild(provider string) []string {
	switch provider {
	case "gitlab":
		return []string{"node", "build/index.js"}
	case "bitbucket":
		return []string{"docker-entrypoint.sh", "npm", "start"}
	default:
		if executable := os.Getenv("MCP_HEALTH_CHILD"); executable != "" {
			return []string{executable, "http", "--port", "8082"}
		}
		return nil
	}
}

func listenAddress() string {
	if address := os.Getenv("MCP_HEALTH_LISTEN"); address != "" {
		return address
	}
	return "0.0.0.0:8084"
}

func startChild(args []string) (*exec.Cmd, <-chan error, error) { return startChildWithEnv(args, nil) }

func startChildWithEnv(args []string, additions map[string]string) (*exec.Cmd, <-chan error, error) {
	child := exec.Command(args[0], args[1:]...)
	child.Env = childEnvironment(os.Getenv("MCP_HEALTH_PROVIDER"), os.Environ())
	for key, value := range additions {
		child.Env = setEnvironment(child.Env, key, value)
	}
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	configureWrapperChildProcessTree(child)
	if err := child.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	return child, done, nil
}

func childEnvironment(provider string, environment []string) []string {
	childEnv := append([]string(nil), environment...)
	if provider != "bitbucket" {
		return childEnv
	}
	for i, item := range childEnv {
		name, value, found := strings.Cut(item, "=")
		if found && name == "BITBUCKET_URL" {
			childEnv[i] = name + "=" + strings.TrimRight(value, "/")
			break
		}
	}
	return childEnv
}

func stopChild(child *exec.Cmd, childDone <-chan error) {
	if child == nil || child.Process == nil {
		return
	}
	_ = signalWrapperChildProcessTree(child, false)
	select {
	case <-childDone:
	case <-time.After(5 * time.Second):
		_ = signalWrapperChildProcessTree(child, true)
		<-childDone
	}
}

func setEnvironment(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
