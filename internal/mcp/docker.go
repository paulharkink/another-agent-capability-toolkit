package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Mount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only"`
}
type RunSpec struct {
	User          string            `json:"user,omitempty"`
	Image         string            `json:"image"`
	BuildContext  string            `json:"build_context,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	SecretEnv     map[string]string `json:"secret_env,omitempty"`
	Mounts        []Mount           `json:"mounts,omitempty"`
	Host          string            `json:"host"`
	HostPort      int               `json:"host_port"`
	ContainerPort int               `json:"container_port"`
	Transport     string            `json:"transport"`
	EndpointPath  string            `json:"endpoint_path"`
}
type Instance struct {
	Key    state.Key `json:"key"`
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	URL    string    `json:"url"`
}
type Runtime struct {
	Store      *state.Store
	Executor   process.Executor
	OnStderr   func([]byte)
	SkipHealth bool
}

func NewDockerRuntime(s *state.Store) *Runtime {
	return &Runtime{Store: s, Executor: process.OSExecutor{}}
}
func containerName(k state.Key) string { return "aact-" + k.ID()[:24] }
func specURL(s RunSpec) string {
	h := s.Host
	if h == "" {
		h = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(h, strconv.Itoa(s.HostPort)) + s.EndpointPath
}
func labels(k state.Key, s RunSpec) map[string]string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return map[string]string{"aact.managed": "1", "aact.key": k.ID(), "aact.source": k.Source, "aact.package": k.Package, "aact.environment": k.Environment, "aact.target": k.Target, "aact.url": specURL(s), "aact.spec": hex.EncodeToString(h[:])}
}

type dockerConfig struct {
	Labels map[string]string `json:"Labels"`
}
type dockerState struct {
	Running bool   `json:"Running"`
	Status  string `json:"Status"`
}
type dockerInfo struct {
	ID     string       `json:"Id"`
	Name   string       `json:"Name"`
	Config dockerConfig `json:"Config"`
	State  dockerState  `json:"State"`
}

func (r *Runtime) run(ctx context.Context, args []string) ([]byte, error) {
	var diagnostics bytes.Buffer
	b, e := r.Executor.Run(ctx, append([]string{"docker"}, args...), "", nil, nil, func(p []byte) {
		if diagnostics.Len() < 8192 {
			diagnostics.Write(p)
		}
		if r.OnStderr != nil {
			r.OnStderr(p)
		}
	})
	if e != nil {
		return b, fmt.Errorf("Docker %s: %w: %s", args[0], e, strings.TrimSpace(diagnostics.String()))
	}
	return b, nil
}
func (r *Runtime) inspect(ctx context.Context, name string) (dockerInfo, error) {
	b, e := r.run(ctx, []string{"inspect", name})
	if e != nil {
		return dockerInfo{}, e
	}
	var v []dockerInfo
	if e = json.Unmarshal(b, &v); e != nil {
		return dockerInfo{}, e
	}
	if len(v) != 1 {
		return dockerInfo{}, errors.New("Docker inspect expected one container")
	}
	return v[0], nil
}
func owned(d dockerInfo, k state.Key) bool {
	return d.Config.Labels["aact.managed"] == "1" && d.Config.Labels["aact.key"] == k.ID() && d.Config.Labels["aact.source"] == k.Source
}
func (r *Runtime) Start(ctx context.Context, k state.Key, s RunSpec) (Instance, error) {
	secretValues := []string{}
	for _, v := range s.SecretEnv {
		if v != "" {
			secretValues = append(secretValues, v)
		}
	}
	local := *r
	local.Executor = redactingExecutor{base: r.Executor, values: secretValues}
	r = &local
	if s.Host == "" {
		s.Host = "127.0.0.1"
	}
	if s.Host != "127.0.0.1" && s.Host != "localhost" && s.Host != "::1" {
		return Instance{}, errors.New("MCP host must be loopback")
	}
	if s.HostPort < 1 || s.HostPort > 65535 || s.ContainerPort < 1 || s.ContainerPort > 65535 {
		return Instance{}, errors.New("MCP port outside 1..65535")
	}
	if s.Image == "" && s.BuildContext == "" {
		return Instance{}, errors.New("MCP image or build context required")
	}
	name := containerName(k)
	expected := labels(k, s)
	if old, e := r.inspect(ctx, name); e == nil {
		if !owned(old, k) {
			return Instance{}, errors.New("container name belongs to an unmanaged container")
		}
		if old.State.Running {
			if old.Config.Labels["aact.spec"] != expected["aact.spec"] {
				return Instance{}, errors.New("MCP is running with different settings; stop it before starting again")
			}
			return instance(old), nil
		}
		if _, e = r.run(ctx, []string{"rm", old.ID}); e != nil {
			return Instance{}, e
		}
	}
	image := s.Image
	if s.BuildContext != "" {
		image = "aact/" + k.ID()[:24] + ":local"
		if _, e := r.run(ctx, []string{"build", "--tag", image, s.BuildContext}); e != nil {
			return Instance{}, e
		}
	}
	a := []string{"run", "--detach", "--name", name}
	if s.User != "" {
		a = append(a, "--user", s.User)
	}
	names := make([]string, 0, len(expected))
	for n := range expected {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a = append(a, "--label", n+"="+expected[n])
	}
	a = append(a, "--publish", net.JoinHostPort(s.Host, strconv.Itoa(s.HostPort))+":"+strconv.Itoa(s.ContainerPort))
	for _, m := range s.Mounts {
		if strings.Contains(m.Source, ",") || strings.Contains(m.Destination, ",") {
			return Instance{}, errors.New("Docker mount paths cannot contain commas")
		}
		v := "type=bind,src=" + m.Source + ",dst=" + m.Destination
		if m.ReadOnly {
			v += ",readonly"
		}
		a = append(a, "--mount", v)
	}
	if len(s.Env)+len(s.SecretEnv) > 0 {
		f, e := os.CreateTemp(r.Store.Root(), ".aact-env-*")
		if e != nil {
			return Instance{}, e
		}
		defer os.Remove(f.Name())
		if e = f.Chmod(0600); e != nil {
			f.Close()
			return Instance{}, e
		}
		env := map[string]string{}
		for n, v := range s.Env {
			env[n] = v
		}
		for n, v := range s.SecretEnv {
			env[n] = v
		}
		ns := make([]string, 0, len(env))
		for n := range env {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		for _, n := range ns {
			if strings.ContainsAny(n, "=\r\n") || strings.ContainsAny(env[n], "\r\n") {
				f.Close()
				return Instance{}, errors.New("invalid container environment entry")
			}
			if _, e = fmt.Fprintf(f, "%s=%s\n", n, env[n]); e != nil {
				f.Close()
				return Instance{}, e
			}
		}
		if e = f.Close(); e != nil {
			return Instance{}, e
		}
		a = append(a, "--env-file", f.Name())
	}
	a = append(a, image)
	a = append(a, s.Args...)
	b, e := r.run(ctx, a)
	if e != nil {
		return Instance{}, fmt.Errorf("start MCP on %s:%d: %w", s.Host, s.HostPort, e)
	}
	out := Instance{Key: k, ID: strings.TrimSpace(string(b)), Name: name, Status: "running", URL: specURL(s)}
	if !r.SkipHealth {
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var healthErr error
		for {
			healthErr = Health(deadline, out.URL, s.Transport)
			if healthErr == nil {
				break
			}
			select {
			case <-deadline.Done():
				return out, fmt.Errorf("MCP started but health check failed: %w", healthErr)
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return out, nil
}

type redactingExecutor struct {
	base   process.Executor
	values []string
}

func (e redactingExecutor) Run(ctx context.Context, a []string, cwd string, in []byte, env map[string]string, cb func([]byte)) ([]byte, error) {
	scrub := func(s string) string {
		for _, v := range e.values {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
		return s
	}
	out, err := e.base.Run(ctx, a, cwd, in, env, func(b []byte) {
		if cb != nil {
			cb([]byte(scrub(string(b))))
		}
	})
	if err != nil {
		err = errors.New(scrub(err.Error()))
	}
	return out, err
}
func instance(d dockerInfo) Instance {
	l := d.Config.Labels
	s := d.State.Status
	if s == "" && d.State.Running {
		s = "running"
	}
	return Instance{Key: state.Key{Source: l["aact.source"], Package: l["aact.package"], Environment: l["aact.environment"], Target: l["aact.target"]}, ID: d.ID, Name: strings.TrimPrefix(d.Name, "/"), Status: s, URL: l["aact.url"]}
}
func (r *Runtime) Stop(ctx context.Context, k state.Key) error {
	d, e := r.inspect(ctx, containerName(k))
	if e != nil {
		return e
	}
	if !owned(d, k) {
		return errors.New("refusing to stop an unmanaged container")
	}
	_, e = r.run(ctx, []string{"rm", "--force", d.ID})
	return e
}
func (r *Runtime) List(ctx context.Context) ([]Instance, error) {
	b, e := r.run(ctx, []string{"ps", "--all", "--filter", "label=aact.managed=1", "--format", "{{.ID}}"})
	if e != nil {
		return nil, e
	}
	ids := strings.Fields(string(b))
	out := []Instance{}
	for _, id := range ids {
		d, e := r.inspect(ctx, id)
		if e != nil {
			return nil, e
		}
		if d.Config.Labels["aact.managed"] != "1" {
			continue
		}
		i := instance(d)
		if d.Config.Labels["aact.key"] != i.Key.ID() {
			continue
		}
		out = append(out, i)
	}
	return out, nil
}
func (r *Runtime) Logs(ctx context.Context, k state.Key) (io.ReadCloser, error) {
	d, e := r.inspect(ctx, containerName(k))
	if e != nil {
		return nil, e
	}
	if !owned(d, k) {
		return nil, errors.New("refusing unmanaged container logs")
	}
	b, e := r.run(ctx, []string{"logs", "--tail", "200", d.ID})
	return io.NopCloser(bytes.NewReader(b)), e
}
