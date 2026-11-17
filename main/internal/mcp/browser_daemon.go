package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func browserContainerName(containerID string) string {
	sum := sha256.Sum256([]byte(containerID))
	return "browser-authentication-helper-" + hex.EncodeToString(sum[:12])
}

func (r *Runtime) registerBrowserWithDaemon(key state.Key, spec RunSpec, containerID string) error {
	if spec.BrowserController == nil {
		return nil
	}
	registrar, ok := r.VaultTokenIssuer.(vault.BrowserConfigurationRegistrar)
	if !ok {
		return errors.New("daemon cannot register browser configuration")
	}
	origin := ""
	if spec.BrowserController.StartupURL != "" {
		u, err := url.Parse(spec.BrowserController.StartupURL)
		if err != nil || u.Host == "" {
			return errors.New("browser startup origin is invalid")
		}
		origin = u.Scheme + "://" + u.Host
	}
	config := browsercontroller.ServiceConfig{Origin: origin, ProfileVolume: spec.BrowserController.ProfileVolume, AccessConfigJSON: spec.BrowserController.AccessConfigJSON, StartupURL: spec.BrowserController.StartupURL, ContainerName: browserContainerName(containerID), Adapter: browsercontroller.BrowserAuthAdapter{Source: spec.BrowserController.AdapterSource}}
	instance := spec.VaultNamespaceID
	if instance == "" {
		instance = key.ID()
	}
	return registrar.RegisterBrowserConfiguration(instance, containerID, config)
}

func (r *Runtime) unregisterBrowserWithDaemon(containerID string) error {
	registrar, ok := r.VaultTokenIssuer.(vault.BrowserConfigurationRegistrar)
	if !ok {
		return nil
	}
	return registrar.UnregisterBrowserConfiguration(containerID)
}

func (r *Runtime) pauseBrowserWithDaemon(containerID string) error {
	registrar, ok := r.VaultTokenIssuer.(vault.BrowserConfigurationRegistrar)
	if !ok {
		return errors.New("daemon cannot pause browser")
	}
	return registrar.PauseBrowserConfiguration(containerID)
}
