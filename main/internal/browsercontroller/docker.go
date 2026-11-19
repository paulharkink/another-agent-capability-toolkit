package browsercontroller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/process"
)

const (
	containerName               = "browser-authentication-helper-controller"
	authenticationHelperImage   = "aact/browser-authentication-helper:1.18.0-form1"
	browserPort                 = "6901/tcp"
	browserHome                 = "/home/kasm-user"
	browserProfilePath          = "/home/kasm-user/.config/chromium/Default"
	browserReloadTimeout        = 40 * time.Second
	maxBrowserAuthCookies       = 128
	maxBrowserAuthCookieName    = 512
	browserAuthOperationTimeout = 60 * time.Second
)

const browserCredentialAdapterSource = `def run(credentials, config):
    return {"credentials": credentials}
`

var volumeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var containerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var dockerIDPattern = regexp.MustCompile(`(?i)^[a-f0-9]{12,64}$`)

func ValidDockerContainerID(id string) bool { return dockerIDPattern.MatchString(id) }

type CommandRunner interface {
	Run(context.Context, []string) ([]byte, error)
}

type InputCommandRunner interface {
	RunInput(context.Context, []string, []byte) ([]byte, error)
}

type executorRunner struct{ Executor process.Executor }

func (r executorRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	return r.Executor.Run(ctx, append([]string{"docker"}, args...), "", nil, nil, nil)
}

func (r executorRunner) RunInput(ctx context.Context, args []string, input []byte) ([]byte, error) {
	return r.Executor.Run(ctx, append([]string{"docker"}, args...), "", input, nil, nil)
}

type DockerBrowser struct {
	Commands         CommandRunner
	ProfileVolume    string
	AccessConfigJSON string
	StartupURL       string
	ContainerName    string
	MCPContainerID   string
}

// BrowserAuthAdapter is trusted, site-specific code executed inside the
// browser authentication helper image. It receives selected cookie records and should return only
// the information its caller needs.
type BrowserAuthAdapter struct {
	Source string         `json:"source"`
	Config map[string]any `json:"config"`
}

// BrowserAuthRequest selects cookies at one exact site origin for a trusted
// in-container adapter. Cookie values are never placed in Docker arguments.
type BrowserAuthRequest struct {
	Origin   string
	Selector BrowserAuthSelector
	Adapter  BrowserAuthAdapter
}

// BrowserAuthSelector chooses named cookies or sessionStorage records.
// Cookie selectors use Names; sessionStorage selectors use Name and Match.
type BrowserAuthSelector struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name,omitempty"`
	Names      []string `json:"names,omitempty"`
	Match      string   `json:"match,omitempty"`
	AllowEmpty bool     `json:"allow_empty,omitempty"`
}

// BrowserCookie is one named cookie read from the browser profile. Values are
// returned in memory to the caller and are never written or logged here.
type BrowserCookie struct {
	Name  string
	Value string
}

// BrowserStorageEntry is one key/value pair selected from sessionStorage.
type BrowserStorageEntry struct {
	Key   string
	Value string
}

func (d DockerBrowser) name() string {
	if d.ContainerName == "" {
		return containerName
	}
	return d.ContainerName
}

