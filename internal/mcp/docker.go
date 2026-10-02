package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
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
	Key          state.Key `json:"key"`
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	URL          string    `json:"url"`
	Ownership    string    `json:"ownership"`
	LastAction   string    `json:"last_action,omitempty"`
	LastActionAt time.Time `json:"last_action_at,omitempty"`
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
func isDockerNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such object:") || strings.Contains(message, "no such container:")
}
func ownership(d dockerInfo, k state.Key, localID string, rows []state.Installation) string {
	l := d.Config.Labels
	if l["aact.managed"] != "1" || l["aact.key"] != k.ID() || l["aact.source"] != k.Source {
		return "unknown"
	}
	if owner := l["aact.owner"]; owner != "" {
		if owner == localID {
			return "local"
		}
		return "other-aact"
	}
	for _, row := range rows {
		if row.Component == "runtime" && row.Key == k && row.SourcePath != "" && row.SourcePath == d.ID {
			return "local"
		}
	}
	return "unknown"
}
func runtimeRecord(rows []state.Installation, k state.Key, id string) (state.Installation, bool) {
	for _, row := range rows {
		if row.Component == "runtime" && row.Key == k && row.SourcePath == id {
			return row, true
		}
	}
	return state.Installation{}, false
}
func (r *Runtime) recordAction(k state.Key, i Instance, action string) (Instance, error) {
	i.LastAction = action
	i.LastActionAt = time.Now().UTC()
	if err := r.Store.Record(state.Installation{Key: k, AgentID: "docker", Component: "runtime", Destination: i.Name, SourcePath: i.ID, Mode: "docker", URL: i.URL, LastAction: action, LastActionAt: i.LastActionAt}); err != nil {
		return i, err
	}
	return i, nil
}
func (r *Runtime) Start(ctx context.Context, k state.Key, s RunSpec) (out Instance, startErr error) {
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
	var attempt [16]byte
	if _, e := rand.Read(attempt[:]); e != nil {
		return Instance{}, e
	}
	expected["aact.attempt"] = hex.EncodeToString(attempt[:])
	installationID, e := r.Store.InstallationID()
	if e != nil {
		return Instance{}, e
	}
	expected["aact.owner"] = installationID
	rows, e := r.Store.Installations()
	if e != nil {
		return Instance{}, e
	}
	var previous *dockerInfo
	if old, e := r.inspect(ctx, name); e == nil {
		if ownership(old, k, installationID, rows) != "local" {
			return Instance{}, errors.New("container name belongs to another installation or an unknown owner")
		}
		if old.State.Running {
			if old.Config.Labels["aact.spec"] != expected["aact.spec"] {
				return Instance{}, errors.New("MCP is running with different settings; stop it before starting again")
			}
			out := instance(old)
			out.Ownership = "local"
			if e := r.checkHealth(ctx, out.URL, s.Transport); e != nil {
				return out, fmt.Errorf("existing MCP health check failed: %w", e)
			}
			if out, e = r.recordAction(k, out, "start"); e != nil {
				return out, fmt.Errorf("MCP state recording failed: %w", e)
			}
			return out, nil
		}
		previous = &old
	} else if !isDockerNotFound(e) {
		return Instance{}, e
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
	committed, renamed := false, false
	createdID := ""
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var cleanupErr error
		if createdID == "" {
			if candidate, err := r.inspect(cleanupCtx, name); err == nil && ownership(candidate, k, installationID, rows) == "local" && candidate.Config.Labels["aact.spec"] == expected["aact.spec"] && candidate.Config.Labels["aact.attempt"] == expected["aact.attempt"] && (previous == nil || candidate.ID != previous.ID) {
				createdID = candidate.ID
			}
		}
		if createdID != "" {
			_, cleanupErr = r.run(cleanupCtx, []string{"rm", "--force", createdID})
		}
		if renamed {
			_, restoreErr := r.run(cleanupCtx, []string{"rename", previous.ID, name})
			cleanupErr = errors.Join(cleanupErr, restoreErr)
		}
		if cleanupErr != nil {
			startErr = errors.Join(startErr, fmt.Errorf("MCP startup cleanup failed: %w", cleanupErr))
		}
	}()
	if previous != nil {
		backupName := name + "-previous-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if _, e := r.run(ctx, []string{"rename", previous.ID, backupName}); e != nil {
			return Instance{}, e
		}
		renamed = true
	}
	b, e := r.run(ctx, a)
	if e != nil {
		return Instance{}, fmt.Errorf("start MCP on %s:%d: %w", s.Host, s.HostPort, e)
	}
	createdID = strings.TrimSpace(string(b))
	out = Instance{Key: k, ID: createdID, Name: name, Status: "running", URL: specURL(s), Ownership: "local"}
	if e = r.checkHealth(ctx, out.URL, s.Transport); e != nil {
		return out, fmt.Errorf("MCP health check failed: %w", e)
	}
	if out, e = r.recordAction(k, out, "start"); e != nil {
		return out, fmt.Errorf("MCP state recording failed: %w", e)
	}
	committed = true
	if renamed {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e = r.run(cleanupCtx, []string{"rm", previous.ID}); e != nil {
			return out, fmt.Errorf("MCP started but prior stopped container cleanup failed: %w", e)
		}
	}
	return out, nil
}

