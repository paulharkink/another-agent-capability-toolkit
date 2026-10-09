// Package packagehelpers implements native package actions. Interpreter-based
// validation runs in the package's declared container, never on the host.
package packagehelpers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/pelletier/go-toml/v2"
)

type Helper struct {
	Executor   process.Executor
	OnStderr   func([]byte)
	HTTPClient *http.Client
}

func PrepareCluster(ctx context.Context, q mcp.ActionRequest) (mcp.ActionResult, error) {
	q.Action = "prepare"
	return (&Helper{}).Run(ctx, "cluster-inspector", q)
}
func AuthenticateCluster(ctx context.Context, q mcp.ActionRequest) (mcp.ActionResult, error) {
	q.Action = "authenticate"
	return (&Helper{}).Run(ctx, "cluster-inspector", q)
}
func PrepareGrafana(ctx context.Context, q mcp.ActionRequest) (mcp.ActionResult, error) {
	q.Action = "prepare"
	return (&Helper{}).Run(ctx, "grafana-inspector", q)
}
func AuthenticateGrafana(ctx context.Context, q mcp.ActionRequest) (mcp.ActionResult, error) {
	q.Action = "authenticate"
	return (&Helper{}).Run(ctx, "grafana-inspector", q)
}
func PrepareAzure(ctx context.Context, q mcp.ActionRequest) (mcp.ActionResult, error) {
	q.Action = "prepare"
	return (&Helper{}).Run(ctx, "azure-inspector", q)
}
func AuthenticateAzure(ctx context.Context, q mcp.ActionRequest) (mcp.ActionResult, error) {
	q.Action = "authenticate"
	return (&Helper{}).Run(ctx, "azure-inspector", q)
}

func privateWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".aact-action-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func normalize(v any) any {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		f, _ := n.Float64()
		return f
	case map[string]any:
		out := map[string]any{}
		for k, v := range n {
			out[k] = normalize(v)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			out[i] = normalize(v)
		}
		return out
	default:
		return v
	}
}
func set(raw map[string]any, path string, value any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := raw[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			raw[k] = next
		}
		raw = next
	}
	raw[keys[len(keys)-1]] = normalize(value)
}
func table(raw map[string]any, name string) map[string]any {
	v, _ := raw[name].(map[string]any)
	if v == nil {
		v = map[string]any{}
		raw[name] = v
	}
	return v
}
func text(raw map[string]any, name string) string { v, _ := raw[name].(string); return v }
func nativeUser() string {
	if runtime.GOOS != "windows" && os.Getuid() >= 0 {
		return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	}
	return ""
}

func (h *Helper) command(ctx context.Context, args []string, stdin []byte, env map[string]string, secrets []string) ([]byte, error) {
	return h.commandWithStderr(ctx, args, stdin, env, secrets, h.OnStderr)
}

