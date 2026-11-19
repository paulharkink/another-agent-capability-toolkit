package browsercontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/daemon"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/process"
)

// ServiceConfig is provisioned by AACT, never supplied by an MCP request.
type ServiceConfig struct {
	ContainerID      string             `json:"-"`
	Origin           string             `json:"origin"`
	ProfileVolume    string             `json:"profile_volume"`
	AccessConfigJSON string             `json:"access_config_json,omitempty"`
	StartupURL       string             `json:"startup_url,omitempty"`
	ContainerName    string             `json:"container_name"`
	Adapter          BrowserAuthAdapter `json:"adapter"`
}

// Validate rejects untrusted paths and policy mismatches before registration.
func (c ServiceConfig) Validate() error {
	if !volumeNamePattern.MatchString(c.ProfileVolume) || !containerNamePattern.MatchString(c.ContainerName) || (c.Origin != "" && !validBrowserOrigin(c.Origin)) {
		return errors.New("invalid browser service configuration")
	}
	if err := ValidateAccessConfig(c.AccessConfigJSON); err != nil {
		return err
	}
	if (c.Origin != "" && ValidateStartupURLAgainstAccessConfig(c.Origin, c.AccessConfigJSON) != nil) || ValidateStartupURLAgainstAccessConfig(c.StartupURL, c.AccessConfigJSON) != nil {
		return errors.New("browser service origin is blocked")
	}
	if len(c.Adapter.Source) > 64*1024 {
		return errors.New("browser adapter is too large")
	}
	return nil
}

type ServiceBrowser interface {
	ReadOffline(context.Context, BrowserAuthRequest) (json.RawMessage, error)
	EnsureStarted(context.Context) (string, error)
	RunBrowserAuthAdapter(context.Context, BrowserAuthRequest) (json.RawMessage, error)
	ReloadBrowserTab(context.Context, string) (string, int, error)
	StopBrowser(context.Context) error
}

type runningBrowser interface {
	BrowserRunning(context.Context) (bool, error)
}

type Service struct {
	lookup     func(daemon.ClientIdentity) (ServiceConfig, bool)
	newBrowser func(ServiceConfig) ServiceBrowser
	mu         sync.Mutex
}

func NewService(lookup func(daemon.ClientIdentity) (ServiceConfig, bool), factory func(ServiceConfig) ServiceBrowser) *Service {
	return &Service{lookup: lookup, newBrowser: factory}
}

func NewDockerService(lookup func(daemon.ClientIdentity) (ServiceConfig, bool)) *Service {
	return NewService(lookup, func(c ServiceConfig) ServiceBrowser {
		return NewDockerBrowser(c)
	})
}

func NewDockerBrowser(c ServiceConfig) DockerBrowser {
	return DockerBrowser{Commands: executorRunner{Executor: process.OSExecutor{}}, ProfileVolume: c.ProfileVolume, AccessConfigJSON: c.AccessConfigJSON, StartupURL: c.StartupURL, ContainerName: c.ContainerName, MCPContainerID: c.ContainerID}
}

// StopConfiguration is used by the trusted local control API, including after
// a container credential has been revoked or its MCP has disappeared.
func (s *Service) StopConfiguration(ctx context.Context, config ServiceConfig, removeBrowser bool, remove func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.Validate(); err != nil {
		return err
	}
	browser := s.newBrowser(config)
	var err error
	if remover, ok := browser.(interface{ RemoveBrowser(context.Context) error }); ok && removeBrowser {
		err = remover.RemoveBrowser(ctx)
	} else {
		err = browser.StopBrowser(ctx)
	}
	if err != nil {
		return err
	}
	return remove()
}

// StartConfiguration opens the registered authentication helper and returns
// its actual loopback viewer URL to the local AACT UI.
func (s *Service) StartConfiguration(ctx context.Context, config ServiceConfig) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.Validate(); err != nil {
		return "", err
	}
	viewer, err := s.newBrowser(config).EnsureStarted(ctx)
	if err != nil || validateViewerURL(viewer) != nil {
		return "", errors.New("browser could not be started")
	}
	return viewer, nil
}