type inspectedContainer struct {
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
		Paused  bool `json:"Paused"`
	} `json:"State"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func (d DockerBrowser) EnsureStarted(ctx context.Context) (string, error) {
	if err := ValidateAccessConfig(d.AccessConfigJSON); err != nil {
		return "", err
	}
	if err := ValidateStartupURLAgainstAccessConfig(d.StartupURL, d.AccessConfigJSON); err != nil {
		return "", errors.New("configured browser startup URL is invalid or blocked by browser access policy")
	}
	if !volumeNamePattern.MatchString(d.ProfileVolume) {
		return "", errors.New("configured browser profile volume name is invalid")
	}
	name := d.name()
	if !containerNamePattern.MatchString(name) {
		return "", errors.New("configured browser container name is invalid")
	}
	if d.Commands == nil {
		return "", errors.New("Docker command runner is unavailable")
	}
	profileLockPath, err := profileVolumeLockPath(d.ProfileVolume)
	if err != nil {
		return "", errors.New("browser profile volume lock is unavailable")
	}
	releaseProfile, acquiredProfile, err := acquireRuntimeLock(profileLockPath)
	if err != nil {
		return "", errors.New("browser profile volume lock is unavailable")
	}
	if !acquiredProfile {
		return "", errors.New("browser profile volume is being prepared by another AACT controller; retry after it finishes")
	}
	defer releaseProfile()
	names, err := d.Commands.Run(ctx, []string{"container", "ls", "--all", "--filter", "name=^/" + name + "$", "--format", "{{.Names}}"})
	if err != nil {
		return "", errors.New("Docker is unavailable")
	}
	volumeUsers, err := d.Commands.Run(ctx, []string{"container", "ls", "--all", "--filter", "volume=" + d.ProfileVolume, "--format", "{{.Names}}"})
	if err != nil {
		return "", errors.New("could not verify whether the browser profile volume is in use")
	}
	for _, volumeUser := range strings.Fields(string(volumeUsers)) {
		if volumeUser == name {
			continue
		}
		mounted, err := d.containerUsesProfileVolume(ctx, volumeUser)
		if err != nil {
			return "", errors.New("could not verify whether another container uses the browser profile volume")
		}
		if mounted {
			return "", errors.New("browser profile volume is mounted by another container; identify it; if it is the previous AACT browser, stop and remove that container without removing the profile volume, then retry; otherwise resolve the mount conflict manually")
		}
	}
	if strings.TrimSpace(string(names)) == name {
		previous, inspectErr := d.inspect(ctx, name)
		if inspectErr != nil {
			return "", inspectErr
		}
		if previous.Config.Image == "aact/browser-authentication-helper:1.18.0" && !previous.State.Running {
			startup, hasStartup := previous.Config.Labels["aact.browser-controller.startup-url-sha256"]
			if !d.owned(previous) || previous.Config.Labels["org.aact.browser-authentication-helper"] != "1" || previous.Config.Labels["org.aact.browser-access-controller"] != "1" || previous.Config.Labels["aact.browser-controller.access-config-sha256"] != accessConfigDigest(d.AccessConfigJSON) || (hasStartup && startup != startupURLDigest(d.StartupURL)) || (!hasStartup && d.StartupURL != "") {
				return "", errors.New("legacy browser container does not match the registered configuration")
			}
			if _, err := d.Commands.Run(ctx, []string{"container", "rm", name}); err != nil {
				return "", errors.New("legacy browser container could not be upgraded")
			}
			names = nil
		}
	}
	if strings.TrimSpace(string(names)) == "" {
		args := []string{
			"container", "run", "--detach", "--name", name,
			"--label", "aact.browser-controller=1",
			"--label", "aact.browser-controller.profile-volume=" + d.ProfileVolume,
			"--label", "aact.browser-controller.access-config-sha256=" + accessConfigDigest(d.AccessConfigJSON),
			"--label", "aact.browser-controller.startup-url-sha256=" + startupURLDigest(d.StartupURL),
			"--shm-size=512m", "--publish", "127.0.0.1::6901",
			"--mount", "source=" + d.ProfileVolume + ",target=" + browserHome,
		}
		if strings.TrimSpace(d.AccessConfigJSON) != "" {
			args = append(args, "--env", "AACT_BROWSER_ACCESS_CONFIG_JSON="+d.AccessConfigJSON)
		}
		if d.StartupURL != "" {
			args = append(args, "--env", "KASM_URL="+d.StartupURL)
		}
		args = append(args, authenticationHelperImage)
		if _, err := d.Commands.Run(ctx, args); err != nil {
			return "", errors.New("browser container could not be created; check that the image and profile volume exist")
		}
	} else if strings.TrimSpace(string(names)) != name {
		return "", errors.New("browser container name is ambiguous; refusing to change an existing container")
	}

	state, err := d.inspect(ctx, name)
	if err != nil {
		return "", err
	}
	if !d.owned(state) {
		return "", errors.New("browser container name belongs to another configuration; refusing to change it")
	}
	if state.Config.Labels["org.aact.browser-authentication-helper"] != "1" || state.Config.Labels["org.aact.browser-access-controller"] != "1" {
		return "", errors.New("browser authentication helper image is missing AACT authentication helper or access-policy support; rebuild the image, then recreate this AACT browser container while retaining its profile volume")
	}
	if state.Config.Labels["aact.browser-controller.access-config-sha256"] != accessConfigDigest(d.AccessConfigJSON) {
		return "", errors.New("browser container has different access settings; stop and recreate the AACT browser container to apply the selected profile policy while retaining its profile volume")
	}
	storedStartupDigest, hasStartupDigest := state.Config.Labels["aact.browser-controller.startup-url-sha256"]
	if (hasStartupDigest && storedStartupDigest != startupURLDigest(d.StartupURL)) || (!hasStartupDigest && d.StartupURL != "") {
		return "", errors.New("browser container has different startup site settings; stop and recreate the AACT browser container to apply the selected startup URL while retaining its profile volume")
	}
	if state.State.Paused {
		return "", errors.New("browser container is paused; resume it manually before starting the browser")
	}
	if !state.State.Running {
		if _, err := d.Commands.Run(ctx, []string{"container", "start", name}); err != nil {
			return "", errors.New("browser container could not be started")
		}
		state, err = d.inspect(ctx, name)
		if err != nil {
			return "", err
		}
	}
	if !state.State.Running {
		return "", errors.New("browser container did not remain running")
	}
	bindings := state.NetworkSettings.Ports[browserPort]
	if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" {
		return "", errors.New("browser viewer is not bound to loopback")
	}
	port, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("Docker did not report a valid browser viewer port")
	}
	return (&url.URL{Scheme: "http", Host: "localhost:" + strconv.Itoa(port)}).String(), nil
}

// StopBrowser stops only the configured AACT-owned browser. The shared
// profile-volume lock serializes this transition with browser startup.
func (d DockerBrowser) StopBrowser(ctx context.Context) error {
	return d.stopBrowser(ctx, false)
}

// BrowserRunning reports whether the registered AACT helper is currently open.
// It never creates or starts the helper, so credential retries can preserve an
// interactive login session.
func (d DockerBrowser) BrowserRunning(ctx context.Context) (bool, error) {
	if err := ValidateAccessConfig(d.AccessConfigJSON); err != nil {
		return false, err
	}
	name := d.name()
	if !containerNamePattern.MatchString(name) || d.Commands == nil {
		return false, errors.New("browser container configuration is unavailable")
	}
	names, err := d.Commands.Run(ctx, []string{"container", "ls", "--all", "--filter", "name=^/" + name + "$", "--format", "{{.Names}}"})
	if err != nil {
		return false, errors.New("could not inspect the managed browser container")
	}
	if strings.TrimSpace(string(names)) == "" {
		return false, nil
	}
	if strings.TrimSpace(string(names)) != name {
		return false, errors.New("browser container name is ambiguous")
	}
	state, err := d.inspect(ctx, name)
	if err != nil {
		return false, err
	}
	if !d.owned(state) || state.Config.Labels["org.aact.browser-authentication-helper"] != "1" || state.Config.Labels["org.aact.browser-access-controller"] != "1" || state.Config.Labels["aact.browser-controller.access-config-sha256"] != accessConfigDigest(d.AccessConfigJSON) {
		return false, errors.New("browser container configuration changed")
	}
	storedStartupDigest, hasStartupDigest := state.Config.Labels["aact.browser-controller.startup-url-sha256"]
	if (hasStartupDigest && storedStartupDigest != startupURLDigest(d.StartupURL)) || (!hasStartupDigest && d.StartupURL != "") {
		return false, errors.New("browser startup configuration changed")
	}
	return state.State.Running, nil
}

// RemoveBrowser removes only the verified helper container, retaining its volume.
func (d DockerBrowser) RemoveBrowser(ctx context.Context) error {
	return d.stopBrowser(ctx, true)
}

func (d DockerBrowser) stopBrowser(ctx context.Context, remove bool) error {
	if err := ValidateAccessConfig(d.AccessConfigJSON); err != nil {
		return err
	}
	if !volumeNamePattern.MatchString(d.ProfileVolume) {
		return errors.New("configured browser profile volume name is invalid")
	}
	name := d.name()
	if !containerNamePattern.MatchString(name) {
		return errors.New("configured browser container name is invalid")
	}
	if d.Commands == nil {
		return errors.New("Docker command runner is unavailable")
	}
	profileLockPath, err := profileVolumeLockPath(d.ProfileVolume)
	if err != nil {
		return errors.New("browser profile volume lock is unavailable")
	}
	releaseProfile, acquiredProfile, err := acquireRuntimeLock(profileLockPath)
	if err != nil {
		return errors.New("browser profile volume lock is unavailable")
	}
	if !acquiredProfile {
		return errors.New("browser profile volume is being prepared by another AACT controller; retry after it finishes")
	}
	defer releaseProfile()

	names, err := d.Commands.Run(ctx, []string{"container", "ls", "--all", "--filter", "name=^/" + name + "$", "--format", "{{.Names}}"})
	if err != nil {
		return errors.New("could not inspect the managed browser container before stopping it")
	}
	if strings.TrimSpace(string(names)) == "" {
		return nil
	}
	if strings.TrimSpace(string(names)) != name {
		return errors.New("browser container name is ambiguous; refusing to stop an existing container")
	}
	state, err := d.inspect(ctx, name)
	if err != nil {
		return err
	}
	if !d.owned(state) {
		return errors.New("browser container name belongs to another configuration; refusing to stop it")
	}
	if state.Config.Labels["org.aact.browser-authentication-helper"] != "1" || state.Config.Labels["org.aact.browser-access-controller"] != "1" ||
		state.Config.Labels["aact.browser-controller.access-config-sha256"] != accessConfigDigest(d.AccessConfigJSON) {
		return errors.New("browser container configuration changed; refusing to stop it")
	}
	storedStartupDigest, hasStartupDigest := state.Config.Labels["aact.browser-controller.startup-url-sha256"]
	if (hasStartupDigest && storedStartupDigest != startupURLDigest(d.StartupURL)) || (!hasStartupDigest && d.StartupURL != "") {
		return errors.New("browser startup configuration changed; refusing to stop it")
	}
	if state.State.Running {
		if _, err := d.Commands.Run(ctx, []string{"container", "stop", "--time", "5", name}); err != nil {
			return errors.New("could not stop the managed browser container")
		}
	}
	if remove {
		if _, err := d.Commands.Run(ctx, []string{"container", "rm", name}); err != nil {
			return errors.New("could not remove the managed browser container")
		}
	}
	return nil
}

func (d DockerBrowser) containerUsesProfileVolume(ctx context.Context, name string) (bool, error) {
	output, err := d.Commands.Run(ctx, []string{"container", "inspect", "--format", "{{json .Mounts}}", name})
	if err != nil || len(output) == 0 || len(output) > 64*1024 {
		return false, errors.New("could not inspect container volume mounts")
	}
	defer clear(output)
	var mounts []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(output, &mounts); err != nil {
		return false, errors.New("could not inspect container volume mounts")
	}
	for _, mount := range mounts {
		if mount.Type == "volume" && mount.Name == d.ProfileVolume {
			return true, nil
		}
	}
	return false, nil
}

func profileVolumeLockPath(volume string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil || root == "" {
		return "", errors.New("user cache directory is unavailable")
	}
	digest := sha256.Sum256([]byte(volume))
	return filepath.Join(root, "aact", "browser-profile-volume", hex.EncodeToString(digest[:])[:32]+".lock"), nil
}

func accessConfigDigest(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "none"
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "invalid"
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "invalid"
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func startupURLDigest(raw string) string {
	if raw == "" {
		return "none"
	}
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (d DockerBrowser) MCPRuntimeRunning(ctx context.Context) (bool, error) {
	status, err := d.MCPRuntimeStatus(ctx)
	return status == "running", err
}

// MCPRuntimeStatus distinguishes restartable stopped containers from removed ones.
func (d DockerBrowser) MCPRuntimeStatus(ctx context.Context) (string, error) {
	if d.Commands == nil || !dockerIDPattern.MatchString(d.MCPContainerID) {
		return "", errors.New("MCP runtime identity is unavailable")
	}
	names, err := d.Commands.Run(ctx, []string{"container", "ls", "--all", "--filter", "id=" + d.MCPContainerID, "--format", "{{.ID}}"})
	if err != nil {
		return "", errors.New("could not inspect the owning MCP runtime")
	}
	listed := strings.Fields(string(names))
	if len(listed) == 0 {
		return "missing", nil
	}
	if len(listed) != 1 || !strings.HasPrefix(strings.ToLower(d.MCPContainerID), strings.ToLower(listed[0])) {
		return "", errors.New("Docker returned ambiguous MCP runtime metadata")
	}
	const format = `{"ID":{{json .Id}},"State":{"Running":{{json .State.Running}},"Restarting":{{json .State.Restarting}},"Status":{{json .State.Status}}}}`
	out, err := d.Commands.Run(ctx, []string{"container", "inspect", "--format", format, d.MCPContainerID})
	if err != nil {
		return "", errors.New("could not inspect the owning MCP runtime")
	}
	var inspected struct {
		ID    string `json:"ID"`
		State struct {
			Running    bool   `json:"Running"`
			Restarting bool   `json:"Restarting"`
			Status     string `json:"Status"`
		} `json:"State"`
	}
	if err := json.Unmarshal(out, &inspected); err != nil || !strings.HasPrefix(strings.ToLower(inspected.ID), strings.ToLower(d.MCPContainerID)) {
		return "", errors.New("Docker returned invalid MCP runtime metadata")
	}
	if inspected.State.Running || inspected.State.Restarting || inspected.State.Status == "restarting" {
		return "running", nil
	}
	return "stopped", nil
}

// RunBrowserAuthAdapter invokes a trusted site adapter once with one consistent
// selection snapshot. Cookie values and sessionStorage records reach the adapter
// only through the helper's private stdin protocol.
func (d DockerBrowser) RunBrowserAuthAdapter(ctx context.Context, request BrowserAuthRequest) (json.RawMessage, error) {
	if request.Adapter.Source == "" {
		request.Adapter.Source = browserCredentialAdapterSource
	}
	if request.Adapter.Config == nil {
		request.Adapter.Config = map[string]any{}
	}
	if err := validateGenericBrowserAuthRequest(request); err != nil {
		return nil, err
	}
	helpRequest := genericBrowserAuthHelperRequest{
		ProfilePath: browserProfilePath,
		Origin:      request.Origin,
		Selector:    request.Selector,
		Adapter:     request.Adapter,
	}
	input, err := json.Marshal(helpRequest)
	if err != nil || len(input) > 256*1024 {
		return nil, errors.New("browser authentication request is too large or invalid")
	}
	defer clear(input)
	opCtx, cancel := context.WithTimeout(ctx, browserAuthOperationTimeout)
	defer cancel()
	if d.Commands == nil {
		return nil, errors.New("Docker command runner is unavailable")
	}
	if _, err := d.EnsureStarted(opCtx); err != nil {
		return nil, errors.New("browser container could not be made ready for authentication")
	}
	runner, ok := d.Commands.(InputCommandRunner)
	if !ok {
		return nil, errors.New("Docker command runner cannot pass private helper input")
	}
	result, err := d.runGenericAdapterInput(opCtx, runner, input)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ReadOffline runs the helper against a read-only profile without starting Chromium.
func (d DockerBrowser) ReadOffline(ctx context.Context, request BrowserAuthRequest) (json.RawMessage, error) {
	if request.Adapter.Source == "" {
		request.Adapter.Source = browserCredentialAdapterSource
	}
	if request.Adapter.Config == nil {
		request.Adapter.Config = map[string]any{}
	}
	if err := validateGenericBrowserAuthRequest(request); err != nil {
		return nil, err
	}
	if !volumeNamePattern.MatchString(d.ProfileVolume) || d.Commands == nil {
		return nil, errors.New("browser profile is unavailable")
	}
	runner, ok := d.Commands.(InputCommandRunner)
	if !ok {
		return nil, errors.New("Docker command runner cannot pass private helper input")
	}
	lockPath, err := profileVolumeLockPath(d.ProfileVolume)
	if err != nil {
		return nil, errors.New("browser profile lock is unavailable")
	}
	release, acquired, err := acquireRuntimeLock(lockPath)
	if err != nil || !acquired {
		return nil, errors.New("browser profile is busy")
	}
	defer release()
	if _, err := d.Commands.Run(ctx, []string{"volume", "inspect", d.ProfileVolume}); err != nil {
		return nil, errors.New("browser profile volume does not exist")
	}
	users, err := d.Commands.Run(ctx, []string{"container", "ls", "--filter", "volume=" + d.ProfileVolume, "--format", "{{.Names}}"})
	if err != nil || len(strings.Fields(string(users))) != 0 {
		return nil, errors.New("browser profile is in use")
	}
	input, err := json.Marshal(genericBrowserAuthHelperRequest{ProfilePath: browserProfilePath, Origin: request.Origin, Selector: request.Selector, Adapter: request.Adapter})
	if err != nil || len(input) > 256*1024 {
		return nil, errors.New("browser authentication request is invalid")
	}
	defer clear(input)
	opCtx, cancel := context.WithTimeout(ctx, browserAuthOperationTimeout)
	defer cancel()
	output, err := runner.RunInput(opCtx, []string{"container", "run", "--rm", "--interactive", "--entrypoint", "browser-authentication-helper", "--mount", "source=" + d.ProfileVolume + ",target=" + browserHome + ",readonly", authenticationHelperImage, "run-adapter-offline"}, input)
	if err != nil {
		return nil, errors.New("offline browser authentication helper failed")
	}
	return decodeBrowserAdapterOutput(output)
}

// ReadBrowserCookies reads a bounded list of named cookies from one exact
// HTTP(S) origin and one browser-helper snapshot. Cookie values return only in
// memory to the caller; site interpretation remains the caller's responsibility.
func (d DockerBrowser) ReadBrowserCookies(ctx context.Context, origin string, cookieNames []string) ([]BrowserCookie, error) {
	result, err := d.RunBrowserAuthAdapter(ctx, BrowserAuthRequest{
		Origin:   origin,
		Selector: BrowserAuthSelector{Kind: "cookie", Names: cookieNames, Match: "exact"},
	})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Credentials []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil || len(envelope.Credentials) != len(cookieNames) {
		clear(result)
		return nil, errors.New("browser authentication helper returned invalid cookie data")
	}
	cookies := make([]BrowserCookie, len(envelope.Credentials))
	for index, cookie := range envelope.Credentials {
		if cookie.Key != cookieNames[index] || cookie.Value == "" {
			clear(result)
			for item := range envelope.Credentials {
				envelope.Credentials[item].Value = ""
			}
			return nil, errors.New("browser authentication helper returned invalid cookie data")
		}
		cookies[index] = BrowserCookie{Name: cookie.Key, Value: cookie.Value}
	}
	clear(result)
	return cookies, nil
}

// ReadBrowserSessionStorage reads exact, prefix-matched, or all keys at one
// exact HTTP(S) origin. The selected records return only in memory.
func (d DockerBrowser) ReadBrowserSessionStorage(ctx context.Context, origin, name, match string, allowEmpty bool) ([]BrowserStorageEntry, error) {
	result, err := d.RunBrowserAuthAdapter(ctx, BrowserAuthRequest{
		Origin:   origin,
		Selector: BrowserAuthSelector{Kind: "sessionStorage", Name: name, Match: match, AllowEmpty: allowEmpty},
	})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Credentials []BrowserStorageEntry `json:"credentials"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		clear(result)
		return nil, errors.New("browser authentication helper returned invalid sessionStorage data")
	}
	clear(result)
	return envelope.Credentials, nil
}

