//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func TestDockerBrowserAuthenticationSurvivesMCPReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached test helper uses a POSIX shell")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	docker := func(args ...string) []byte {
		t.Helper()
		output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s failed: %v: %s", args[0], err, output)
		}
		return []byte(strings.TrimSpace(string(output)))
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	if _, err := exec.CommandContext(ctx, "docker", "info").Output(); err != nil {
		t.Fatalf("docker daemon is unavailable: %v", err)
	}

	architecture := string(docker("info", "--format", "{{.Architecture}}"))
	goarch := map[string]string{"aarch64": "arm64", "x86_64": "amd64"}[architecture]
	if goarch == "" {
		t.Skipf("unsupported Docker architecture %q", architecture)
	}
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	buildRoot := t.TempDir()
	helperBinary := filepath.Join(buildRoot, "aact-vault-handoff")
	buildHelper := exec.Command("go", "build", "-trimpath", "-o", helperBinary, "./main/cmd/aact-vault-handoff")
	buildHelper.Dir = repoRoot
	buildHelper.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
	if output, err := buildHelper.CombinedOutput(); err != nil {
		t.Fatalf("build isolated Linux vault handoff helper: %v: %s", err, output)
	}

	fixture, err := filepath.Abs("../../testdata/browser-auth-mcp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	fixture = fixtureWithPrefetchedPythonBase(t, fixture)
	mcpImage := fmt.Sprintf("aact-test/browser-replacement:%x", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "image", "rm", mcpImage).Run()
	})
	docker("build", "--tag", mcpImage, fixture)
	volume := string(docker("volume", "create", "--label", "aact.test=browser-replacement"))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		containers, _ := exec.CommandContext(cleanupCtx, "docker", "container", "ls", "--all", "--quiet", "--filter", "label=aact.browser-controller=1", "--filter", "label=aact.browser-controller.profile-volume="+volume).Output()
		for _, container := range strings.Fields(string(containers)) {
			_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", container).Run()
		}
		_ = exec.CommandContext(cleanupCtx, "docker", "volume", "rm", volume).Run()
	})

	root := t.TempDir()
	password := []byte("synthetic-browser-replacement-password")
	vaultPath := filepath.Join(root, "credentials.kdbx")
	vaultStore, err := vault.Open(vaultPath, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "0.0.0.0")
	control, err := vault.ControlTokenForVault(vaultPath, password)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	daemonScript := filepath.Join(root, "aact-test-daemon")
	quotedExecutable := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(daemonScript, []byte("#!/bin/sh\nAACT_TEST_DAEMON_CHILD=1 exec "+quotedExecutable+" -test.run=^TestVaultDaemonChildHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	daemonInfo, err := vault.StartDetached(daemonScript, root, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.StopDetached(daemonInfo, control); wipeTestBytes(control) })
	daemonInfo, err = vault.EnsureContainerAPIReachable(daemonInfo, control)
	if err != nil {
		t.Fatal(err)
	}
	vaultStore.ConfigureDetachedDaemon(daemonInfo, control)

	database, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetSecretBackend(vaultStore)
	manager := mcp.NewDockerRuntime(database)
	manager.VaultTokenIssuer = vaultStore
	manager.VaultHandoffHelperPath = helperBinary
	key := state.Key{Source: fmt.Sprintf("browser-replacement-%d", time.Now().UnixNano()), Package: "fixture", Environment: "synthetic", Target: "replacement", MCPDefinition: "authenticated-api"}
	owner, err := database.InstallationID()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = manager.Stop(cleanupCtx, key)
		containers, _ := exec.CommandContext(cleanupCtx, "docker", "container", "ls", "--all", "--quiet", "--filter", "label=aact.key="+key.ID(), "--filter", "label=aact.owner="+owner).Output()
		for _, container := range strings.Fields(string(containers)) {
			_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", container).Run()
		}
	})

	pageListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	pageServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>synthetic login page</body></html>"))
	})}
	go func() { _ = pageServer.Serve(pageListener) }()
	t.Cleanup(func() { _ = pageServer.Close() })
	pageURL := fmt.Sprintf("http://host.docker.internal:%d", pageListener.Addr().(*net.TCPAddr).Port)
	accessConfig, err := json.Marshal(map[string]any{"allowed_urls": []string{pageURL}})
	if err != nil {
		t.Fatal(err)
	}
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostPort := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	spec := mcp.RunSpec{
		Image: mcpImage, RegistrationName: "browser-replacement-initial",
		Args: []string{"sh", "-c", "while [ ! -f /run/aact/vault.json ]; do sleep 0.1; done; exec python3 /app/server.py"},
		Host: "127.0.0.1", HostPort: hostPort, ContainerPort: 8765,
		Transport: "streamable-http", EndpointPath: "/mcp",
		BrowserController: &mcp.BrowserControllerSpec{
			ProfileVolume: volume, AccessConfigJSON: string(accessConfig), StartupURL: pageURL,
			AdapterSource: "def run(credentials, config):\n    return {'status': 'interaction_required'}\n",
		},
	}
	first, err := manager.Start(ctx, key, spec)
	if err != nil {
		t.Fatalf("start first isolated browser-auth MCP: %v", err)
	}
	if _, err := manager.StartBrowser(ctx, key); err != nil {
		t.Fatalf("open initial browser login: %v", err)
	}
	oldHelper := docker("container", "ls", "--quiet", "--filter", "label=aact.browser-controller=1", "--filter", "label=aact.browser-controller.profile-volume="+volume)
	if len(strings.Fields(string(oldHelper))) != 1 {
		t.Fatalf("initial browser login did not create one profile helper: %q", oldHelper)
	}
	docker("container", "stop", "--time", "1", strings.TrimSpace(string(oldHelper)))

	replacementSpec := spec
	replacementSpec.RegistrationName = "browser-replacement-next"
	replacement, err := manager.Start(ctx, key, replacementSpec)
	if err != nil {
		t.Fatalf("replace browser-auth MCP: %v", err)
	}
	if replacement.ID == first.ID || replacement.Status != "running" {
		t.Fatalf("replacement did not create a new running MCP: initial=%+v replacement=%+v", first, replacement)
	}
	viewerURL, err := manager.StartBrowser(ctx, key)
	if err != nil {
		t.Fatalf("replacement MCP could not open its browser login: %v", err)
	}
	if !strings.HasPrefix(viewerURL, "http://localhost:") {
		t.Fatalf("replacement MCP did not open browser login: %q", viewerURL)
	}
	helpers := docker("container", "ls", "--all", "--quiet", "--filter", "label=aact.browser-controller=1", "--filter", "label=aact.browser-controller.profile-volume="+volume)
	if len(strings.Fields(string(helpers))) != 1 {
		t.Fatalf("expected one isolated browser helper on the retained profile volume, found %q", helpers)
	}
}