func (h *Helper) commandWithStderr(ctx context.Context, args []string, stdin []byte, env map[string]string, secrets []string, onStderr func([]byte)) ([]byte, error) {
	executor := h.Executor
	if executor == nil {
		executor = process.OSExecutor{}
	}
	var diagnostic bytes.Buffer
	redactor := process.NewRedactor(secrets, func(p []byte) {
		const diagnosticLimit = 8192
		if len(p) >= diagnosticLimit {
			diagnostic.Reset()
			diagnostic.Write(p[len(p)-diagnosticLimit:])
		} else {
			if excess := diagnostic.Len() + len(p) - diagnosticLimit; excess > 0 {
				diagnostic.Next(excess)
			}
			diagnostic.Write(p)
		}
		if onStderr != nil {
			onStderr(p)
		}
	})
	out, err := executor.Run(ctx, append([]string{"docker"}, args...), "", stdin, env, redactor.Write)
	redactor.Flush()
	if err != nil {
		detail := err.Error()
		for _, secret := range secrets {
			if secret != "" {
				detail = strings.ReplaceAll(detail, secret, "[redacted]")
			}
		}
		return nil, fmt.Errorf("Docker %s failed: %s: %s", args[0], detail, strings.TrimSpace(diagnostic.String()))
	}
	return out, nil
}
func (h *Helper) stage(q mcp.ActionRequest, p catalog.Package, directory string) (mcp.ActionRequest, map[string]any, error) {
	raw := normalize(q.Target.Raw).(map[string]any)
	for _, in := range p.Inputs {
		if in.ConfigKey != "" {
			value, ok := q.Inputs[in.Name]
			if !ok && in.Default != nil {
				keys := strings.Split(in.ConfigKey, ".")
				current := any(raw)
				for _, key := range keys {
					m, _ := current.(map[string]any)
					current = m[key]
				}
				if current == nil {
					value = in.Default
					ok = true
				}
			}
			if ok {
				if !in.Required && (value == nil || value == "") {
					removeConfigKey(raw, in.ConfigKey)
				} else {
					set(raw, in.ConfigKey, value)
				}
			}
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return q, nil, err
	}
	// Resolve only declared file resources; retain relative references safely in
	// the staged target directory, and copy selected absolute resources there.
	var copyFiles func(map[string]any) error
	copyFiles = func(values map[string]any) error {
		for key, v := range values {
			if nested, ok := v.(map[string]any); ok {
				if err := copyFiles(nested); err != nil {
					return err
				}
				continue
			}
			if key != "ca_file" && key != "root_cert" && key != "client_cert" && key != "client_key" && key != "repository_context_hook" && key != "command" {
				continue
			}
			path, ok := v.(string)
			if !ok || path == "" {
				continue
			}
			source := path
			if !filepath.IsAbs(source) {
				if q.Target.Path == "" {
					return fmt.Errorf("relative resource %s requires a target TOML", path)
				}
				source = filepath.Join(filepath.Dir(q.Target.Path), path)
			}
			resolved, err := filepath.EvalSymlinks(source)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return err
			}
			dest := path
			if filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
				hash := sha256.Sum256([]byte(resolved))
				dest = filepath.Join("resources", hex.EncodeToString(hash[:])[:12]+"-"+filepath.Base(path))
			}
			abs := filepath.Join(directory, dest)
			if err = privateWrite(abs, data); err != nil {
				return err
			}
			if info, err := os.Stat(resolved); err == nil && info.Mode()&0111 != 0 {
				if err = os.Chmod(abs, 0700); err != nil {
					return err
				}
			}
			values[key] = filepath.ToSlash(dest)
		}
		return nil
	}
	if err := copyFiles(raw); err != nil {
		return q, nil, err
	}
	data, err := toml.Marshal(raw)
	if err != nil {
		return q, nil, err
	}
	if err = privateWrite(filepath.Join(directory, "target.toml"), data); err != nil {
		return q, nil, err
	}
	q.Target.Raw = raw
	q.Target.Path = "/config/target.toml"
	q.StateDir = "/state"
	return q, raw, nil
}
func (h *Helper) Run(ctx context.Context, id string, q mcp.ActionRequest) (mcp.ActionResult, error) {
	if q.ProtocolVersion != 1 {
		return mcp.ActionResult{}, errors.New("unsupported action protocol version")
	}
	if q.Action != "prepare" && q.Action != "authenticate" {
		return mcp.ActionResult{}, errors.New("expected prepare or authenticate")
	}
	p, err := catalog.Load(q.PackageDir)
	if err != nil {
		return mcp.ActionResult{}, err
	}
	if p.ID != id || p.MCP == nil {
		return mcp.ActionResult{}, errors.New("package/action identity mismatch")
	}
	if q.Target.Raw == nil {
		q.Target.Raw = map[string]any{}
	}
	if q.Inputs == nil {
		q.Inputs = map[string]any{}
	}
	if q.StateDir == "" {
		return mcp.ActionResult{}, errors.New("target state directory required")
	}
	q.StateDir, err = filepath.Abs(q.StateDir)
	if err != nil {
		return mcp.ActionResult{}, err
	}
	if id == "azure-inspector" {
		cfg := table(q.Target.Raw, "azure")
		if hints, ok := cfg["resource_hints"].(map[string]any); ok && len(hints) > 0 {
			return mcp.ActionResult{}, errors.New("this Azure image does not support resource-hints")
		}
	}
	if err = os.MkdirAll(q.StateDir, 0700); err != nil {
		return mcp.ActionResult{}, err
	}
	if err = os.Chmod(q.StateDir, 0700); err != nil {
		return mcp.ActionResult{}, err
	}
	stagingDirectory, err := os.MkdirTemp(q.StateDir, ".config-stage-*")
	if err != nil {
		return mcp.ActionResult{}, err
	}
	defer os.RemoveAll(stagingDirectory)
	mapped, raw, err := h.stage(q, p, stagingDirectory)
	if err != nil {
		return mcp.ActionResult{}, err
	}
	finish := func(result mcp.ActionResult, actionErr error) (mcp.ActionResult, error) {
		if actionErr != nil || result.AuthRequired || result.Runtime == nil {
			return result, actionErr
		}
		digest, err := configDigest(stagingDirectory)
		if err != nil {
			return mcp.ActionResult{}, err
		}
		if result.Runtime.Env == nil {
			result.Runtime.Env = map[string]string{}
		}
		result.Runtime.Env["AACT_CONFIG_DIGEST"] = digest
		if err := publishConfig(stagingDirectory, filepath.Join(q.StateDir, "config")); err != nil {
			return mcp.ActionResult{}, err
		}
		return result, nil
	}
	cfg := table(raw, "mcp")
	port := p.MCP.ContainerPort
	switch v := cfg["local_port"].(type) {
	case int:
		port = v
	case int64:
		port = int(v)
	}
	if port < 1024 || port > 65535 {
		return mcp.ActionResult{}, errors.New("local_port must be between1024 and65535")
	}
	host := text(q.Inputs, "host")
	if host == "" {
		host = "127.0.0.1"
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return mcp.ActionResult{}, errors.New("MCP host must be loopback")
	}
	spec := &mcp.RunSpec{Image: p.MCP.Image, Host: host, HostPort: port, ContainerPort: p.MCP.ContainerPort, Transport: p.MCP.Transport, EndpointPath: p.MCP.EndpointPath, User: nativeUser(), Env: map[string]string{}, Mounts: []mcp.Mount{{Source: filepath.Join(q.StateDir, "config"), Destination: "/config", ReadOnly: true}, {Source: q.StateDir, Destination: "/state"}}}
	secrets := []string{}
	for _, field := range p.Inputs {
		if field.Type == "secret" {
			if v := text(q.Inputs, field.Name); v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	if q.ImageSource == "release" && q.ReleaseImage != "" {
		spec.Image = q.ReleaseImage
	} else if p.MCP.BuildContext != "" {
		hash := sha256.Sum256([]byte(p.Dir))
		spec.Image = "aact/" + id + "-" + hex.EncodeToString(hash[:])[:12] + ":local"
		iidFile, fileErr := os.CreateTemp("", "aact-build-iid-*")
		if fileErr != nil {
			return mcp.ActionResult{}, fileErr
		}
		iidPath := iidFile.Name()
		if err = iidFile.Close(); err != nil {
			os.Remove(iidPath)
			return mcp.ActionResult{}, err
		}
		defer os.Remove(iidPath)
		_, buildErr := h.command(ctx, []string{"build", "--progress=plain", "--iidfile", iidPath, "--tag", spec.Image, filepath.Join(p.Dir, p.MCP.BuildContext)}, nil, nil, secrets)
		if buildErr != nil {
			return mcp.ActionResult{}, buildErr
		}
		imageIDBytes, readErr := os.ReadFile(iidPath)
		if readErr != nil {
			return mcp.ActionResult{}, fmt.Errorf("read built image identity: %w", readErr)
		}
		imageID := strings.TrimSpace(string(imageIDBytes))
		decoded, decodeErr := hex.DecodeString(strings.TrimPrefix(imageID, "sha256:"))
		if !strings.HasPrefix(imageID, "sha256:") || len(imageID) != 71 || decodeErr != nil || len(decoded) != 32 {
			return mcp.ActionResult{}, errors.New("Docker build did not produce a valid image identity")
		}
		spec.Image = imageID
	}
	switch id {
	case "cluster-inspector", "grafana-inspector":
		args := []string{"run", "--rm", "--interactive"}
		if spec.User != "" {
			args = append(args, "--user", spec.User)
		}
		for _, mount := range spec.Mounts {
			source := mount.Source
			if mount.Destination == "/config" {
				source = stagingDirectory
			}
			entry := "type=bind,src=" + source + ",dst=" + mount.Destination
			if mount.ReadOnly {
				entry += ",readonly"
			}
			if strings.Contains(mount.Source, ",") {
				return mcp.ActionResult{}, errors.New("mount path contains a comma")
			}
			args = append(args, "--mount", entry)
		}
		if source := text(q.Inputs, "kubeconfig"); source != "" {
			source, err = filepath.Abs(source)
			if err != nil {
				return mcp.ActionResult{}, err
			}
			args = append(args, "--mount", "type=bind,src="+source+",dst=/selected-kubeconfig,readonly")
			mapped.Inputs = map[string]any{}
			for k, v := range q.Inputs {
				mapped.Inputs[k] = v
			}
			mapped.Inputs["kubeconfig"] = "/selected-kubeconfig"
		}
		env := map[string]string{}
		if vault := os.Getenv("VAULT_TOKEN"); vault != "" {
			env["VAULT_TOKEN"] = vault
			secrets = append(secrets, vault)
			args = append(args, "--env", "VAULT_TOKEN")
		}
		mapped.ProtocolVersion = 1
		data, err := json.Marshal(mapped)
		if err != nil {
			return mcp.ActionResult{}, err
		}
		args = append(args, "--entrypoint", "python", spec.Image, "/app/auth.py", q.Action)
		output, err := h.command(ctx, args, data, env, secrets)
		if err != nil {
			return mcp.ActionResult{}, err
		}
		var result mcp.ActionResult
		if err = json.Unmarshal(output, &result); err != nil {
			return result, fmt.Errorf("container action returned invalid JSON: %w", err)
		}
		if result.AuthRequired {
			return result, nil
		}
		if id == "cluster-inspector" {
			spec.Env["CLUSTER_INSPECTOR_CONFIG"] = "/config/target.toml"
			spec.Env["CLUSTER_INSPECTOR_TARGET"] = q.Target.Name
			spec.Env["CLUSTER_INSPECTOR_STATE"] = "/state"
			v := q.Inputs["connections"]
			switch a := v.(type) {
			case []any:
				ss := []string{}
				for _, s := range a {
					if value, ok := s.(string); ok {
						ss = append(ss, value)
					}
				}
				spec.Env["CLUSTER_INSPECTOR_CONNECTIONS"] = strings.Join(ss, ",")
			case []string:
				spec.Env["CLUSTER_INSPECTOR_CONNECTIONS"] = strings.Join(a, ",")
			case string:
				spec.Env["CLUSTER_INSPECTOR_CONNECTIONS"] = a
			}
		} else {
			spec.Env["GRAFANA_CONFIG"] = "/config/target.toml"
			spec.Env["GRAFANA_TARGET"] = q.Target.Name
			spec.Env["MCP_ENVIRONMENT"] = q.Target.Environment
			spec.Env["GRAFANA_AUTH_FILE"] = "/state/auth.json"
		}
		result.Runtime = spec
		return finish(result, nil)
	case "azure-inspector":
		result, err := h.azure(ctx, q, raw, spec, secrets)
		return finish(result, err)
	default:
		return mcp.ActionResult{}, errors.New("unsupported inspector package")
	}
}

func configDigest(directory string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", filepath.ToSlash(relative), info.Mode().Perm(), len(data))
		hash.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (h *Helper) azure(ctx context.Context, q mcp.ActionRequest, raw map[string]any, spec *mcp.RunSpec, secrets []string) (mcp.ActionResult, error) {
	cfg := table(raw, "azure")
	tenant, subscription := text(cfg, "tenant_id"), text(cfg, "subscription_id")
	if tenant == "" || subscription == "" {
		return mcp.ActionResult{}, errors.New("Azure tenant_id and subscription_id are required")
	}
	spec.Env = map[string]string{"AZMCP_TRANSPORT": "sse", "AZMCP_PORT": "8084", "AZURE_CONFIG_DIR": "/state", "HOME": "/tmp"}
	spec.Args = []string{"server", "start"}
	base := []string{"run", "--rm"}
	if spec.User != "" {
		base = append(base, "--user", spec.User)
	}
	base = append(base, "--mount", "type=bind,src="+q.StateDir+",dst=/state", "--env", "AZURE_CONFIG_DIR=/state", "--env", "HOME=/tmp", "--entrypoint", "az", spec.Image)
	call := func(a ...string) error {
		_, err := h.command(ctx, append(append([]string{}, base...), a...), nil, nil, secrets)
		return err
	}
	if err := call("config", "set", "core.login_experience_v2=off"); err != nil {
		return mcp.ActionResult{}, err
	}
	var probeOutput bytes.Buffer
	_, probeErr := h.commandWithStderr(ctx, append(append([]string{}, base...), "account", "show", "--subscription", subscription, "--output", "none"), nil, nil, secrets, func(p []byte) { probeOutput.Write(p) })
	if probeErr != nil {
		output := strings.TrimSpace(probeOutput.String())
		if !azureLoginRequired(output) {
			if h.OnStderr != nil && probeOutput.Len() > 0 {
				h.OnStderr(probeOutput.Bytes())
			}
			return mcp.ActionResult{}, probeErr
		}
		if q.Action != "authenticate" || !q.Interactive {
			return mcp.ActionResult{AuthRequired: true, Diagnostic: "Azure CLI account probe requires authentication: " + output}, nil
		}
		argv := append(append([]string{"docker"}, base...), "login", "--use-device-code", "--tenant", tenant, "--output", "none")
		if h.Executor == nil {
			command := exec.CommandContext(ctx, argv[0], argv[1:]...)
			command.Stdin = os.Stdin
			command.Stdout = callbackProgress{h.OnStderr}
			command.Stderr = callbackProgress{h.OnStderr}
			if err := command.Run(); err != nil {
				return mcp.ActionResult{}, errors.New("Azure device-code authentication failed")
			}
		} else {
			out, e := h.Executor.Run(ctx, argv, "", nil, nil, h.OnStderr)
			if e != nil {
				return mcp.ActionResult{}, e
			}
			if h.OnStderr != nil {
				h.OnStderr(out)
			}
		}
	}
	if err := call("account", "set", "--subscription", subscription, "--output", "none"); err != nil {
		return mcp.ActionResult{}, err
	}
	return mcp.ActionResult{Runtime: spec}, nil
}

func azureLoginRequired(output string) bool {
	message := strings.ToLower(output)
	return strings.Contains(message, "az login") || strings.Contains(message, "not logged in")
}

type callbackProgress struct{ callback func([]byte) }

func (w callbackProgress) Write(p []byte) (int, error) {
	if w.callback != nil {
		w.callback(p)
	}
	return len(p), nil
}

func removeConfigKey(raw map[string]any, path string) {
	keys := strings.Split(path, ".")
	for _, key := range keys[:len(keys)-1] {
		next, ok := raw[key].(map[string]any)
		if !ok {
			return
		}
		raw = next
	}
	delete(raw, keys[len(keys)-1])
}
func publishConfig(staged, stable string) error {
	if _, err := os.Stat(stable); os.IsNotExist(err) {
		return os.Rename(staged, stable)
	} else if err != nil {
		return err
	}
	backup, err := os.MkdirTemp(filepath.Dir(stable), ".config-previous-*")
	if err != nil {
		return err
	}
	if err = os.Remove(backup); err != nil {
		return err
	}
	if err = os.Rename(stable, backup); err != nil {
		return err
	}
	if err = os.Rename(staged, stable); err != nil {
		os.Rename(backup, stable)
		return err
	}
	return os.RemoveAll(backup)
}