func validateGenericBrowserAuthRequest(request BrowserAuthRequest) error {
	if len(request.Origin) == 0 || len(request.Origin) > 2048 {
		return errors.New("browser authentication origin is invalid")
	}
	parsed, err := url.Parse(request.Origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("browser authentication origin is invalid")
	}
	host := parsed.Hostname()
	if net.ParseIP(host) == nil && !validDomain(host) {
		return errors.New("browser authentication origin is invalid")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("browser authentication origin is invalid")
		}
	}
	switch request.Selector.Kind {
	case "none":
		if request.Selector.Name != "" || len(request.Selector.Names) != 0 || request.Selector.Match != "" || request.Selector.AllowEmpty {
			return errors.New("browser authentication selector is invalid")
		}
	case "cookie":
		if request.Selector.Name != "" || request.Selector.Match != "exact" || request.Selector.AllowEmpty || len(request.Selector.Names) == 0 || len(request.Selector.Names) > maxBrowserAuthCookies {
			return errors.New("browser authentication cookie selector is invalid")
		}
		seen := make(map[string]struct{}, len(request.Selector.Names))
		for _, name := range request.Selector.Names {
			if name == "" || len(name) > maxBrowserAuthCookieName || strings.ContainsAny(name, "\x00\r\n") {
				return errors.New("browser authentication cookie name is invalid")
			}
			if _, exists := seen[name]; exists {
				return errors.New("browser authentication cookie list contains duplicates")
			}
			seen[name] = struct{}{}
		}
	case "sessionStorage", "localStorage":
		if len(request.Selector.Names) != 0 || len(request.Selector.Name) > maxBrowserAuthCookieName || strings.ContainsAny(request.Selector.Name, "\x00\r\n") {
			return errors.New("browser authentication sessionStorage selector is invalid")
		}
		if request.Selector.Match != "exact" && request.Selector.Match != "prefix" && request.Selector.Match != "all" {
			return errors.New("browser authentication sessionStorage selector is invalid")
		}
		if request.Selector.Name == "" && request.Selector.Match != "all" {
			return errors.New("browser authentication sessionStorage selector is invalid")
		}
	default:
		return errors.New("browser authentication selector kind is invalid")
	}
	if len(request.Adapter.Source) == 0 || len(request.Adapter.Source) > 64*1024 {
		return errors.New("browser authentication adapter source is invalid")
	}
	adapter := request.Adapter
	if adapter.Config == nil {
		adapter.Config = map[string]any{}
	}
	helpRequest := genericBrowserAuthHelperRequest{ProfilePath: browserProfilePath, Origin: request.Origin, Selector: request.Selector, Adapter: adapter}
	encoded, err := json.Marshal(helpRequest)
	defer clear(encoded)
	if err != nil || len(encoded) > 256*1024 {
		return errors.New("browser authentication request is too large or invalid")
	}
	return nil
}

