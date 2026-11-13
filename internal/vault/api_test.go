package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstanceAPIRequiresBearerAndScopesEveryOperation(t *testing.T) {
	store, err := Open(t.TempDir()+"/credentials.kdbx", []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	instanceA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	instanceB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	api := NewAPI(store, map[string]string{"token-a": instanceA, "token-b": instanceB})
	request := func(token, method, path string, body any) *httptest.ResponseRecorder {
		var encoded bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&encoded).Encode(body)
		}
		req := httptest.NewRequest(method, path, &encoded)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		return res
	}
	if got := request("", http.MethodGet, "/v1/secrets", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", got)
	}
	put := request("token-a", http.MethodPut, "/v1/secrets/access-token", map[string]string{"value": "secret-a"})
	if put.Code != http.StatusNoContent {
		t.Fatalf("put status=%d body=%s", put.Code, put.Body.String())
	}
	get := request("token-a", http.MethodGet, "/v1/secrets/access-token", nil)
	var response struct {
		Value string `json:"value"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &response) != nil || response.Value != "secret-a" {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}
	if got := request("token-b", http.MethodGet, "/v1/secrets/access-token", nil).Code; got != http.StatusNotFound {
		t.Fatalf("cross-instance read status=%d", got)
	}
	listed := request("token-a", http.MethodGet, "/v1/secrets", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	deleted := request("token-a", http.MethodDelete, "/v1/secrets/access-token", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", deleted.Code)
	}
}

func TestContainerPrivateInputTokenCanOnlyReadItsDeclaredInputs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	namespace := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	encoded, _ := json.Marshal(map[string]any{"version": 1, "values": map[string]any{"github_token": "github-secret", "gitlab_token": "gitlab-secret"}})
	if err := store.Put(namespace, "aact-private-inputs-v1", encoded); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	control := controlHandler(api, "control-token", func() {}, nil)
	issue := func(name string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"namespace": namespace, "inputs": []string{name}})
		req := httptest.NewRequest(http.MethodPost, "/tokens/private-inputs", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer control-token")
		res := httptest.NewRecorder()
		control.ServeHTTP(res, req)
		if res.Code != http.StatusCreated {
			t.Fatalf("issue scoped token for %s: status=%d body=%s", name, res.Code, res.Body.String())
		}
		var result struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || result.Token == "" {
			t.Fatalf("issued scoped token response: %#v err=%v", result, err)
		}
		return result.Token
	}
	githubToken := issue("github_token")
	gitlabToken := issue("gitlab_token")
	if err := api.BindTokenContainer(githubToken, "container-github"); err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(gitlabToken, "container-gitlab"); err != nil {
		t.Fatal(err)
	}
	request := func(token, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req := httptest.NewRequest(method, path, &payload)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		return res
	}
	var response struct {
		Value string `json:"value"`
	}
	got := request(githubToken, http.MethodGet, "/v1/private-inputs/github_token", nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &response) != nil || response.Value != "github-secret" {
		t.Fatalf("github input read: status=%d body=%s", got.Code, got.Body.String())
	}
	for _, tc := range []struct{ token, method, path string }{
		{githubToken, http.MethodGet, "/v1/private-inputs/gitlab_token"},
		{githubToken, http.MethodGet, "/v1/secrets"},
		{githubToken, http.MethodGet, "/v1/secrets/aact-private-inputs-v1"},
		{githubToken, http.MethodPut, "/v1/secrets/github_token"},
		{githubToken, http.MethodDelete, "/v1/secrets/github_token"},
		{gitlabToken, http.MethodGet, "/v1/private-inputs/github_token"},
	} {
		if response := request(tc.token, tc.method, tc.path, map[string]string{"value": "attacker-write"}); response.Code != http.StatusForbidden {
			t.Errorf("%s %s status=%d, want forbidden", tc.method, tc.path, response.Code)
		}
	}
	if err := api.LoadTokens(); err != nil {
		t.Fatal(err)
	}
	restarted := NewAPI(store, nil)
	if err := restarted.LoadTokens(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/private-inputs/gitlab_token", nil)
	req.Header.Set("Authorization", "Bearer "+githubToken)
	res := httptest.NewRecorder()
	restarted.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("restarted scoped token cross-read status=%d, want forbidden", res.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/private-inputs/github_token", nil)
	req.Header.Set("Authorization", "Bearer "+githubToken)
	res = httptest.NewRecorder()
	restarted.ServeHTTP(res, req)
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &response) != nil || response.Value != "github-secret" {
		t.Fatalf("restarted scoped token allowed read: status=%d body=%s", res.Code, res.Body.String())
	}
	if err := restarted.RevokeTokensForContainer(namespace, "container-github"); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/private-inputs/github_token", nil)
	req.Header.Set("Authorization", "Bearer "+githubToken)
	res = httptest.NewRecorder()
	restarted.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("revoked scoped token status=%d, want unauthorized", res.Code)
	}
}

func TestContainerTokenCanBeRevokedByExactIDWithoutNamespace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	first, err := api.IssuePrivateInputToken("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"github_token"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := api.IssuePrivateInputToken("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", []string{"gitlab_token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(first, "container-github"); err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(second, "container-gitlab"); err != nil {
		t.Fatal(err)
	}
	control := controlHandler(api, "control-token", func() {}, nil)
	req := httptest.NewRequest(http.MethodDelete, "/tokens/containers/container-github/all", nil)
	req.Header.Set("Authorization", "Bearer control-token")
	res := httptest.NewRecorder()
	control.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("revoke by exact container ID status=%d body=%s", res.Code, res.Body.String())
	}
	if _, ok := api.tokens[first]; ok {
		t.Fatal("token for exact missing container remained active")
	}
	if api.tokens[second] == "" {
		t.Fatal("revoking one container affected a different container token")
	}
}

func TestInstanceAPIRejectsMalformedRequestsAndMethods(t *testing.T) {
	store, err := Open(t.TempDir()+"/credentials.kdbx", []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, map[string]string{"token-a": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/secrets/key", "{}"},
		{http.MethodPut, "/v1/secrets/", `{"value":"x"}`},
		{http.MethodPut, "/v1/secrets/key", `{"value":4}`},
		{http.MethodPut, "/v1/secrets/key", `{"value":"x","namespace":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Authorization", "Bearer token-a")
		res := httptest.NewRecorder()
		api.ServeHTTP(res, req)
		if res.Code < 400 || res.Code >= 500 {
			t.Errorf("%s %s status=%d", tc.method, tc.path, res.Code)
		}
	}
}

