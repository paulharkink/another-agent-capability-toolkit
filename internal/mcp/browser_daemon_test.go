package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/browsercontroller"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

type browserDaemonIssuer struct {
	fixtureVaultIssuer
	instance, container string
	config              browsercontroller.ServiceConfig
	unregistered        []string
	unregisterErr       error
	revokeErr           error
	events              []string
}

func (i *browserDaemonIssuer) RegisterBrowserConfiguration(instance, container string, config browsercontroller.ServiceConfig) error {
	i.events = append(i.events, "register:"+container)
	i.instance, i.container, i.config = instance, container, config
	return nil
}
func (i *browserDaemonIssuer) UnregisterBrowserConfiguration(container string) error {
	i.unregistered = append(i.unregistered, container)
	return i.unregisterErr
}
func (i *browserDaemonIssuer) PauseBrowserConfiguration(container string) error {
	i.events = append(i.events, "pause:"+container)
	return nil
}
func (i *browserDaemonIssuer) RevokeContainerHandoff(namespace, container string) error {
	if i.revokeErr != nil {
		return i.revokeErr
	}
	return i.fixtureVaultIssuer.RevokeContainerHandoff(namespace, container)
}

func TestReplacementRetainsOldCleanupRecordsWhenRevocationFails(t *testing.T) {
	r, f, key := testRuntime(t)
	help := t.TempDir() + "/handoff"
	if err := os.WriteFile(help, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	r.VaultHandoffHelperPath = help
	issuer := &browserDaemonIssuer{revokeErr: errors.New("synthetic revoke failure")}
	r.VaultTokenIssuer = issuer
	spec := RunSpec{Image: "fixture", HostPort: 18765, ContainerPort: 8765, EndpointPath: "/mcp", BrowserController: &BrowserControllerSpec{ProfileVolume: "fixture-volume", StartupURL: "https://example.test"}}
	oldSpec := normalizedTestSpec(key, spec)
	old := dockerInfo{ID: "old-runtime", Name: "/" + containerName(key), Config: dockerConfig{Labels: locallyOwnedLabels(t, r, key, oldSpec)}, State: dockerState{Running: false, Status: "exited"}}
	if err := r.Store.Record(state.Installation{Key: key, AgentID: "docker", Component: "runtime", Mode: "docker", Destination: containerName(key), SourcePath: old.ID}); err != nil {
		t.Fatal(err)
	}
	f.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "inspect":
			return json.Marshal([]dockerInfo{old})
		case "run":
			return []byte("new-runtime"), nil
		default:
			return nil, nil
		}
	}
	if _, err := r.Start(context.Background(), key, spec); err == nil {
		t.Fatal("revocation failure was not reported")
	}
	for _, id := range issuer.unregistered {
		if id == old.ID {
			t.Fatal("old browser registration removed before revocation")
		}
	}
	rows, err := r.Store.Installations()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.SourcePath == old.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("old runtime cleanup record was lost")
	}
}

func TestBrowserNamesAreBoundToContainerIdentity(t *testing.T) {
	if browserContainerName("first") == browserContainerName("second") {
		t.Fatal("distinct runtimes share a browser container name")
	}
}

func TestStopOrdinaryMCPDoesNotContactBrowserService(t *testing.T) {
	r, f, key := testRuntime(t)
	spec := RunSpec{Image: "fixture", HostPort: 8765, ContainerPort: 80}
	container := dockerInfo{ID: "ordinary-id", Name: "/aact-ordinary", Config: dockerConfig{Labels: locallyOwnedLabels(t, r, key, spec)}, State: dockerState{Running: true, Status: "running"}}
	issuer := &browserDaemonIssuer{unregisterErr: errors.New("daemon unavailable")}
	r.VaultTokenIssuer = issuer
	f.f = func(args []string) ([]byte, error) {
		if args[1] == "inspect" {
			return json.Marshal([]dockerInfo{container})
		}
		return nil, nil
	}
	if err := r.StopExact(context.Background(), key, container.ID); err != nil {
		t.Fatal(err)
	}
	if len(issuer.unregistered) != 0 {
		t.Fatal("ordinary MCP contacted browser service")
	}
}

func TestReplacementPausesOldBrowserBeforeNewRuntimeRegistration(t *testing.T) {
	r, f, key := testRuntime(t)
	help := t.TempDir() + "/handoff"
	if err := os.WriteFile(help, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	r.VaultHandoffHelperPath = help
	issuer := &browserDaemonIssuer{}
	r.VaultTokenIssuer = issuer
	spec := RunSpec{Image: "fixture", HostPort: 18765, ContainerPort: 8765, EndpointPath: "/mcp", BrowserController: &BrowserControllerSpec{ProfileVolume: "fixture-volume", StartupURL: "https://example.test"}}
	oldSpec := normalizedTestSpec(key, spec)
	old := dockerInfo{ID: "old-runtime", Name: "/" + containerName(key), Config: dockerConfig{Labels: locallyOwnedLabels(t, r, key, oldSpec)}, State: dockerState{Running: false, Status: "exited"}}
	f.f = func(args []string) ([]byte, error) {
		switch args[1] {
		case "inspect":
			return json.Marshal([]dockerInfo{old})
		case "run":
			return []byte("new-runtime"), nil
		default:
			return nil, nil
		}
	}
	if _, err := r.Start(context.Background(), key, spec); err != nil {
		t.Fatal(err)
	}
	if len(issuer.events) < 2 || issuer.events[0] != "pause:old-runtime" || issuer.events[1] != "register:new-runtime" {
		t.Fatalf("event order %v", issuer.events)
	}
}

func TestBrowserRuntimeRegistersWithDaemonWithoutBrokerToken(t *testing.T) {
	r, executor, key := testRuntime(t)
	issuer := &browserDaemonIssuer{}
	r.VaultTokenIssuer = issuer
	help := t.TempDir() + "/handoff"
	if err := os.WriteFile(help, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	r.VaultHandoffHelperPath = help
	executor.f = absent
	spec := RunSpec{Image: "fixture", HostPort: 18765, ContainerPort: 8765, EndpointPath: "/mcp", BrowserController: &BrowserControllerSpec{ProfileVolume: "fixture-volume", StartupURL: "https://example.test", AdapterSource: "def run(credentials, config): return {}"}}
	instance, err := r.Start(context.Background(), key, spec)
	if err != nil {
		t.Fatal(err)
	}
	if issuer.container != instance.ID || issuer.instance != key.ID() || issuer.config.ProfileVolume != "fixture-volume" {
		t.Fatalf("registration did not use the bound container: %#v", issuer.config)
	}
	for _, name := range issuer.issuedPrivateInputs {
		if strings.HasPrefix(name, "aact-browser-controller-") {
			t.Fatal("issued an obsolete broker capability")
		}
	}
}
