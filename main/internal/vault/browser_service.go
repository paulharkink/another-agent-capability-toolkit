package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/daemon"
)

// BrowserConfigurationRegistrar provisions daemon-side browser access for a
// container after its existing daemon credential has been bound.
type BrowserConfigurationRegistrar interface {
	RegisterBrowserConfiguration(instance, container string, config browsercontroller.ServiceConfig) error
	UnregisterBrowserConfiguration(container string) error
	PauseBrowserConfiguration(container string) error
}

// BrowserConfigurationOpener starts an instance's registered browser helper
// through the authenticated local control API.
type BrowserConfigurationOpener interface {
	StartBrowserConfiguration(container string) (string, error)
}

func (s *Store) StartBrowserConfiguration(container string) (string, error) {
	if !browsercontroller.ValidDockerContainerID(container) {
		return "", errors.New("invalid browser owner")
	}
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return "", errors.New("daemon control is unavailable")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	var result struct {
		ViewerURL string `json:"viewer_url"`
	}
	err := controlRequest(info, string(controlToken), http.MethodPost, "/browser/configurations/"+container+"/start", nil, &result)
	if err != nil {
		return "", err
	}
	return result.ViewerURL, nil
}

func (s *Store) PauseBrowserConfiguration(container string) error {
	if !browsercontroller.ValidDockerContainerID(container) {
		return errors.New("invalid browser owner")
	}
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("daemon control is unavailable")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	return controlRequest(info, string(controlToken), http.MethodPost, "/browser/configurations/"+container+"/stop", nil, nil)
}

func (s *Store) RegisterBrowserConfiguration(instance, container string, config browsercontroller.ServiceConfig) error {
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("daemon control is unavailable")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	return controlRequest(info, string(controlToken), http.MethodPost, "/browser/configurations", browserRegistration{Instance: instance, ContainerID: container, Config: config}, nil)
}

func (s *Store) UnregisterBrowserConfiguration(container string) error {
	if !browsercontroller.ValidDockerContainerID(container) {
		return errors.New("invalid browser owner")
	}
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("daemon control is unavailable")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	return controlRequest(info, string(controlToken), http.MethodDelete, "/browser/configurations/"+container, nil, nil)
}

func (a *API) UnregisterBrowser(ctx context.Context, container string) error {
	return a.stopBrowserRegistration(ctx, container, true)
}

// StartBrowser starts only a browser helper registered to the named runtime.
// This method is reachable through the authenticated local control API.
func (a *API) StartBrowser(ctx context.Context, container string) (string, error) {
	if !browsercontroller.ValidDockerContainerID(container) {
		return "", errors.New("invalid browser owner")
	}
	encoded, found, err := a.store.Get(browserRegistryNamespace, container)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("browser configuration is unavailable")
	}
	defer clear(encoded)
	var record browserConfigurationRecord
	if json.Unmarshal(encoded, &record) != nil || validateInstanceNamespace(record.Instance) != nil {
		return "", errors.New("invalid browser registration")
	}
	if a.browserService == nil {
		return "", errors.New("browser service is unavailable")
	}
	record.Config.ContainerID = container
	return a.browserService.StartConfiguration(ctx, record.Config)
}

func (a *API) stopBrowserRegistration(ctx context.Context, container string, remove bool) error {
	if !browsercontroller.ValidDockerContainerID(container) {
		return errors.New("invalid browser owner")
	}
	encoded, found, err := a.store.Get(browserRegistryNamespace, container)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer clear(encoded)
	var record browserConfigurationRecord
	if json.Unmarshal(encoded, &record) != nil || validateInstanceNamespace(record.Instance) != nil {
		return errors.New("invalid browser registration")
	}
	if a.browserService == nil {
		return errors.New("browser service is unavailable")
	}
	record.Config.ContainerID = container
	return a.browserService.StopConfiguration(ctx, record.Config, remove, func() error {
		if !remove {
			return nil
		}
		return a.store.Delete(browserRegistryNamespace, container)
	})
}

var browserRegistryNamespace = func() string {
	sum := sha256.Sum256([]byte("AACT internal browser configuration registry"))
	return hex.EncodeToString(sum[:])
}()

type browserConfigurationRecord struct {
	Instance string                          `json:"instance"`
	Config   browsercontroller.ServiceConfig `json:"config"`
}

type browserRegistration struct {
	Instance    string                          `json:"instance"`
	ContainerID string                          `json:"container_id"`
	Config      browsercontroller.ServiceConfig `json:"config"`
}

// RegisterBrowser is called only by the authenticated local daemon control API.
func (a *API) RegisterBrowser(instance, container string, config browsercontroller.ServiceConfig) error {
	if validateInstanceNamespace(instance) != nil || !browsercontroller.ValidDockerContainerID(container) {
		return errors.New("invalid browser owner")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	owned := false
	for token, id := range a.owners {
		if id == container && a.tokens[token] == instance {
			owned = true
			break
		}
	}
	if !owned {
		return errors.New("browser owner has no bound daemon credential")
	}
	encoded, err := json.Marshal(browserConfigurationRecord{Instance: instance, Config: config})
	if err != nil || len(encoded) > 128*1024 {
		return errors.New("invalid browser configuration")
	}
	defer clear(encoded)
	return a.store.Put(browserRegistryNamespace, container, encoded)
}

func (a *API) BrowserConfiguration(client daemon.ClientIdentity) (browsercontroller.ServiceConfig, bool) {
	var empty browsercontroller.ServiceConfig
	if validateInstanceNamespace(client.Instance) != nil || !browsercontroller.ValidDockerContainerID(client.ContainerID) {
		return empty, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	owned := false
	for token, id := range a.owners {
		if id == client.ContainerID && a.tokens[token] == client.Instance {
			owned = true
			break
		}
	}
	if !owned {
		return empty, false
	}
	encoded, found, err := a.store.Get(browserRegistryNamespace, client.ContainerID)
	if err != nil || !found {
		return empty, false
	}
	defer clear(encoded)
	var record browserConfigurationRecord
	if json.Unmarshal(encoded, &record) != nil || record.Instance != client.Instance || record.Config.Validate() != nil {
		return empty, false
	}
	record.Config.ContainerID = client.ContainerID
	return record.Config, true
}

func reapBrowserRegistrations(ctx context.Context, api *API, status func(context.Context, browsercontroller.ServiceConfig) (string, error)) error {
	entries, err := api.store.List(browserRegistryNamespace)
	if err != nil {
		return err
	}
	var result error
	for container, encoded := range entries {
		var record browserConfigurationRecord
		err := json.Unmarshal(encoded, &record)
		clear(encoded)
		if err != nil || !browsercontroller.ValidDockerContainerID(container) || validateInstanceNamespace(record.Instance) != nil || record.Config.Validate() != nil {
			continue
		}
		record.Config.ContainerID = container
		if api.browserService == nil {
			return errors.New("browser service is unavailable")
		}
		err = api.browserService.ReapConfiguration(ctx, record.Config, func() (string, error) { return status(ctx, record.Config) }, func() error {
			if err := api.RevokeTokensByContainerID(container); err != nil {
				return err
			}
			return api.store.Delete(browserRegistryNamespace, container)
		})
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func runBrowserReaper(ctx context.Context, api *API) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = reapBrowserRegistrations(ctx, api, func(ctx context.Context, config browsercontroller.ServiceConfig) (string, error) {
				return browsercontroller.NewDockerBrowser(config).MCPRuntimeStatus(ctx)
			})
		}
	}
}
