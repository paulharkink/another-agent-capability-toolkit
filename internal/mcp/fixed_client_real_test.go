package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func TestArgoImageContextIncludesCLIInventory(t *testing.T) {
	root := filepath.Join("..", "..", "packages", "argocd", "mcp")
	manifest, err := os.ReadFile(filepath.Join("..", "..", "packages", "argocd", "package.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "cli_version") || strings.Contains(string(manifest), "[mcp.image_variant]") {
		t.Fatal("Argo package must use one fixed CLI through the standard package image")
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "COPY server.py operation_inventory.json ./") {
		t.Fatal("Argo MCP image must include the operation inventory used by runtime discovery")
	}
	for _, required := range []string{"3.2.0", "verify-cli-checksum.sh", "cli_checksums.txt"} {
		if !strings.Contains(string(dockerfile), required) {
			t.Errorf("standard Argo MCP Dockerfile must include %q", required)
		}
	}
	if strings.Contains(string(dockerfile), "ARG CLI_VERSION") || strings.Contains(string(dockerfile), "latest") {
		t.Fatal("standard Argo MCP Dockerfile must pin its CLI version without a build argument or latest")
	}
	if _, err := os.Stat(filepath.Join(root, "operation_inventory.json")); err != nil {
		t.Fatal(err)
	}
}

type argoImageFixture struct {
	Versions []struct {
		Version string            `json:"version"`
		SHA256  map[string]string `json:"sha256"`
	} `json:"versions"`
}

// TestArgoFixedClientStandardImage is opt-in because it builds one owned
// package image and contacts the local Docker daemon and official release
// assets. Its release-style tag is an owned local parity fixture; it never
// accesses a published AACT image or live Argo CD server.
func TestArgoFixedClientStandardImage(t *testing.T) {
	if os.Getenv("AACT_TEST_ARGOCD_DOCKER") != "1" {
		t.Skip("set AACT_TEST_ARGOCD_DOCKER=1 to build and inspect the standard Argo MCP image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, err := requireLocalDockerEndpoint(); err != nil {
		t.Fatal(err)
	}
	dockerInfo, err := runOwnedDocker(ctx, t, "info", "--format", "{{.OSType}}/{{.Architecture}}")
	if err != nil {
		t.Fatalf("read local Docker platform: %v\n%s", err, dockerInfo)
	}
	platform, err := dockerPlatform(strings.TrimSpace(string(dockerInfo)))
	if err != nil {
		t.Fatal(err)
	}

	packageRoot := filepath.Join("..", "..", "packages", "argocd")
	mcpRoot := filepath.Join(packageRoot, "mcp")
	packageDefinition, err := catalog.Load(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if packageDefinition.MCP == nil || packageDefinition.MCP.BuildContext != "./mcp" || packageDefinition.MCP.ReleaseImage == "" {
		t.Fatalf("Argo package does not select its standard MCP image path: %#v", packageDefinition.MCP)
	}
	if !strings.Contains(packageDefinition.MCP.ReleaseImage, "{aact_version}") {
		t.Fatalf("expected package release image template for locally owned parity tag, got %q", packageDefinition.MCP.ReleaseImage)
	}
	fixtureBytes, err := os.ReadFile(filepath.Join(mcpRoot, "tests", "testdata", "fixed-client-checksum.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture argoImageFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Versions) != 1 || fixture.Versions[0].Version != "3.2.0" {
		t.Fatalf("expected the single fixed CLI release v3.2.0, got %#v", fixture.Versions)
	}
	serverSource, err := os.ReadFile(filepath.Join(mcpRoot, "server.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serverSource), `CLI = "/usr/local/bin/argocd"`) || !strings.Contains(string(serverSource), "run_cli([CLI, *argv") {
		t.Fatal("server/action operation resolver does not use the selected CLI path")
	}
	unique, err := randomDockerSuffix()
	if err != nil {
		t.Fatal(err)
	}
	releaseStyleImage := strings.ReplaceAll(packageDefinition.MCP.ReleaseImage, "{aact_version}", "local-"+unique)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewDockerRuntime(store)
	runtime.Executor = boundedRuntimeDockerExecutor{}
	runtime.SkipHealth = true
	started := map[state.Key]bool{}
	ownedImages := []string{}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		for key, isStarted := range started {
			if isStarted {
				_ = runtime.Stop(cleanupCtx, key)
			}
		}
		for _, image := range ownedImages {
			_, _ = runOwnedDocker(cleanupCtx, t, "image", "rm", "--force", image)
		}
	})
	evidenceRoot := os.Getenv("AACT_ARGOCD_EVIDENCE_DIR")
	if evidenceRoot == "" {
		evidenceRoot, err = os.MkdirTemp("", "aact-argocd-fixed-image-evidence-")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("exact image version/help outputs: %s", evidenceRoot)
	} else if err := os.MkdirAll(evidenceRoot, 0700); err != nil {
		t.Fatal(err)
	}

	selected := fixture.Versions[0]
	checksum, ok := selected.SHA256[platform.Arch]
	if !ok {
		t.Fatalf("fixture has no official %s checksum for Argo CD %s", platform.Arch, selected.Version)
	}
	sourceHostPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	sourceKey := state.Key{Source: "argocd-fixed-test", Package: "argocd", Target: "source", Profile: unique, MCP: "source"}
	sourceImage := "aact/" + sourceKey.ID()[:24] + ":local"
	ownedImages = append(ownedImages, sourceImage, releaseStyleImage)
	sourceSpec := fixedArgoRunSpec(mcpRoot, "", sourceHostPort, store.Root(), "aact-argocd-fixed-"+unique+"-source")
	sourceInstance, err := runtime.Start(ctx, sourceKey, sourceSpec)
	if err != nil {
		t.Fatalf("start standard Argo source package through AACT Runtime.Start: %v", err)
	}
	started[sourceKey] = true
	if output, err := runOwnedDocker(ctx, t, "image", "tag", sourceImage, releaseStyleImage); err != nil {
		t.Fatalf("create locally owned package release-style tag: %v\n%s", err, output)
	}
	for _, image := range []struct {
		name     string
		key      state.Key
		tag      string
		instance Instance
	}{
		{name: "source", key: sourceKey, tag: sourceImage, instance: sourceInstance},
	} {
		result, err := runStartedContainerInventory(ctx, t, image.instance.ID, selected.Version, checksum, evidenceRoot, image.name)
		if err != nil {
			t.Fatalf("verify standard Argo MCP %s runtime: %v", image.name, err)
		}
		t.Logf("%s package image tag Argo CD %s: %d executable commands, %d command paths; version=%q; evidence=%s", image.name, selected.Version, result.ExecutableCommands, result.CommandPaths, result.VersionOutput, filepath.Join(evidenceRoot, image.name+"-v"+selected.Version))
		if err := runtime.Stop(ctx, image.key); err != nil {
			t.Fatalf("stop owned standard Argo %s runtime: %v", image.name, err)
		}
		started[image.key] = false
	}
	releaseHostPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	releaseKey := state.Key{Source: "argocd-fixed-test", Package: "argocd", Target: "release", Profile: unique, MCP: "release"}
	releaseSpec := fixedArgoRunSpec("", releaseStyleImage, releaseHostPort, store.Root(), "aact-argocd-fixed-"+unique+"-release")
	releaseInstance, err := runtime.Start(ctx, releaseKey, releaseSpec)
	if err != nil {
		t.Fatalf("start locally owned release-style image through AACT Runtime.Start: %v", err)
	}
	started[releaseKey] = true
	result, err := runStartedContainerInventory(ctx, t, releaseInstance.ID, selected.Version, checksum, evidenceRoot, "release-style-local")
	if err != nil {
		t.Fatalf("verify standard Argo release-style runtime: %v", err)
	}
	t.Logf("locally owned release-style package tag Argo CD %s: %d executable commands, %d command paths; version=%q; evidence=%s", selected.Version, result.ExecutableCommands, result.CommandPaths, result.VersionOutput, filepath.Join(evidenceRoot, "release-style-local-v"+selected.Version))
	if err := runtime.Stop(ctx, releaseKey); err != nil {
		t.Fatalf("stop owned standard Argo release-style runtime: %v", err)
	}
	started[releaseKey] = false
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func fixedArgoRunSpec(buildContext, image string, hostPort int, stateDir, registrationName string) RunSpec {
	return RunSpec{
		Image: image, BuildContext: buildContext, Env: map[string]string{
			"AACT_ARGOCD_SERVER": "127.0.0.1", "AACT_ARGOCD_READ_ONLY": "true", "AACT_ARGOCD_AUTH_MODE": "sso",
		},
		Mounts: []Mount{{Source: stateDir, Destination: "/state"}},
		Host:   "127.0.0.1", HostPort: hostPort, ContainerPort: 8765,
		Transport: "streamable-http", EndpointPath: "/mcp", RegistrationName: registrationName,
	}
}

type boundedRuntimeDockerExecutor struct{}

func (boundedRuntimeDockerExecutor) Run(ctx context.Context, argv []string, cwd string, stdin []byte, env map[string]string, onStderr func([]byte)) ([]byte, error) {
	if len(argv) >= 2 && argv[0] == "docker" && argv[1] == "run" {
		limits := []string{"--memory", "512m", "--cpus", "1", "--pids-limit", "64", "--read-only", "--tmpfs", "/tmp:size=64m"}
		argv = append(append(append([]string{}, argv[:2]...), limits...), argv[2:]...)
	}
	return (process.OSExecutor{}).Run(ctx, argv, cwd, stdin, env, onStderr)
}

type dockerPlatformInfo struct{ Arch string }

func dockerPlatform(value string) (dockerPlatformInfo, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] != "linux" {
		return dockerPlatformInfo{}, fmt.Errorf("expected a local Linux Docker engine, got %q", value)
	}
	arch := parts[1]
	switch arch {
	case "aarch64":
		arch = "arm64"
	case "x86_64":
		arch = "amd64"
	}
	if arch != "amd64" && arch != "arm64" {
		return dockerPlatformInfo{}, fmt.Errorf("unsupported local Docker architecture %q", arch)
	}
	return dockerPlatformInfo{Arch: arch}, nil
}

func randomDockerSuffix() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func requireLocalDockerEndpoint() (string, error) {
	configDir := os.Getenv("DOCKER_CONFIG")
	if configDir == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Docker configuration directory: %w", err)
		}
		configDir = filepath.Join(userHome, ".docker")
	}
	config := struct {
		CurrentContext string `json:"currentContext"`
	}{}
	if data, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return "", fmt.Errorf("parse local Docker context configuration: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read local Docker context configuration: %w", err)
	}
	contextName := os.Getenv("DOCKER_CONTEXT")
	var endpoint string
	if contextName != "" {
		endpoint = dockerContextEndpoint(configDir, contextName)
	} else if host := os.Getenv("DOCKER_HOST"); host != "" {
		endpoint = host
	} else {
		contextName = config.CurrentContext
		if contextName == "" || contextName == "default" {
			endpoint = "unix:///var/run/docker.sock"
		} else {
			endpoint = dockerContextEndpoint(configDir, contextName)
		}
	}
	if !strings.HasPrefix(endpoint, "unix:///") {
		return "", fmt.Errorf("refusing Docker daemon command: effective endpoint for context %q is not a local Unix socket (%q)", contextName, endpoint)
	}
	return endpoint, nil
}

func dockerContextEndpoint(configDir, wanted string) string {
	metadata, err := filepath.Glob(filepath.Join(configDir, "contexts", "meta", "*", "meta.json"))
	if err != nil {
		return ""
	}
	for _, path := range metadata {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var value struct {
			Name      string `json:"Name"`
			Endpoints struct {
				Docker struct {
					Host string `json:"Host"`
				} `json:"docker"`
			} `json:"Endpoints"`
		}
		if json.Unmarshal(data, &value) == nil && (value.Name == wanted || filepath.Base(value.Name) == wanted) {
			return value.Endpoints.Docker.Host
		}
	}
	return ""
}

func runOwnedDocker(ctx context.Context, t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	if _, err := requireLocalDockerEndpoint(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

type imageInventoryResult struct {
	VersionOutput      string `json:"version_output"`
	ExecutableCommands int    `json:"executable_commands"`
	CommandPaths       int    `json:"command_paths"`
}

const selectedImageInventoryScript = `import asyncio, hashlib, inspect, json, os, re, shutil, subprocess, sys
sys.path.insert(0, "/app")
import server
cli = "/usr/local/bin/argocd"
if server.CLI != cli:
    raise SystemExit(f"server selected unexpected CLI path: {server.CLI!r}")
resolver = inspect.getsource(server._run_operation_tool)
if "run_cli([CLI, *argv" not in resolver:
    raise SystemExit("operation resolver does not execute the selected CLI path")
if "CLI" not in inspect.getsource(server.run_auth_worker) or "[CLI," not in inspect.getsource(server._identity):
    raise SystemExit("authentication resolver does not execute the selected CLI path")
path_hits = []
for directory in os.environ.get("PATH", "").split(os.pathsep):
    candidate = os.path.join(directory, "argocd")
    if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
        path_hits.append(os.path.realpath(candidate))
if sorted(set(path_hits)) != [cli]:
    raise SystemExit(f"unexpected executable Argo CD CLI paths in PATH: {sorted(set(path_hits))!r}")
actual_hash = hashlib.sha256(open(cli, "rb").read()).hexdigest()
if actual_hash != sys.argv[2]:
    raise SystemExit(f"selected CLI checksum {actual_hash} != official checksum {sys.argv[2]}")
version_proc = subprocess.run([cli, "version", "--client"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
version_output = (version_proc.stdout + version_proc.stderr).decode("utf-8", "replace")
match = re.search(r"argocd:\s*v?(\d+\.\d+\.\d+)(?:[+ ].*)?$", version_output, re.M)
if version_proc.returncode != 0 or not match or match.group(1) != sys.argv[1]:
    raise SystemExit(f"selected CLI version output does not match {sys.argv[1]}: {version_output!r}")
selected = server.selected_cli_version()
if selected != sys.argv[1]:
    raise SystemExit(f"server selected inventory for {selected}, expected {sys.argv[1]}")
tools = asyncio.run(server.mcp.list_tools())
if "argocd_app_get" not in {tool.name for tool in tools}:
    raise SystemExit("server startup did not register argocd_app_get from the selected inventory")
inventory = server._operation_inventory()["versions"][selected]
raw_help = {}
normalized_defaults = []
seen = {}
queue = [()]
index = 0
def run_help(path):
    global index
    proc = subprocess.run([cli, *path, "--help"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    if proc.returncode != 0:
        raise SystemExit(f"help failed for {' '.join(path) or '<root>'}: {proc.returncode}")
    key = " ".join(path)
    raw_help[key] = {"stdout": proc.stdout.decode("utf-8", "replace"), "stderr": proc.stderr.decode("utf-8", "replace")}
    index += 1
    return (proc.stdout + proc.stderr).decode("utf-8", "replace")
def command_rows(text):
    lines = text.splitlines()
    try:
        start = lines.index("Available Commands:") + 1
    except ValueError:
        return []
    rows = []
    for line in lines[start:]:
        if not line.strip():
            break
        match = re.match(r"^\s+([A-Za-z0-9][A-Za-z0-9-]*)\s+\S", line)
        if match:
            rows.append(match.group(1))
    return rows
def flag_names(text):
    names = set()
    in_flags = False
    for line in text.splitlines():
        if line.strip() in ("Flags:", "Global Flags:"):
            in_flags = True
            continue
        if in_flags and not line.strip():
            in_flags = False
            continue
        if in_flags:
            match = re.match(r"^\s*(?:-[A-Za-z],\s*)?(--[A-Za-z0-9][A-Za-z0-9-]*)\b", line)
            if match:
                names.add(match.group(1))
    return names
while queue:
    path = queue.pop(0)
    key = " ".join(path)
    if key in seen:
        continue
    text = run_help(path)
    seen[key] = {"flags": sorted(flag_names(text)), "children": command_rows(text)}
    for child in seen[key]["children"]:
        queue.append(path + (child,))
expected = {entry["path"]: entry for entry in inventory["commands"]}
observed_paths = set(seen) - {""}
if observed_paths != set(expected):
    raise SystemExit(f"command path mismatch; missing={sorted(set(expected)-observed_paths)!r}; extra={sorted(observed_paths-set(expected))!r}")
flag_mismatches = {}
for path, entry in expected.items():
    want = {flag["name"] for flag in entry["flags"]}
    got = set(seen[path]["flags"])
    if want != got:
        flag_mismatches[path] = {"missing": sorted(want-got), "extra": sorted(got-want)}
if flag_mismatches:
    raise SystemExit("flag mismatch: " + json.dumps(flag_mismatches, sort_keys=True))
expected_executable = {path for path, entry in expected.items() if not entry["children"]}
actual_executable = {path for path, entry in seen.items() if not entry["children"]}
if actual_executable != expected_executable:
    raise SystemExit(f"executable command mismatch; missing={sorted(expected_executable-actual_executable)!r}; extra={sorted(actual_executable-expected_executable)!r}")
for path, text in ((key, value["stdout"] + value["stderr"]) for key, value in raw_help.items()):
    for line in text.splitlines():
        if "(default " in line:
            normalized = line.replace(os.path.expanduser("~"), "<home>")
            normalized_defaults.append({"path": path, "raw": line, "normalized": normalized})
print(json.dumps({"version_output": version_output, "selected_version": selected, "sha256": actual_hash,
                  "executable_commands": len(actual_executable), "command_paths": len(observed_paths),
                  "help": raw_help, "normalized_defaults": normalized_defaults}, separators=(",", ":")))
`

func runStartedContainerInventory(ctx context.Context, t *testing.T, containerID, version, checksum, evidenceRoot, baseName string) (imageInventoryResult, error) {
	t.Helper()
	output, err := runOwnedDocker(ctx, t, "exec", containerID, "python", "-c", selectedImageInventoryScript, version, checksum)
	if err != nil {
		return imageInventoryResult{}, fmt.Errorf("started-container inventory verification failed: %w\n%s", err, output)
	}
	var report struct {
		VersionOutput      string `json:"version_output"`
		SelectedVersion    string `json:"selected_version"`
		SHA256             string `json:"sha256"`
		ExecutableCommands int    `json:"executable_commands"`
		CommandPaths       int    `json:"command_paths"`
		Help               map[string]struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		} `json:"help"`
		NormalizedDefaults []struct {
			Path       string `json:"path"`
			Raw        string `json:"raw"`
			Normalized string `json:"normalized"`
		} `json:"normalized_defaults"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		return imageInventoryResult{}, fmt.Errorf("decode image inventory report: %w\n%s", err, output)
	}
	if report.SelectedVersion != version || report.SHA256 != checksum {
		return imageInventoryResult{}, fmt.Errorf("selected version/hash mismatch: %s/%s", report.SelectedVersion, report.SHA256)
	}
	dir := filepath.Join(evidenceRoot, baseName+"-v"+version)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return imageInventoryResult{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "version.stdout.txt"), []byte(report.VersionOutput), 0600); err != nil {
		return imageInventoryResult{}, err
	}
	for path, output := range report.Help {
		name := "help-root"
		if path != "" {
			name = "help-" + strings.NewReplacer(" ", "_", "/", "_", "\\", "_").Replace(path)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".stdout.txt"), []byte(output.Stdout), 0600); err != nil {
			return imageInventoryResult{}, err
		}
		if err := os.WriteFile(filepath.Join(dir, name+".stderr.txt"), []byte(output.Stderr), 0600); err != nil {
			return imageInventoryResult{}, err
		}
	}
	defaults, err := json.MarshalIndent(report.NormalizedDefaults, "", "  ")
	if err != nil {
		return imageInventoryResult{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "normalized-help-defaults.json"), defaults, 0600); err != nil {
		return imageInventoryResult{}, err
	}
	return imageInventoryResult{VersionOutput: report.VersionOutput, ExecutableCommands: report.ExecutableCommands, CommandPaths: report.CommandPaths}, nil
}
