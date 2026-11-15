package browsercontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/daemon"
)

type serviceBrowserFixture struct {
	offline, online                                   json.RawMessage
	starts, offlineCalls, onlineCalls, reloads, stops int
	cancel                                            context.CancelFunc
	stopContextError                                  error
}

type stoppedOwnerBrowser struct{ serviceBrowserFixture }

func (*stoppedOwnerBrowser) MCPRuntimeRunning(context.Context) (bool, error) { return false, nil }

func TestServiceRejectsStoppedOwner(t *testing.T) {
	identity := daemon.ClientIdentity{Instance: strings.Repeat("a", 64), ContainerID: strings.Repeat("b", 64)}
	b := &stoppedOwnerBrowser{}
	s := NewService(func(daemon.ClientIdentity) (ServiceConfig, bool) {
		return ServiceConfig{Origin: "https://example.test"}, true
	}, func(ServiceConfig) ServiceBrowser { return b })
	h := daemon.NewAuthenticatedHost(func(string) (daemon.ClientIdentity, bool) { return identity, true })
	if err := h.Register("browser", "/v1/browser/", s); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/browser/credentials", strings.NewReader(`{"selector":{"kind":"cookie","match":"exact","names":["session"]}}`)))
	if w.Code != http.StatusForbidden || b.offlineCalls != 0 {
		t.Fatalf("status=%d offline=%d", w.Code, b.offlineCalls)
	}
}

func (b *serviceBrowserFixture) ReadOffline(context.Context, BrowserAuthRequest) (json.RawMessage, error) {
	b.offlineCalls++
	return b.offline, nil
}
func (b *serviceBrowserFixture) EnsureStarted(context.Context) (string, error) {
	b.starts++
	return "http://localhost:49001", nil
}
func (b *serviceBrowserFixture) RunBrowserAuthAdapter(context.Context, BrowserAuthRequest) (json.RawMessage, error) {
	b.onlineCalls++
	if b.cancel != nil {
		b.cancel()
	}
	return b.online, nil
}
func (b *serviceBrowserFixture) ReloadBrowserTab(context.Context, string) (string, int, error) {
	b.reloads++
	return "reloaded", 1, nil
}
func (b *serviceBrowserFixture) StopBrowser(ctx context.Context) error {
	b.stops++
	b.stopContextError = ctx.Err()
	return nil
}

func TestServiceRefreshesFromOfflineProfileWithoutStartingBrowser(t *testing.T) {
	b := &serviceBrowserFixture{offline: json.RawMessage(`{"status":"refreshed","access_token":"synthetic"}`)}
	w := serviceRequest(t, b)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic") {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	if b.offlineCalls != 1 || b.starts != 0 || b.onlineCalls != 0 {
		t.Fatalf("unexpected calls %#v", b)
	}
}

func TestServiceStartsBrowserOnlyAfterOfflineNeedsInteraction(t *testing.T) {
	b := &serviceBrowserFixture{offline: json.RawMessage(`{"status":"interaction_required"}`), online: json.RawMessage(`{"status":"refreshed","access_token":"synthetic"}`)}
	w := serviceRequest(t, b)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic") {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	if b.offlineCalls != 1 || b.starts != 1 || b.reloads != 1 || b.onlineCalls != 1 || b.stops != 1 {
		t.Fatalf("unexpected calls %#v", b)
	}
}

func TestServiceReturnsViewerIfInteractiveSignInNeeded(t *testing.T) {
	b := &serviceBrowserFixture{offline: json.RawMessage(`{"status":"interaction_required"}`), online: json.RawMessage(`{"status":"interaction_required"}`)}
	w := serviceRequest(t, b)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "http://localhost:49001") {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	if b.stops != 0 {
		t.Fatal("interactive browser was stopped")
	}
}

func TestServiceRejectsUnregisteredContainerAndAdapterSource(t *testing.T) {
	identity := daemon.ClientIdentity{Instance: strings.Repeat("a", 64), ContainerID: strings.Repeat("b", 64)}
	h := daemon.NewAuthenticatedHost(func(string) (daemon.ClientIdentity, bool) { return identity, true })
	b := &serviceBrowserFixture{offline: json.RawMessage(`{"status":"refreshed"}`)}
	registered := false
	s := NewService(func(daemon.ClientIdentity) (ServiceConfig, bool) {
		return ServiceConfig{Origin: "https://example.test"}, registered
	}, func(ServiceConfig) ServiceBrowser { return b })
	if err := h.Register("browser", "/v1/browser/", s); err != nil {
		t.Fatal(err)
	}
	call := func(body string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/browser/credentials", strings.NewReader(body)))
		return w.Code
	}
	if status := call(`{"selector":{"kind":"cookie","match":"exact","names":["session"]}}`); status != 403 {
		t.Fatalf("unregistered status %d", status)
	}
	registered = true
	if status := call(`{"selector":{"kind":"cookie","match":"exact","names":["session"]},"adapter":{"source":"malicious"}}`); status != 400 {
		t.Fatalf("caller adapter status %d", status)
	}
	if b.offlineCalls != 0 {
		t.Fatal("rejected request accessed the browser")
	}
}