// ReloadBrowserTab reloads only an already-open tab at one exact origin.
// Startup is handled by the host controller before this method is called.
func (d DockerBrowser) ReloadBrowserTab(ctx context.Context, origin string) (string, int, error) {
	if !validBrowserOrigin(origin) || ValidateStartupURLAgainstAccessConfig(origin, d.AccessConfigJSON) != nil {
		return "", 0, reloadNotDispatchedError()
	}
	if d.Commands == nil {
		return "", 0, reloadNotDispatchedError()
	}
	runner, ok := d.Commands.(InputCommandRunner)
	if !ok {
		return "", 0, reloadNotDispatchedError()
	}
	return d.reloadBrowserOrigin(ctx, runner, origin)
}

func (d DockerBrowser) runGenericAdapterInput(ctx context.Context, runner InputCommandRunner, input []byte) (json.RawMessage, error) {
	output, err := runner.RunInput(ctx, []string{"container", "exec", "-i", d.name(), "browser-authentication-helper", "run-adapter"}, input)
	if err != nil {
		return nil, errors.New("browser authentication helper failed")
	}
	return decodeBrowserAdapterOutput(output)
}

func decodeBrowserAdapterOutput(output []byte) (json.RawMessage, error) {
	defer clear(output)
	if len(output) == 0 || len(output) > 64*1024 {
		return nil, errors.New("browser authentication helper returned invalid output")
	}
	var result map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, errors.New("browser authentication helper returned invalid output")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("browser authentication helper returned invalid output")
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 64*1024 {
		return nil, errors.New("browser authentication helper returned invalid output")
	}
	return encoded, nil
}

