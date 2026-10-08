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
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Mount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only"`
}
type RunSpec struct {
	User             string            `json:"user,omitempty"`
	Image            string            `json:"image"`
	BuildContext     string            `json:"build_context,omitempty"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	SecretEnv        map[string]string `json:"secret_env,omitempty"`
	Mounts           []Mount           `json:"mounts,omitempty"`
	BindIP           string            `json:"bind_ip,omitempty"`
	AdvertisedHost   string            `json:"advertised_host,omitempty"`
	RegistrationName string            `json:"registration_name,omitempty"`
	// Host is retained for actions and saved specs written before bind and
	// advertised endpoint addressing were separated.
	Host          string `json:"host"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Transport     string `json:"transport"`
	EndpointPath  string `json:"endpoint_path"`
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

var ErrRunningWithDifferentSettings = errors.New("MCP is running with different settings")

func NewDockerRuntime(s *state.Store) *Runtime {
	return &Runtime{Store: s, Executor: process.OSExecutor{}}
}
func containerName(k state.Key) string { return "aact-" + k.ID()[:24] }

// containerNameForRegistration produces a readable, deterministic Docker name.
// Non-ASCII letters and digits are encoded as u plus their lowercase codepoint
// so distinct names do not collapse merely because their characters were lost.
func containerNameForRegistration(registrationName string) (string, error) {
	registrationName = strings.TrimSpace(registrationName)
	if registrationName == "" {
		return "", errors.New("MCP registration name is required for Docker container naming")
	}
	var b strings.Builder
	lastDash := false
	for _, r := range registrationName {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if r > 127 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			fmt.Fprintf(&b, "u%x", r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(b.String(), "-._")
	if value == "" {
		return "", errors.New("MCP registration name contains no Docker-name characters")
	}
	const maxName = 128
	const prefix = "aact-"
	if len(value) > maxName-len(prefix) {
		value = strings.TrimRight(value[:maxName-len(prefix)], "-._")
	}
	if value == "" {
		return "", errors.New("MCP registration name is too long to normalize safely")
	}
	return prefix + value, nil
}
func specURL(s RunSpec) string {
	h := s.AdvertisedHost
	if h == "" {
		h = s.Host
	}
	if h == "" {
		h = "localhost"
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") && net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")) != nil {
		h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	}
	return "http://" + net.JoinHostPort(h, strconv.Itoa(s.HostPort)) + s.EndpointPath
}

func effectiveBindIP(s RunSpec) string {
	if s.BindIP != "" {
		return s.BindIP
	}
	if ip := net.ParseIP(s.Host); ip != nil && ip.IsLoopback() {
		return ip.String()
	}
	return "127.0.0.1"
}

func effectiveAdvertisedHost(s RunSpec) string {
	if s.AdvertisedHost != "" {
		return s.AdvertisedHost
	}
	if s.Host != "" {
		return s.Host
	}
	return "localhost"
}

func validateAdvertisedHost(host string) error {
	if host == "" || strings.ContainsAny(host, "\r\n\x00 /\\?#@") {
		return errors.New("MCP advertised host must be a hostname or IP address without a scheme, port, path, or credentials")
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")) != nil {
		return nil
	}
	dnsName := strings.TrimSuffix(host, ".")
	if len(dnsName) == 0 || len(dnsName) > 253 {
		return errors.New("MCP advertised host must be a hostname or IP address without a scheme, port, path, or credentials")
	}
	for _, label := range strings.Split(dnsName, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("MCP advertised host must be a hostname or IP address without a scheme, port, path, or credentials")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("MCP advertised host must be a hostname or IP address without a scheme, port, path, or credentials")
			}
		}
	}
	u, err := url.Parse("http://" + host)
	if err != nil || u.Scheme != "http" || u.Host != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" || u.Port() != "" {
		return errors.New("MCP advertised host must be a hostname or IP address without a scheme, port, path, or credentials")
	}
	return nil
}
func labels(k state.Key, s RunSpec) map[string]string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	result := map[string]string{"aact.managed": "1", "aact.key": k.ID(), "aact.source": k.Source, "aact.package": k.Package, "aact.environment": k.Environment, "aact.target": k.Target, "aact.url": specURL(s), "aact.spec": hex.EncodeToString(h[:])}
	if s.RegistrationName != "" {
		result["aact.registration_name"] = s.RegistrationName
	}
	if k.Profile != "" {
		result["aact.profile"] = k.Profile
	}
	if k.MCP != "" {
		result["aact.mcp"] = k.MCP
	}
	return result
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