func TestServiceUnregisterStopsOnlyRegisteredBrowser(t *testing.T) {
	identity := daemon.ClientIdentity{Instance: strings.Repeat("a", 64), ContainerID: strings.Repeat("b", 64)}
	b := &serviceBrowserFixture{}
	removed := false
	s := NewService(func(client daemon.ClientIdentity) (ServiceConfig, bool) { return ServiceConfig{}, client == identity }, func(ServiceConfig) ServiceBrowser { return b })
	config := ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := s.StopConfiguration(context.Background(), config, func() error { removed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if b.stops != 1 || !removed {
		t.Fatalf("stops %d removed %v", b.stops, removed)
	}
}

func TestServiceDoesNotHoldLifecycleLockWhileReadingClientBody(t *testing.T) {
	identity := daemon.ClientIdentity{Instance: strings.Repeat("a", 64), ContainerID: strings.Repeat("b", 64)}
	b := &serviceBrowserFixture{offline: json.RawMessage(`{"status":"refreshed"}`)}
	s := NewService(func(daemon.ClientIdentity) (ServiceConfig, bool) {
		return ServiceConfig{Origin: "https://example.test"}, true
	}, func(ServiceConfig) ServiceBrowser { return b })
	h := daemon.NewAuthenticatedHost(func(string) (daemon.ClientIdentity, bool) { return identity, true })
	if err := h.Register("browser", "/v1/browser/", s); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	started := make(chan struct{})
	go func() {
		close(started)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/browser/credentials", reader))
	}()
	<-started
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/browser/credentials", strings.NewReader(`{"selector":{"kind":"cookie","match":"exact","names":["session"]}}`)))
		result <- w.Code
	}()
	select {
	case got := <-result:
		if got != 200 {
			t.Fatalf("second request %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("slow client blocked another browser request")
	}
}

func TestServiceStopsAfterSuccessEvenIfCallerDisconnects(t *testing.T) {
	identity := daemon.ClientIdentity{Instance: strings.Repeat("a", 64), ContainerID: strings.Repeat("b", 64)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &serviceBrowserFixture{offline: json.RawMessage(`{"status":"interaction_required"}`), online: json.RawMessage(`{"status":"refreshed"}`), cancel: cancel}
	s := NewService(func(daemon.ClientIdentity) (ServiceConfig, bool) {
		return ServiceConfig{Origin: "https://example.test"}, true
	}, func(ServiceConfig) ServiceBrowser { return b })
	h := daemon.NewAuthenticatedHost(func(string) (daemon.ClientIdentity, bool) { return identity, true })
	if err := h.Register("browser", "/v1/browser/", s); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/browser/credentials", strings.NewReader(`{"selector":{"kind":"cookie","match":"exact","names":["session"]}}`)).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if b.stops != 1 || b.stopContextError != nil {
		t.Fatalf("cleanup stops=%d context=%v", b.stops, b.stopContextError)
	}
}

func TestReaperChecksOwnerAfterAcquiringLifecycleLock(t *testing.T) {
	b := &serviceBrowserFixture{}
	s := NewService(func(daemon.ClientIdentity) (ServiceConfig, bool) { return ServiceConfig{}, true }, func(ServiceConfig) ServiceBrowser { return b })
	config := ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	removed := false
	if err := s.ReapConfiguration(context.Background(), config, func() (string, error) { return "running", nil }, func() error { removed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if b.stops != 0 || removed {
		t.Fatal("running owner was cleaned up")
	}
}

func serviceRequest(t *testing.T, b *serviceBrowserFixture) *httptest.ResponseRecorder {
	t.Helper()
	identity := daemon.ClientIdentity{Instance: strings.Repeat("a", 64), ContainerID: strings.Repeat("b", 64)}
	h := daemon.NewAuthenticatedHost(func(header string) (daemon.ClientIdentity, bool) { return identity, header == "Bearer synthetic" })
	s := NewService(func(client daemon.ClientIdentity) (ServiceConfig, bool) {
		return ServiceConfig{Origin: "https://example.test", Adapter: BrowserAuthAdapter{Source: "def run(credentials, config): return {}"}}, client == identity
	}, func(ServiceConfig) ServiceBrowser { return b })
	if err := h.Register("browser", "/v1/browser/", s); err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"selector":{"kind":"cookie","match":"exact","names":["session"]}}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/browser/credentials", body)
	r.Header.Set("Authorization", "Bearer synthetic")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