type reloadHelperRequest struct {
	Origin string `json:"origin"`
}

type reloadHelperResult struct {
	Status string `json:"status"`
	Tabs   int    `json:"tabs"`
}

func (d DockerBrowser) reloadBrowserOrigin(ctx context.Context, runner InputCommandRunner, origin string) (string, int, error) {
	input, err := json.Marshal(reloadHelperRequest{Origin: origin})
	if err != nil {
		return "", 0, reloadNotDispatchedError()
	}
	reloadCtx, cancel := context.WithTimeout(ctx, browserReloadTimeout)
	defer cancel()
	output, err := runner.RunInput(reloadCtx, []string{"container", "exec", "-i", d.name(), "browser-authentication-helper", "reload"}, input)
	if err != nil {
		return "", 0, errors.New("browser page reload helper failed")
	}
	defer clear(output)
	if len(output) == 0 || len(output) > 4096 {
		return "", 0, errors.New("browser page reload helper returned invalid output")
	}
	var response reloadHelperResult
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil {
		return "", 0, errors.New("browser page reload helper returned invalid output")
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return "", 0, errors.New("browser page reload helper returned invalid output")
	}
	switch response.Status {
	case "reloaded":
		if response.Tabs != 1 {
			return "", 0, errors.New("browser page reload helper returned invalid output")
		}
	case "not_found":
		if response.Tabs != 0 {
			return "", 0, errors.New("browser page reload helper returned invalid output")
		}
	case "ambiguous":
		if response.Tabs < 2 {
			return "", 0, errors.New("browser page reload helper returned invalid output")
		}
	case "not_configured":
		if response.Tabs != 0 {
			return "", 0, errors.New("browser page reload helper returned invalid output")
		}
	default:
		return "", 0, errors.New("browser page reload helper returned invalid output")
	}
	return response.Status, response.Tabs, nil
}