func TestAPIIssuesAndRevokesIndependentInstanceTokens(t *testing.T) {
	store, err := Open(t.TempDir()+"/credentials.kdbx", []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	instance := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/secrets", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("issued token status=%d", response.Code)
	}
	restarted := NewAPI(store, nil)
	if err := restarted.LoadTokens(); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	restarted.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("restored token status=%d", response.Code)
	}
	if err := api.RevokeToken(token); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d", response.Code)
	}
}

func TestDaemonServesAPIAndClosesWithContext(t *testing.T) {
	store, err := Open(t.TempDir()+"/credentials.kdbx", []byte("test-only-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store, nil)
	token, err := api.IssueToken("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := StartDaemon(ctx, listener, api)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, daemon.URL()+"/v1/secrets", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	cancel()
	if err := daemon.Wait(); err != nil {
		t.Fatalf("daemon shutdown: %v", err)
	}
}

func TestContainerTokenOwnershipSurvivesDaemonRestartAndRevokesOnlyOldContainer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.kdbx")
	password := []byte("container-owner-test-password")
	store, err := Open(path, password)
	if err != nil {
		t.Fatal(err)
	}
	instance := strings.Repeat("a", 64)
	api := NewAPI(store, nil)
	oldToken, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(oldToken, "old-container"); err != nil {
		t.Fatal(err)
	}
	newToken, err := api.IssueToken(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(newToken, "new-container"); err != nil {
		t.Fatal(err)
	}
	restartedStore, err := Open(path, password)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewAPI(restartedStore, nil)
	if err := restarted.LoadTokens(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RevokeTokensForContainer(instance, "old-container"); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.tokens[oldToken]; ok {
		t.Fatal("old container token remained active after restart")
	}
	if got := restarted.tokens[newToken]; got != instance {
		t.Fatalf("replacement token namespace=%q", got)
	}
}
