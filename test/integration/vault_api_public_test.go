package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

func TestDetachedVaultHTTPScopesAndPersistsContainerRevocation(t *testing.T) {
	const password = "isolated-public-vault-api-password"
	keyA := state.Key{Source: "public-test", Package: "vault-api", Target: "namespace-a"}
	keyB := state.Key{Source: "public-test", Package: "vault-api", Target: "namespace-b"}
	namespaceA := keyA.ID()
	namespaceB := keyB.ID()
	root := t.TempDir()
	vaultPath := filepath.Join(root, "credentials.kdbx")
	store, err := vault.Open(vaultPath, []byte(password))
	if err != nil {
		t.Fatalf("create isolated Vault: %v", err)
	}
	for _, value := range []struct{ namespace, name, secret string }{
		{namespaceA, "shared", "namespace-a-value"},
		{namespaceB, "shared", "namespace-b-value"},
	} {
		if err := store.Put(value.namespace, value.name, []byte(value.secret)); err != nil {
			t.Fatalf("seed isolated Vault: %v", err)
		}
	}
	database, err := state.Open(root)
	if err != nil {
		t.Fatalf("open isolated public state store: %v", err)
	}
	database.SetSecretBackend(store)
	if err := database.SaveSecretInputs(keyA, map[string]any{
		"allowed_input": "allowed-private-value",
		"other_input":   "out-of-scope-value",
	}, nil); err != nil {
		t.Fatalf("save namespace A private inputs through state API: %v", err)
	}
	if err := database.SaveSecretInputs(keyB, map[string]any{
		"allowed_input": "other-namespace-private-value",
	}, nil); err != nil {
		t.Fatalf("save namespace B private inputs through state API: %v", err)
	}
	controlToken, err := vault.ControlTokenForVault(vaultPath, []byte(password))
	if err != nil {
		t.Fatalf("prepare isolated daemon control credential: %v", err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	binary := buildPublicCLIBinary(t)
	info, err := vault.StartDetached(binary, root, []byte(password))
	if err != nil {
		t.Fatalf("start isolated AACT daemon: %v", err)
	}
	t.Cleanup(func() {
		defer clear(controlToken)
		if vault.DaemonRunning(info) {
			if err := vault.StopDetached(info, controlToken); err != nil {
				t.Errorf("stop isolated Vault daemon during cleanup: %v", err)
			}
		}
	})

	oldToken, err := vault.IssueContainerToken(info, controlToken, namespaceA)
	if err != nil {
		t.Fatalf("issue first namespace token through daemon control API: %v", err)
	}
	if err := vault.BindContainerToken(info, controlToken, "old-container", oldToken); err != nil {
		t.Fatalf("bind first namespace token: %v", err)
	}
	otherToken, err := vault.IssueContainerToken(info, controlToken, namespaceB)
	if err != nil {
		t.Fatalf("issue second namespace token through daemon control API: %v", err)
	}
	if err := vault.BindContainerToken(info, controlToken, "other-container", otherToken); err != nil {
		t.Fatalf("bind second namespace token: %v", err)
	}
	scopedToken, err := vault.IssueContainerPrivateInputToken(info, controlToken, namespaceA, []string{"allowed_input"})
	if err != nil {
		t.Fatalf("issue scoped private-input token through daemon control API: %v", err)
	}
	if err := vault.BindContainerToken(info, controlToken, "scoped-container", scopedToken); err != nil {
		t.Fatalf("bind scoped private-input token: %v", err)
	}
	otherScopedToken, err := vault.IssueContainerPrivateInputToken(info, controlToken, namespaceB, []string{"allowed_input"})
	if err != nil {
		t.Fatalf("issue second namespace private-input token: %v", err)
	}
	if err := vault.BindContainerToken(info, controlToken, "other-scoped-container", otherScopedToken); err != nil {
		t.Fatalf("bind second namespace private-input token: %v", err)
	}

	client := publicVaultHTTPClient(t, info)
	request := func(client *http.Client, token, method, path string, body io.Reader) *http.Response {
		t.Helper()
		endpoint := strings.TrimRight(info.APIAddress, "/") + path
		req, err := http.NewRequestWithContext(context.Background(), method, endpoint, body)
		if err != nil {
			t.Fatalf("build Vault request: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("request Vault public endpoint %s: %v", path, err)
		}
		return response
	}

	if response := request(client, "", http.MethodGet, "/v1/secrets/shared", nil); response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("unauthenticated Vault request status=%d, want 401", response.StatusCode)
	} else {
		response.Body.Close()
	}
	if response := request(client, "invalid-token", http.MethodGet, "/v1/secrets/shared", nil); response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("invalid bearer status=%d, want 401", response.StatusCode)
	} else {
		response.Body.Close()
	}
	if response := request(client, oldToken, http.MethodGet, "/v1/secrets", nil); response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("namespace list status=%d body=%s", response.StatusCode, data)
	} else {
		var values map[string]string
		if err := json.NewDecoder(response.Body).Decode(&values); err != nil {
			response.Body.Close()
			t.Fatalf("decode namespace list: %v", err)
		}
		response.Body.Close()
		if values["shared"] != "namespace-a-value" || strings.Contains(fmt.Sprint(values), "namespace-b-value") {
			t.Fatalf("token listed values outside its namespace: %#v", values)
		}
	}
	if response := request(client, oldToken, http.MethodPut, "/v1/secrets/written", strings.NewReader(`{"value":"written-value"}`)); response.StatusCode != http.StatusNoContent {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("namespace write status=%d body=%s", response.StatusCode, data)
	} else {
		response.Body.Close()
	}
	if response := request(client, oldToken, http.MethodGet, "/v1/secrets/written", nil); response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("read-after-write status=%d, want 200", response.StatusCode)
	} else {
		var value struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if value.Value != "written-value" {
			t.Fatalf("read-after-write returned %q", value.Value)
		}
	}
	if response := request(client, oldToken, http.MethodDelete, "/v1/secrets/written", nil); response.StatusCode != http.StatusNoContent {
		response.Body.Close()
		t.Fatalf("namespace delete status=%d, want 204", response.StatusCode)
	} else {
		response.Body.Close()
	}
	if response := request(client, oldToken, http.MethodPost, "/v1/secrets/written", strings.NewReader(`{}`)); response.StatusCode != http.StatusMethodNotAllowed {
		response.Body.Close()
		t.Fatalf("unsupported secret method status=%d, want 405", response.StatusCode)
	} else {
		response.Body.Close()
	}
	if response := request(client, otherToken, http.MethodGet, "/v1/secrets/shared", nil); response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("second namespace read status=%d, want 200", response.StatusCode)
	} else {
		var value struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if value.Value != "namespace-b-value" {
			t.Fatalf("second namespace read returned %q", value.Value)
		}
	}
	if response := request(client, scopedToken, http.MethodGet, "/v1/private-inputs/allowed_input", nil); response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("declared private input status=%d, want 200", response.StatusCode)
	} else {
		var value struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if value.Value != "allowed-private-value" {
			t.Fatalf("declared private input returned %q", value.Value)
		}
	}
	for _, tc := range []struct {
		method string
		path   string
		body   io.Reader
	}{
		{http.MethodGet, "/v1/private-inputs/other_input", nil},
		{http.MethodGet, "/v1/secrets", nil},
		{http.MethodGet, "/v1/secrets/aact-private-inputs-v1", nil},
		{http.MethodPut, "/v1/secrets/allowed_input", strings.NewReader(`{"value":"unauthorized-write"}`)},
		{http.MethodDelete, "/v1/secrets/allowed_input", nil},
	} {
		response := request(client, scopedToken, tc.method, tc.path, tc.body)
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("scoped token %s %s status=%d, want 403", tc.method, tc.path, response.StatusCode)
		}
	}
	if response := request(client, otherScopedToken, http.MethodGet, "/v1/private-inputs/allowed_input", nil); response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("second namespace scoped input status=%d, want 200", response.StatusCode)
	} else {
		var value struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if value.Value != "other-namespace-private-value" {
			t.Fatalf("private input returned across namespace boundary: %q", value.Value)
		}
	}

	if err := vault.StopDetached(info, controlToken); err != nil {
		t.Fatalf("stop isolated daemon before restart: %v", err)
	}
	info, err = vault.StartDetached(binary, root, []byte(password))
	if err != nil {
		t.Fatalf("restart isolated daemon from the same Vault: %v", err)
	}
	client = publicVaultHTTPClient(t, info)
	if response := request(client, scopedToken, http.MethodGet, "/v1/private-inputs/allowed_input", nil); response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("scoped credential did not survive daemon restart: status=%d", response.StatusCode)
	} else {
		response.Body.Close()
	}
	if err := vault.RevokeContainerTokensByID(info, controlToken, "old-container"); err != nil {
		t.Fatalf("revoke only the old container token through daemon control API: %v", err)
	}
	if response := request(client, oldToken, http.MethodGet, "/v1/secrets/shared", nil); response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("revoked token status=%d, want 401", response.StatusCode)
	} else {
		response.Body.Close()
	}
	if err := vault.StopDetached(info, controlToken); err != nil {
		t.Fatalf("stop isolated daemon after revocation: %v", err)
	}
	info, err = vault.StartDetached(binary, root, []byte(password))
	if err != nil {
		t.Fatalf("restart isolated daemon after revocation: %v", err)
	}
	client = publicVaultHTTPClient(t, info)
	if response := request(client, oldToken, http.MethodGet, "/v1/secrets/shared", nil); response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("revoked token after daemon restart status=%d, want 401", response.StatusCode)
	} else {
		response.Body.Close()
	}
	for _, stillValid := range []struct{ token, path string }{
		{otherToken, "/v1/secrets/shared"},
		{scopedToken, "/v1/private-inputs/allowed_input"},
		{otherScopedToken, "/v1/private-inputs/allowed_input"},
	} {
		response := request(client, stillValid.token, http.MethodGet, stillValid.path, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("revoking old-container changed another token: GET %s status=%d", stillValid.path, response.StatusCode)
		}
	}
}

func publicVaultHTTPClient(t *testing.T, info vault.DaemonInfo) *http.Client {
	t.Helper()
	client, err := vault.NewContainerHTTPClient(info)
	if err != nil {
		t.Fatalf("create certificate-pinned Vault client: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Vault client transport type=%T", client.Transport)
	}
	transport = transport.Clone()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		apiURL, err := url.Parse(info.APIAddress)
		if err != nil || apiURL.Port() == "" {
			return nil, fmt.Errorf("invalid isolated API endpoint: %q", info.APIAddress)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(info.APIListenIP, apiURL.Port()))
	}
	client.Transport = transport
	return client
}
