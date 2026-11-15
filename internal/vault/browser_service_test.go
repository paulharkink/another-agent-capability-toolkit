package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/browsercontroller"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/daemon"
)

func TestBrowserConfigurationIsBoundToIssuedContainerAndInstance(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	namespace, container := strings.Repeat("a", 64), strings.Repeat("b", 64)
	token, err := api.IssueToken(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, container); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := api.RegisterBrowser(namespace, container, config); err != nil {
		t.Fatal(err)
	}
	identity := daemon.ClientIdentity{Instance: namespace, ContainerID: container}
	if got, ok := api.BrowserConfiguration(identity); !ok || got.ProfileVolume != config.ProfileVolume {
		t.Fatalf("lookup = %#v %v", got, ok)
	}
	if _, ok := api.BrowserConfiguration(daemon.ClientIdentity{Instance: strings.Repeat("c", 64), ContainerID: container}); ok {
		t.Fatal("cross-instance browser access")
	}
	if _, ok := api.BrowserConfiguration(daemon.ClientIdentity{Instance: namespace, ContainerID: strings.Repeat("d", 64)}); ok {
		t.Fatal("cross-container browser access")
	}
	if err := api.RevokeToken(token); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.BrowserConfiguration(identity); ok {
		t.Fatal("revoked browser remained accessible")
	}
}

type reaperBrowser struct{ stopped bool }

func (*reaperBrowser) ReadOffline(context.Context, browsercontroller.BrowserAuthRequest) (json.RawMessage, error) {
	return nil, nil
}
func (*reaperBrowser) EnsureStarted(context.Context) (string, error) { return "", nil }
func (*reaperBrowser) RunBrowserAuthAdapter(context.Context, browsercontroller.BrowserAuthRequest) (json.RawMessage, error) {
	return nil, nil
}
func (*reaperBrowser) ReloadBrowserTab(context.Context, string) (string, int, error) {
	return "", 0, nil
}
func (b *reaperBrowser) StopBrowser(context.Context) error { b.stopped = true; return nil }

func TestBrowserReaperStopsOrphanAndRevokesItsCredential(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	instance, container := strings.Repeat("a", 64), strings.Repeat("b", 64)
	token, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, container); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := api.RegisterBrowser(instance, container, config); err != nil {
		t.Fatal(err)
	}
	b := &reaperBrowser{}
	api.browserService = browsercontroller.NewService(api.BrowserConfiguration, func(browsercontroller.ServiceConfig) browsercontroller.ServiceBrowser { return b })
	if err := reapBrowserRegistrations(context.Background(), api, func(context.Context, browsercontroller.ServiceConfig) (string, error) { return "missing", nil }); err != nil {
		t.Fatal(err)
	}
	if !b.stopped {
		t.Fatal("orphan browser was not stopped")
	}
	if _, ok := api.AuthenticateDaemonRequest("Bearer " + token); ok {
		t.Fatal("orphan credential remained valid")
	}
}

func TestBrowserReaperKeepsCredentialWhenDockerStatusIsUnknown(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	instance, container := strings.Repeat("a", 64), strings.Repeat("b", 64)
	token, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, container); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := api.RegisterBrowser(instance, container, config); err != nil {
		t.Fatal(err)
	}
	b := &reaperBrowser{}
	api.browserService = browsercontroller.NewService(api.BrowserConfiguration, func(browsercontroller.ServiceConfig) browsercontroller.ServiceBrowser { return b })
	if err := reapBrowserRegistrations(context.Background(), api, func(context.Context, browsercontroller.ServiceConfig) (string, error) {
		return "", errors.New("synthetic Docker error")
	}); err == nil {
		t.Fatal("missing inspection error")
	}
	if b.stopped {
		t.Fatal("stopped browser when owner status was unknown")
	}
	if _, ok := api.AuthenticateDaemonRequest("Bearer " + token); !ok {
		t.Fatal("revoked credential on transient Docker failure")
	}
}

func TestBrowserReaperKeepsCredentialForStoppedContainer(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	instance, container := strings.Repeat("a", 64), strings.Repeat("b", 64)
	token, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, container); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := api.RegisterBrowser(instance, container, config); err != nil {
		t.Fatal(err)
	}
	b := &reaperBrowser{}
	api.browserService = browsercontroller.NewService(api.BrowserConfiguration, func(browsercontroller.ServiceConfig) browsercontroller.ServiceBrowser { return b })
	if err := reapBrowserRegistrations(context.Background(), api, func(context.Context, browsercontroller.ServiceConfig) (string, error) { return "stopped", nil }); err != nil {
		t.Fatal(err)
	}
	if !b.stopped {
		t.Fatal("browser not stopped")
	}
	if _, ok := api.AuthenticateDaemonRequest("Bearer " + token); !ok {
		t.Fatal("credential revoked for restartable container")
	}
	if _, ok := api.BrowserConfiguration(daemon.ClientIdentity{Instance: instance, ContainerID: container}); !ok {
		t.Fatal("configuration lost for restartable container")
	}
}

func TestBrowserControlRegistrationRequiresDaemonControlCredential(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	instance, container := strings.Repeat("a", 64), strings.Repeat("b", 64)
	token, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, container); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(browserRegistration{Instance: instance, ContainerID: container, Config: browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}})
	handler := controlHandler(api, "synthetic-control", func() {}, nil)
	for _, tc := range []struct {
		credential string
		want       int
	}{{"incorrect", 401}, {"synthetic-control", 204}} {
		r := httptest.NewRequest(http.MethodPost, "/browser/configurations", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+tc.credential)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("credential %s: %d", tc.credential, w.Code)
		}
	}
}

func TestPauseBrowserKeepsRegistrationForOwnerRecovery(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	instance, container := strings.Repeat("a", 64), strings.Repeat("b", 64)
	token, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, container); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := api.RegisterBrowser(instance, container, config); err != nil {
		t.Fatal(err)
	}
	b := &reaperBrowser{}
	api.browserService = browsercontroller.NewService(api.BrowserConfiguration, func(browsercontroller.ServiceConfig) browsercontroller.ServiceBrowser { return b })
	h := controlHandler(api, "synthetic-control", func() {}, nil)
	r := httptest.NewRequest(http.MethodPost, "/browser/configurations/"+container+"/stop", nil)
	r.Header.Set("Authorization", "Bearer synthetic-control")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || !b.stopped {
		t.Fatalf("status=%d stopped=%v", w.Code, b.stopped)
	}
	if _, ok := api.BrowserConfiguration(daemon.ClientIdentity{Instance: instance, ContainerID: container}); !ok {
		t.Fatal("pause removed registration")
	}
}

func TestBrowserOnlyCredentialCannotReadPrivateInputs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	instance := strings.Repeat("a", 64)
	data, _ := json.Marshal(map[string]any{"version": 1, "values": map[string]any{BrowserOnlyPrivateInputScope: "do-not-return", "other": "do-not-return"}})
	if err := store.Put(instance, privateInputEnvelopeSecretName, data); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	token, err := api.IssuePrivateInputToken(instance, []string{BrowserOnlyPrivateInputScope})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/private-inputs/" + BrowserOnlyPrivateInputScope, "/v1/private-inputs/other", "/v1/secrets"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code == 200 || strings.Contains(w.Body.String(), "do-not-return") {
			t.Fatalf("scope exposed a private value for %s", path)
		}
	}
}