func reloadNotDispatchedError() error {
	return errors.New("browser reload request was not dispatched")
}

type genericBrowserAuthHelperRequest struct {
	ProfilePath string              `json:"profile_path"`
	Origin      string              `json:"origin"`
	Selector    BrowserAuthSelector `json:"selector"`
	Adapter     BrowserAuthAdapter  `json:"adapter"`
}

func (d DockerBrowser) inspect(ctx context.Context, name string) (*inspectedContainer, error) {
	const format = `{"Config":{"Image":{{json .Config.Image}},"Labels":{{json .Config.Labels}}},"State":{"Running":{{json .State.Running}},"Paused":{{json .State.Paused}}},"Mounts":{{json .Mounts}},"NetworkSettings":{"Ports":{{json .NetworkSettings.Ports}}}}`
	out, err := d.Commands.Run(ctx, []string{"container", "inspect", "--format", format, name})
	if err != nil {
		return nil, errors.New("could not inspect browser container")
	}
	var inspected inspectedContainer
	if err := json.Unmarshal(out, &inspected); err != nil {
		return nil, errors.New("Docker returned invalid browser container metadata")
	}
	return &inspected, nil
}

func (d DockerBrowser) owned(state *inspectedContainer) bool {
	if (state.Config.Image != authenticationHelperImage && state.Config.Image != "aact/browser-authentication-helper:1.18.0") || state.Config.Labels["aact.browser-controller"] != "1" || state.Config.Labels["aact.browser-controller.profile-volume"] != d.ProfileVolume {
		return false
	}
	for _, mount := range state.Mounts {
		if mount.Type == "volume" && mount.Name == d.ProfileVolume && mount.Destination == browserHome {
			return true
		}
	}
	return false
}

func (d DockerBrowser) String() string {
	return fmt.Sprintf("Docker browser authentication helper (%s)", authenticationHelperImage)
}