var errDockerObjectNotFound = errors.New("expected Docker object-not-found probe")

type dockerObjectNotFoundError struct{ cause error }

func (e dockerObjectNotFoundError) Error() string { return e.cause.Error() }
func (e dockerObjectNotFoundError) Unwrap() error { return e.cause }
func (e dockerObjectNotFoundError) Is(target error) bool {
	return target == errDockerObjectNotFound
}

func (r *Runtime) run(ctx context.Context, args []string) ([]byte, error) {
	b, commandErr, diagnostics := r.runCapture(ctx, args, true)
	if commandErr != nil {
		return b, dockerCommandError(args[0], commandErr, diagnostics)
	}
	return b, nil
}
func (r *Runtime) runCapture(ctx context.Context, args []string, stream bool) ([]byte, error, []byte) {
	var diagnostics bytes.Buffer
	b, e := r.Executor.Run(ctx, append([]string{"docker"}, args...), "", nil, nil, func(p []byte) {
		if diagnostics.Len() < 8192 {
			diagnostics.Write(p)
		}
		if stream && r.OnStderr != nil {
			r.OnStderr(p)
		}
	})
	return b, e, diagnostics.Bytes()
}
func dockerCommandError(operation string, commandErr error, diagnostics []byte) error {
	return fmt.Errorf("Docker %s: %w: %s", operation, commandErr, strings.TrimSpace(string(diagnostics)))
}
func (r *Runtime) inspect(ctx context.Context, name string) (dockerInfo, error) {
	// Inspect is a probe during first install. Keep its stderr buffered until
	// the result distinguishes an expected missing container from a real error.
	b, e, diagnostics := r.runCapture(ctx, []string{"inspect", name}, false)
	if e != nil {
		if isExpectedDockerNotFound(e, diagnostics) {
			e = dockerObjectNotFoundError{cause: dockerCommandError("inspect", e, diagnostics)}
		} else if r.OnStderr != nil && len(diagnostics) > 0 {
			r.OnStderr(diagnostics)
		}
		if !isDockerNotFound(e) {
			e = dockerCommandError("inspect", e, diagnostics)
		}
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
	return errors.Is(err, errDockerObjectNotFound)
}
func isExpectedDockerNotFound(exitErr error, diagnostics []byte) bool {
	if exitErr == nil {
		return false
	}
	message := strings.TrimSpace(exitErr.Error())
	statusText, hasStatus := strings.CutPrefix(message, "exit status ")
	if strings.HasPrefix(message, "command docker failed: ") {
		statusText, hasStatus = strings.CutPrefix(strings.TrimPrefix(message, "command docker failed: "), "exit status ")
	}
	if !hasStatus {
		return false
	}
	status, err := strconv.Atoi(statusText)
	if err != nil || status < 1 {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(string(diagnostics), "\r\n", "\n"), "\n")
	matched := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lowerLine := strings.ToLower(line)
		knownNotFound := false
		for _, prefix := range []string{"error: no such object: ", "error: no such container: "} {
			if strings.HasPrefix(lowerLine, prefix) && strings.TrimSpace(line[len(prefix):]) != "" {
				knownNotFound = true
				break
			}
		}
		if knownNotFound {
			matched++
			continue
		}
		return false
	}
	return matched > 0
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
	rows, err := r.Store.Installations()
	if err != nil {
		return i, err
	}
	var canonical state.Installation
	hasCanonical := false
	duplicates := []state.Installation{}
	for _, row := range rows {
		if row.Key != k || row.AgentID != "docker" || row.Component != "runtime" || row.SourcePath != i.ID {
			continue
		}
		if !hasCanonical {
			canonical, hasCanonical = row, true
		} else if row.Destination != canonical.Destination {
			duplicates = append(duplicates, row)
		}
	}
	destination := i.Name
	if hasCanonical {
		destination = canonical.Destination
	}
	for _, duplicate := range duplicates {
		if err := r.Store.Remove(duplicate); err != nil {
			return i, err
		}
	}
	if err := r.Store.Record(state.Installation{Key: k, AgentID: "docker", Component: "runtime", Destination: destination, SourcePath: i.ID, Mode: "docker", URL: i.URL, LastAction: action, LastActionAt: i.LastActionAt}); err != nil {
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
	s.BindIP = effectiveBindIP(s)
	s.AdvertisedHost = effectiveAdvertisedHost(s)
	bindIP := net.ParseIP(s.BindIP)
	if bindIP == nil || !bindIP.IsLoopback() {
		return Instance{}, errors.New("MCP Docker bind IP must be a numeric loopback address")
	}
	if err := validateAdvertisedHost(s.AdvertisedHost); err != nil {
		return Instance{}, err
	}
	if s.HostPort < 1 || s.HostPort > 65535 || s.ContainerPort < 1 || s.ContainerPort > 65535 {
		return Instance{}, errors.New("MCP port outside 1..65535")
	}
	if s.Image == "" && s.BuildContext == "" {
		return Instance{}, errors.New("MCP image or build context required")
	}
	registrationName := s.RegistrationName
	if strings.TrimSpace(registrationName) == "" {
		registrationName = strings.Join([]string{k.Package, k.Environment, k.Target, k.Profile, k.MCP}, " ")
	}
	s.RegistrationName = registrationName
	name, err := containerNameForRegistration(registrationName)
	if err != nil {
		return Instance{}, err
	}
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
	replacingRegistrationName := false
	legacyNameLookup := false
	old, inspectErr := r.inspect(ctx, name)
	if inspectErr != nil && isDockerNotFound(inspectErr) && containerName(k) != name {
		// Saved installations may still own a key-derived container name.
		old, inspectErr = r.inspect(ctx, containerName(k))
		legacyNameLookup = inspectErr == nil
	}
	hasRecordedRuntime := false
	for _, row := range rows {
		if row.Key == k && row.AgentID == "docker" && row.Component == "runtime" && row.SourcePath != "" && row.Destination != containerName(k) {
			hasRecordedRuntime = true
			break
		}
	}
	if inspectErr != nil && isDockerNotFound(inspectErr) && hasRecordedRuntime {
		// A registration rename changes the desired Docker name, but Docker's
		// immutable key/owner labels and container ID still identify our current
		// runtime. Resolve that current observation before attempting another bind.
		observed, listErr := r.List(ctx)
		if listErr != nil {
			return Instance{}, fmt.Errorf("refresh Docker inventory before registration-name replacement: %w", listErr)
		}
		var keyed []Instance
		for _, item := range observed {
			if item.Key == k && item.Status != "missing" && item.Status != "external" && item.ID != "" {
				keyed = append(keyed, item)
			}
		}
		if len(keyed) > 1 {
			return Instance{}, errors.New("multiple Docker runtime observations match this MCP profile; refusing ambiguous replacement")
		}
		if len(keyed) == 1 {
			candidate, candidateErr := r.inspect(ctx, keyed[0].ID)
			if candidateErr != nil {
				return Instance{}, candidateErr
			}
			if ownership(candidate, k, installationID, rows) != "local" {
				return Instance{}, errors.New("container name collision: the current MCP runtime belongs to another installation or has unknown ownership")
			}
			registeredName := candidate.Config.Labels["aact.registration_name"]
			candidateName, nameErr := containerNameForRegistration(registeredName)
			if registeredName == "" || nameErr != nil || registeredName == s.RegistrationName || candidateName == name {
				return Instance{}, errors.New("container name collision: existing runtime does not identify a distinct registration name")
			}
			old, inspectErr = candidate, nil
			replacingRegistrationName = true
		}
	}
	if inspectErr == nil {
		if ownership(old, k, installationID, rows) != "local" {
			return Instance{}, errors.New("container name collision: the readable name belongs to another installation, key, or an unknown owner")
		}
		if legacyNameLookup {
			if _, recorded := runtimeRecord(rows, k, old.ID); !recorded && old.State.Running {
				return Instance{}, errors.New("container name collision: legacy runtime has no matching locally recorded container ID")
			}
			actualName := strings.TrimPrefix(old.Name, "/")
			registeredName := old.Config.Labels["aact.registration_name"]
			if actualName == name {
				return Instance{}, errors.New("container name collision: legacy runtime unexpectedly occupies the desired readable name")
			}
			if registeredName != "" && registeredName != s.RegistrationName {
				registeredDockerName, nameErr := containerNameForRegistration(registeredName)
				if nameErr == nil && registeredDockerName == name {
					return Instance{}, errors.New("container name collision: different MCP registration names normalize to the same Docker name")
				}
			}
			replacingRegistrationName = true
		}
		if existingName := old.Config.Labels["aact.registration_name"]; !replacingRegistrationName && existingName != "" && existingName != s.RegistrationName {
			return Instance{}, errors.New("container name collision: different MCP registration names normalize to the same Docker name")
		}
		if old.State.Running && !replacingRegistrationName {
			if old.Config.Labels["aact.spec"] != expected["aact.spec"] {
				return Instance{}, ErrRunningWithDifferentSettings
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
	} else if !isDockerNotFound(inspectErr) {
		return Instance{}, inspectErr
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
	a = append(a, "--publish", net.JoinHostPort(s.BindIP, strconv.Itoa(s.HostPort))+":"+strconv.Itoa(s.ContainerPort))
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
		if cleanupErr != nil {
			startErr = errors.Join(startErr, fmt.Errorf("MCP startup cleanup failed: %w", cleanupErr))
		}
	}()
	if previous != nil {
		if previous.State.Running && replacingRegistrationName {
			if _, e := r.run(ctx, []string{"stop", previous.ID}); e != nil {
				return Instance{}, fmt.Errorf("stop prior MCP runtime before registration-name replacement: %w", e)
			}
		}
		backupName := "aact-previous-" + previous.ID[:min(12, len(previous.ID))] + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if _, e := r.run(ctx, []string{"rename", previous.ID, backupName}); e != nil {
			return Instance{}, e
		}
		renamed = true
	}
	b, e := r.run(ctx, a)
	if e != nil {
		return Instance{}, fmt.Errorf("start MCP on %s:%d: %w", s.BindIP, s.HostPort, e)
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
		for _, row := range rows {
			if row.Key == k && row.AgentID == "docker" && row.Component == "runtime" && row.SourcePath == previous.ID {
				if e = r.Store.Remove(row); e != nil {
					return out, fmt.Errorf("MCP started but prior runtime history cleanup failed: %w", e)
				}
			}
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
	return Instance{Key: state.Key{Source: l["aact.source"], Package: l["aact.package"], Environment: l["aact.environment"], Target: l["aact.target"], MCP: l["aact.mcp"], Profile: l["aact.profile"]}, ID: d.ID, Name: strings.TrimPrefix(d.Name, "/"), Status: s, URL: l["aact.url"]}
}
func (r *Runtime) observedContainer(ctx context.Context, k state.Key) (Instance, error) {
	instances, err := r.List(ctx)
	if err != nil {
		return Instance{}, fmt.Errorf("refresh Docker observation before controlling runtime: %w", err)
	}
	var match *Instance
	for i := range instances {
		item := &instances[i]
		if item.Key != k || item.Status == "missing" || item.Status == "external" || item.ID == "" {
			continue
		}
		if match != nil {
			return Instance{}, fmt.Errorf("multiple Docker runtime observations match this MCP profile; refusing ambiguous control")
		}
		match = item
	}
	if match == nil {
		return Instance{}, errors.New("no current Docker runtime observation matches this MCP profile")
	}
	if match.Ownership != "local" {
		return Instance{}, fmt.Errorf("refusing to control a container owned by %s", match.Ownership)
	}
	return *match, nil
}
func (r *Runtime) Stop(ctx context.Context, k state.Key) error {
	observed, err := r.observedContainer(ctx, k)
	if err != nil {
		return err
	}
	_, e := r.run(ctx, []string{"rm", "--force", observed.ID})
	if e == nil {
		if _, err := r.recordAction(k, observed, "stop"); err != nil {
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
		if row.Component == "mcp" && row.ExternalRegistration && row.URL != "" && !seen[identity] {
			out = append(out, Instance{Key: row.Key, Name: row.RegistrationName, Status: "external", URL: row.URL, Ownership: "unknown"})
			seen[identity] = true
		}
	}
	return out, nil
}
func (r *Runtime) Logs(ctx context.Context, k state.Key) (io.ReadCloser, error) {
	observed, err := r.observedContainer(ctx, k)
	if err != nil {
		return nil, err
	}
	b, e := r.run(ctx, []string{"logs", "--tail", "200", observed.ID})
	if e != nil {
		return nil, e
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
