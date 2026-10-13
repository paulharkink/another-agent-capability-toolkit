package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArgoImageContextIncludesCLIInventory(t *testing.T) {
	root := filepath.Join("..", "..", "packages", "argocd", "mcp")
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "COPY server.py operation_inventory.json ./") {
		t.Fatal("Argo MCP base image must include the operation inventory required by selected_cli_version")
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

// TestArgoVariantContainsSelectedCLIForLocalAndReleaseBases is opt-in because
// it builds four owned images and contacts the local Docker daemon and public
// Argo CD release assets. It never uses a published AACT image or live server.
func TestArgoVariantContainsSelectedCLIForLocalAndReleaseBases(t *testing.T) {
	if os.Getenv("AACT_TEST_ARGOCD_VARIANT_DOCKER") != "1" {
		t.Skip("set AACT_TEST_ARGOCD_VARIANT_DOCKER=1 to build owned Argo CLI variant images")
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
	fixtureBytes, err := os.ReadFile(filepath.Join(mcpRoot, "tests", "testdata", "image-variant-versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture argoImageFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Versions) != 2 {
		t.Fatalf("expected exactly two pinned CLI releases, got %d", len(fixture.Versions))
	}
	serverSource, err := os.ReadFile(filepath.Join(mcpRoot, "server.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serverSource), `CLI = "/usr/local/bin/argocd"`) || !strings.Contains(string(serverSource), "run_cli([CLI, *argv") {
		t.Fatal("server/action operation resolver does not use the selected CLI path")
	}
	variantDockerfile := filepath.Join(mcpRoot, "Dockerfile.variant")
	if _, err := os.Stat(variantDockerfile); err != nil {
		t.Fatal(err)
	}
	unique, err := randomDockerSuffix()
	if err != nil {
		t.Fatal(err)
	}
	imagePrefix := "aact-task8-argocd-" + unique
	localBase := imagePrefix + ":local-base"
	releaseBase := "ghcr.io/aact-task8/argocd-mcp:v" + unique
	images := []string{localBase, releaseBase}
	containerNames := []string{}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		for _, name := range containerNames {
			_, _ = runOwnedDocker(cleanupCtx, t, "rm", "--force", name)
		}
		for _, image := range images {
			_, _ = runOwnedDocker(cleanupCtx, t, "image", "rm", "--force", image)
		}
	})
	if output, err := runOwnedDocker(ctx, t, "build", "--file", filepath.Join(mcpRoot, "Dockerfile"), "--tag", localBase, mcpRoot); err != nil {
		t.Fatalf("build owned local FastMCP base: %v\n%s", err, output)
	}
	if output, err := runOwnedDocker(ctx, t, "image", "tag", localBase, releaseBase); err != nil {
		t.Fatalf("create locally owned release-style base tag: %v\n%s", err, output)
	}

	evidenceRoot := os.Getenv("AACT_TASK8_EVIDENCE_DIR")
	if evidenceRoot == "" {
		evidenceRoot, err = os.MkdirTemp("", "aact-task8-image-evidence-")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("exact image version/help outputs: %s", evidenceRoot)
	} else if err := os.MkdirAll(evidenceRoot, 0700); err != nil {
		t.Fatal(err)
	}

	for _, base := range []struct{ name, tag string }{{"local", localBase}, {"release-style", releaseBase}} {
		for _, selected := range fixture.Versions {
			version := selected.Version
			checksum, ok := selected.SHA256[platform.Arch]
			if !ok {
				t.Fatalf("fixture has no official %s checksum for Argo CD %s", platform.Arch, version)
			}
			image := imagePrefix + ":" + base.name + "-v" + version
			images = append(images, image)
			buildOutput, buildErr := runOwnedDocker(ctx, t,
				"build", "--file", variantDockerfile, "--tag", image,
				"--build-arg", "CLI_VERSION="+version,
				"--build-arg", "AACT_BASE_IMAGE="+base.tag,
				"--platform", "linux/"+platform.Arch, mcpRoot,
			)
			if buildErr != nil {
				t.Fatalf("build %s variant for Argo CD %s: %v\n%s", base.name, version, buildErr, buildOutput)
			}
			containerName := "aact-task8-" + unique + "-" + base.name + "-" + strings.ReplaceAll(version, ".", "-")
			containerNames = append(containerNames, containerName)
			result, err := runImageInventory(ctx, t, image, containerName, version, checksum, platform.Arch, evidenceRoot, base.name)
			if err != nil {
				t.Fatalf("verify %s Argo CD %s image: %v", base.name, version, err)
			}
			t.Logf("%s Argo CD %s: %d executable commands, %d command paths; version=%q; evidence=%s", base.name, version, result.ExecutableCommands, result.CommandPaths, result.VersionOutput, filepath.Join(evidenceRoot, base.name+"-v"+version))
		}
	}
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

func runImageInventory(ctx context.Context, t *testing.T, image, containerName, version, checksum, arch, evidenceRoot, baseName string) (imageInventoryResult, error) {
	t.Helper()
	args := []string{"run", "--rm", "--name", containerName, "--network", "none", "--memory", "512m", "--cpus", "1", "--pids-limit", "64", "--read-only", "--tmpfs", "/tmp:size=64m", "--env", "AACT_ARGOCD_READ_ONLY=true", "--entrypoint", "python", image, "-c", selectedImageInventoryScript, version, checksum}
	output, err := runOwnedDocker(ctx, t, args...)
	if err != nil {
		return imageInventoryResult{}, fmt.Errorf("container inventory verification failed: %w\n%s", err, output)
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