func (r *Runtime) checkHealth(ctx context.Context, url, transport string) error {
	if r.SkipHealth {
		return nil
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		err := Health(deadline, url, transport)
		if err == nil {
			return nil
		}
		select {
		case <-deadline.Done():
			return err
		case <-time.After(250 * time.Millisecond):
		}
	}
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
	redactor := process.NewRedactor(e.values, cb)
	out, err := e.base.Run(ctx, a, cwd, in, env, redactor.Write)
	redactor.Flush()
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
	installationID, err := r.Store.InstallationID()
	if err != nil {
		return err
	}
	rows, err := r.Store.Installations()
	if err != nil {
		return err
	}
	d, e := r.inspect(ctx, containerName(k))
	if e != nil {
		return e
	}
	if ownership(d, k, installationID, rows) != "local" {
		return errors.New("refusing to stop a container owned by another installation or an unknown owner")
	}
	_, e = r.run(ctx, []string{"rm", "--force", d.ID})
	if e == nil {
		if _, err := r.recordAction(k, instance(d), "stop"); err != nil {
			return err
		}
	}
	return e
}
func (r *Runtime) List(ctx context.Context) ([]Instance, error) {
	installationID, e := r.Store.InstallationID()
	if e != nil {
		return nil, e
	}
	rows, e := r.Store.Installations()
	if e != nil {
		return nil, e
	}
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
		i.Ownership = ownership(d, i.Key, installationID, rows)
		if row, ok := runtimeRecord(rows, i.Key, i.ID); ok {
			i.LastAction = row.LastAction
			i.LastActionAt = row.LastActionAt
		}
		out = append(out, i)
	}
	present := map[string]bool{}
	for _, i := range out {
		present[i.ID] = true
	}
	for _, row := range rows {
		if row.Component == "runtime" && !present[row.SourcePath] {
			out = append(out, Instance{Key: row.Key, ID: row.SourcePath, Name: row.Destination, Status: "missing", URL: row.URL, Ownership: "local", LastAction: row.LastAction, LastActionAt: row.LastActionAt})
		}
	}
	type registration struct {
		Key state.Key
		URL string
	}
	seen := map[registration]bool{}
	for _, item := range out {
		if item.Status != "missing" {
			seen[registration{item.Key, item.URL}] = true
		}
	}
	for _, row := range rows {
		identity := registration{row.Key, row.URL}
		if row.Component == "mcp" && row.URL != "" && !seen[identity] {
			out = append(out, Instance{Key: row.Key, Name: row.RegistrationName, Status: "external", URL: row.URL, Ownership: "unknown"})
			seen[identity] = true
		}
	}
	return out, nil
}
func (r *Runtime) Logs(ctx context.Context, k state.Key) (io.ReadCloser, error) {
	installationID, err := r.Store.InstallationID()
	if err != nil {
		return nil, err
	}
	rows, err := r.Store.Installations()
	if err != nil {
		return nil, err
	}
	d, e := r.inspect(ctx, containerName(k))
	if e != nil {
		return nil, e
	}
	if ownership(d, k, installationID, rows) != "local" {
		return nil, errors.New("refusing logs for a container owned by another installation or an unknown owner")
	}
	var errlog bytes.Buffer
	b, e := r.Executor.Run(ctx, []string{"docker", "logs", "--tail", "200", d.ID}, "", nil, nil, func(p []byte) { errlog.Write(p) })
	b = append(b, errlog.Bytes()...)
	return io.NopCloser(bytes.NewReader(b)), e
}