// ReapConfiguration checks owner state while holding the same lifecycle lock
// used by requests. Stopped owners retain credentials for a later restart;
// removed owners can never restart with the same container ID.
func (s *Service) ReapConfiguration(ctx context.Context, config ServiceConfig, status func() (string, error), remove func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.Validate(); err != nil {
		return err
	}
	state, err := status()
	if err != nil {
		return err
	}
	if state == "running" {
		return nil
	}
	if state != "stopped" && state != "missing" {
		return errors.New("invalid browser owner state")
	}
	browser := s.newBrowser(config)
	if remover, ok := browser.(interface{ RemoveBrowser(context.Context) error }); ok {
		err = remover.RemoveBrowser(ctx)
	} else {
		err = browser.StopBrowser(ctx)
	}
	if err != nil {
		return err
	}
	if state == "missing" {
		return remove()
	}
	return nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/browser/credentials" {
		http.NotFound(w, r)
		return
	}
	client, ok := daemon.ClientFromContext(r.Context())
	if !ok || client.ContainerID == "" || s.lookup == nil || s.newBrowser == nil {
		http.Error(w, "browser access is unavailable", http.StatusForbidden)
		return
	}
	var input struct {
		Selector BrowserAuthSelector `json:"selector"`
		Origin   string              `json:"origin,omitempty"`
		Config   map[string]any      `json:"config,omitempty"`
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	dec.DisallowUnknownFields()
	decodeErr := dec.Decode(&input)
	if decodeErr != nil {
		_ = controller.SetReadDeadline(time.Time{})
		http.Error(w, "invalid browser request", http.StatusBadRequest)
		return
	}
	decodeErr = dec.Decode(new(any))
	_ = controller.SetReadDeadline(time.Time{})
	if decodeErr != io.EOF {
		http.Error(w, "invalid browser request", http.StatusBadRequest)
		return
	}
	// Keep registration lookup and browser operations serialized with removal.
	s.mu.Lock()
	defer s.mu.Unlock()
	config, ok := s.lookup(client)
	if !ok {
		http.Error(w, "browser access is unavailable", http.StatusForbidden)
		return
	}
	origin := input.Origin
	if origin == "" {
		origin = config.Origin
	}
	adapter := config.Adapter
	adapter.Config = input.Config
	request := BrowserAuthRequest{Origin: origin, Selector: input.Selector, Adapter: adapter}
	if request.Adapter.Source == "" {
		request.Adapter.Source = browserCredentialAdapterSource
	}
	if validateGenericBrowserAuthRequest(request) != nil || ValidateStartupURLAgainstAccessConfig(request.Origin, config.AccessConfigJSON) != nil {
		http.Error(w, "invalid browser request", http.StatusBadRequest)
		return
	}
	browser := s.newBrowser(config)
	if liveness, ok := browser.(interface {
		MCPRuntimeRunning(context.Context) (bool, error)
	}); ok {
		active, err := liveness.MCPRuntimeRunning(r.Context())
		if err != nil {
			http.Error(w, "browser owner is unavailable", http.StatusServiceUnavailable)
			return
		}
		if !active {
			http.Error(w, "browser owner is not running", http.StatusForbidden)
			return
		}
	}
	interactive := false
	var err error
	var result json.RawMessage
	if running, ok := browser.(runningBrowser); ok {
		interactive, err = running.BrowserRunning(r.Context())
		if err != nil {
			http.Error(w, "browser state is unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	// Offline reads require an unmounted profile. Once a login helper is open,
	// retries must keep it alive and use its active profile through the adapter.
	if !interactive {
		result, readErr := browser.ReadOffline(r.Context(), request)
		if readErr == nil && adapterSucceeded(result) {
			writeBrowserResult(w, http.StatusOK, result, "")
			return
		}
	}
	viewer, err := browser.EnsureStarted(r.Context())
	if err != nil || validateViewerURL(viewer) != nil {
		http.Error(w, "browser could not be started", http.StatusServiceUnavailable)
		return
	}
	if !interactive {
		// Chromium and its initial page may take a few seconds after startup.
		// Retry the initial tab lookup only for a newly opened login; reloading
		// an active session would discard forms and other user progress.
		readyCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		for {
			status, _, reloadErr := browser.ReloadBrowserTab(readyCtx, request.Origin)
			if reloadErr == nil && (status == "reloaded" || status == "ambiguous") {
				break
			}
			select {
			case <-readyCtx.Done():
				break
			case <-time.After(time.Second):
			}
			if readyCtx.Err() != nil {
				break
			}
		}
		cancel()
	}
	result, err = browser.RunBrowserAuthAdapter(r.Context(), request)
	if err == nil && adapterSucceeded(result) {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := browser.StopBrowser(stopCtx)
		cancel()
		if err != nil {
			http.Error(w, "browser could not be stopped", http.StatusServiceUnavailable)
			return
		}
		writeBrowserResult(w, http.StatusOK, result, "")
		return
	}
	writeBrowserResult(w, http.StatusConflict, json.RawMessage(`{"status":"interaction_required"}`), viewer)
}

func adapterSucceeded(result json.RawMessage) bool {
	var value map[string]json.RawMessage
	if len(result) == 0 || len(result) > 64*1024 || json.Unmarshal(result, &value) != nil || value == nil {
		return false
	}
	var status string
	if raw, ok := value["status"]; ok && json.Unmarshal(raw, &status) != nil {
		return false
	}
	return status != "interaction_required" && status != "retryable_error" && status != "error"
}

func writeBrowserResult(w http.ResponseWriter, status int, result json.RawMessage, viewer string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Result    json.RawMessage `json:"result"`
		ViewerURL string          `json:"viewer_url,omitempty"`
	}{result, viewer})
}
