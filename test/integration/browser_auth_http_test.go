package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/daemon"
	vault "github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

type integrationBrowser struct {
	offline       json.RawMessage
	starts, stops int
}

func (b *integrationBrowser) ReadOffline(context.Context, browsercontroller.BrowserAuthRequest) (json.RawMessage, error) {
	return b.offline, nil
}
func (b *integrationBrowser) EnsureStarted(context.Context) (string, error) {
	b.starts++
	return "http://localhost:49001", nil
}
func (b *integrationBrowser) RunBrowserAuthAdapter(context.Context, browsercontroller.BrowserAuthRequest) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"interaction_required"}`), nil
}
func (b *integrationBrowser) ReloadBrowserTab(context.Context, string) (string, int, error) {
	return "reloaded", 1, nil
}
func (b *integrationBrowser) StopBrowser(context.Context) error { b.stops++; return nil }

func TestExistingDaemonCredentialAccessesOnlyRegisteredBrowserOverTLS(t *testing.T) {
	store, err := vault.Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := vault.NewAPI(store, nil)
	namespace, firstID, secondID := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	first, err := api.IssuePrivateInputToken(namespace, []string{vault.BrowserOnlyPrivateInputScope})
	if err != nil {
		t.Fatal(err)
	}
	second, err := api.IssuePrivateInputToken(namespace, []string{vault.BrowserOnlyPrivateInputScope})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(first, firstID); err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(second, secondID); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.test", StartupURL: "https://example.test", ProfileVolume: "fixture-volume", ContainerName: "fixture-browser"}
	if err := api.RegisterBrowser(namespace, firstID, config); err != nil {
		t.Fatal(err)
	}
	browser := &integrationBrowser{offline: json.RawMessage(`{"status":"refreshed","access_token":"synthetic"}`)}
	host := daemon.NewAuthenticatedHost(api.AuthenticateDaemonRequest)
	if err := host.Register("vault", "/", api); err != nil {
		t.Fatal(err)
	}
	if err := host.Register("browser", "/v1/browser/", browsercontroller.NewService(api.BrowserConfiguration, func(browsercontroller.ServiceConfig) browsercontroller.ServiceBrowser { return browser })); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(host)
	defer server.Close()
	request := func(token string) (int, string) {
		body := bytes.NewBufferString(`{"selector":{"kind":"cookie","match":"exact","names":["session"]}}`)
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/browser/credentials", body)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var data bytes.Buffer
		_, _ = data.ReadFrom(resp.Body)
		return resp.StatusCode, data.String()
	}
	if status, body := request(first); status != 200 || !strings.Contains(body, "synthetic") || browser.starts != 0 {
		t.Fatalf("offline status %d body %s starts %d", status, body, browser.starts)
	}
	if status, _ := request(second); status != 403 {
		t.Fatalf("other container status %d", status)
	}
	if status, _ := request("invalid"); status != 401 {
		t.Fatalf("invalid status %d", status)
	}
	browser.offline = json.RawMessage(`{"status":"interaction_required"}`)
	if status, body := request(first); status != 409 || !strings.Contains(body, "http://localhost:49001") || browser.starts != 1 || browser.stops != 0 {
		t.Fatalf("interactive status %d body %s", status, body)
	}
	browser.offline = json.RawMessage(`{"status":"refreshed","access_token":"signed-in-session"}`)
	if status, body := request(first); status != 200 || !strings.Contains(body, "signed-in-session") || browser.starts != 1 {
		t.Fatalf("signed-in session handoff status %d body %s starts %d", status, body, browser.starts)
	}
	if err := api.RevokeToken(first); err != nil {
		t.Fatal(err)
	}
	if status, _ := request(first); status != 401 {
		t.Fatalf("revoked status %d", status)
	}
}

func TestBrowserLoginStartsGenericHelperAndReturnsViewerURL(t *testing.T) {
	browser := &integrationBrowser{}
	service := browsercontroller.NewService(nil, func(browsercontroller.ServiceConfig) browsercontroller.ServiceBrowser { return browser })
	config := browsercontroller.ServiceConfig{Origin: "https://capability.example.test", StartupURL: "https://capability.example.test", ProfileVolume: "capability-profile", ContainerName: "generic-browser-login"}
	viewer, err := service.StartConfiguration(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if viewer != "http://localhost:49001" || browser.starts != 1 {
		t.Fatalf("viewer URL = %q, helper starts = %d", viewer, browser.starts)
	}
}
