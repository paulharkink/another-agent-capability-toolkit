package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const (
	defaultProbeTimeout = 3 * time.Second
	probeBodyLimit      = 64 << 10
	mcpErrorBodyLimit   = 64 << 10
)

const invalidMCPSessionMessage = "Bad Request: No valid session ID provided"

type preservedResponseBody struct {
	io.Reader
	io.Closer
}

type Config struct {
	Provider       string
	UpstreamURL    string
	APIBaseURL     string
	GitHubHost     string
	GitLabURL      string
	BitbucketURL   string
	BitbucketToken string
	ProbeTimeout   time.Duration
	ChildReady     func() bool
}

// GitHubAPIBase mirrors the REST target selected by the pinned GitHub MCP
// server v1.10.1. Its host resolver discards the configured path, uses the
// dotcom API for github.com and its subdomains, maps ghe.com tenants to api.*,
// and uses /api/v3 for other enterprise hosts.
func GitHubAPIBase(host string) (*url.URL, error) {
	if strings.TrimSpace(host) == "" {
		return url.Parse("https://api.github.com/")
	}
	u, err := url.Parse(strings.TrimSpace(host))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("GitHub host is unsupported")
	}
	if err := requireSecureScheme(u); err != nil {
		return nil, errors.New("GitHub host is unsupported")
	}
	hostname := u.Hostname()
	switch {
	case hostname == "github.com" || strings.HasSuffix(hostname, ".github.com"):
		return url.Parse("https://api.github.com/")
	case hostname == "ghe.com" || strings.HasSuffix(hostname, ".ghe.com"):
		if u.Scheme == "http" {
			return nil, errors.New("GitHub host is unsupported")
		}
		return url.Parse("https://api." + hostname + "/")
	default:
		return url.Parse(u.Scheme + "://" + u.Host + "/api/v3/")
	}
}

func requireSecureScheme(u *url.URL) error {
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return errors.New("GitHub host is unsupported")
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func New(config Config) http.Handler {
	upstream, err := url.Parse(config.UpstreamURL)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		})
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(response *http.Response) error {
		if config.Provider != "bitbucket" {
			return nil
		}
		return translateExpiredSessionResponse(response)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			authenticated := backendReady(r.Context(), config, upstream) && probeIdentity(r.Context(), config, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(struct {
				Authenticated bool `json:"authenticated"`
			}{Authenticated: authenticated})
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func translateExpiredSessionResponse(response *http.Response) error {
	if response == nil || response.StatusCode != http.StatusBadRequest || response.Request == nil ||
		response.Request.Method != http.MethodPost || strings.TrimSpace(response.Request.Header.Get("Mcp-Session-Id")) == "" {
		return nil
	}

	originalBody := response.Body
	body, err := io.ReadAll(io.LimitReader(originalBody, mcpErrorBodyLimit+1))
	if err != nil {
		response.Body = preservedResponseBody{Reader: io.MultiReader(bytes.NewReader(body), originalBody), Closer: originalBody}
		return err
	}
	if len(body) > mcpErrorBodyLimit {
		response.Body = preservedResponseBody{Reader: io.MultiReader(bytes.NewReader(body), originalBody), Closer: originalBody}
		return nil
	}
	response.Body = preservedResponseBody{Reader: bytes.NewReader(body), Closer: originalBody}
	var rpcError struct {
		JSONRPC string `json:"jsonrpc"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcError); err != nil || rpcError.JSONRPC != "2.0" || rpcError.Error == nil || rpcError.Error.Code != -32000 || rpcError.Error.Message != invalidMCPSessionMessage {
		return nil
	}

	response.StatusCode = http.StatusNotFound
	response.Status = "404 Not Found"
	return nil
}

func backendReady(ctx context.Context, config Config, upstream *url.URL) bool {
	if config.ChildReady != nil && !config.ChildReady() {
		return false
	}
	timeout := config.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodHead, upstream.String(), nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, probeBodyLimit+1))
	return response.StatusCode < http.StatusInternalServerError
}

func probeIdentity(ctx context.Context, config Config, incoming *http.Request) bool {
	target, authName, authValue, ok := identityTarget(config, incoming)
	if !ok {
		return false
	}
	timeout := config.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return false
	}
	request.Header.Set(authName, authValue)
	client := &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if strings.EqualFold(config.Provider, "github") && isGitHubInstallationAuthorization(authValue) {
		if response.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, probeBodyLimit+1))
			return false
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, probeBodyLimit+1))
		return readErr == nil && len(body) <= probeBodyLimit && validGitHubInstallationRepositories(body)
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, probeBodyLimit+1))
	return readErr == nil && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
}

func isGitHubInstallationAuthorization(value string) bool {
	parts := strings.Fields(value)
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && strings.HasPrefix(parts[1], "ghs_")
}

func validGitHubInstallationRepositories(body []byte) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return false
	}
	rawTotalCount, ok := object["total_count"]
	if !ok {
		return false
	}
	totalCount, ok := new(big.Int).SetString(strings.TrimSpace(string(rawTotalCount)), 10)
	if !ok || totalCount.Sign() < 0 {
		return false
	}
	repositories, ok := object["repositories"]
	if !ok {
		return false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(repositories, &entries); err != nil || entries == nil || totalCount.Cmp(big.NewInt(int64(len(entries)))) < 0 {
		return false
	}
	for _, entry := range entries {
		var repository map[string]json.RawMessage
		if err := json.Unmarshal(entry, &repository); err != nil || repository == nil {
			return false
		}
		rawID, ok := repository["id"]
		if !ok {
			return false
		}
		id, ok := new(big.Int).SetString(strings.TrimSpace(string(rawID)), 10)
		if !ok || id.Sign() <= 0 {
			return false
		}
	}
	return true
}

func identityTarget(config Config, incoming *http.Request) (*url.URL, string, string, bool) {
	base, err := configuredAPIBase(config)
	if err != nil {
		return nil, "", "", false
	}
	var suffix, headerName, headerValue string
	switch strings.ToLower(config.Provider) {
	case "github":
		headerValue = incoming.Header.Get("Authorization")
		if headerValue == "" {
			return nil, "", "", false
		}
		headerName, suffix = "Authorization", "user"
		if isGitHubInstallationAuthorization(headerValue) {
			suffix = "installation/repositories?per_page=1"
		}
	case "gitlab":
		switch {
		case incoming.Header.Get("Private-Token") != "":
			headerName, headerValue, suffix = "Private-Token", incoming.Header.Get("Private-Token"), "user"
		case incoming.Header.Get("JOB-TOKEN") != "":
			headerName, headerValue, suffix = "JOB-TOKEN", incoming.Header.Get("JOB-TOKEN"), "job"
		case incoming.Header.Get("Authorization") != "":
			headerName, headerValue, suffix = "Authorization", incoming.Header.Get("Authorization"), "user"
		default:
			return nil, "", "", false
		}
	case "bitbucket":
		headerName, headerValue, suffix = "Authorization", "Bearer "+config.BitbucketToken, "rest/api/latest/dashboard/pull-requests?limit=1"
		if config.BitbucketToken == "" {
			return nil, "", "", false
		}
	default:
		return nil, "", "", false
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + suffix
	base.RawPath = ""
	base.RawQuery = ""
	if strings.Contains(suffix, "?") {
		parts := strings.SplitN(suffix, "?", 2)
		base.Path = strings.TrimRight(configuredBasePath(config, base), "/") + "/" + parts[0]
		base.RawQuery = parts[1]
	}
	return base, headerName, headerValue, true
}

func configuredAPIBase(config Config) (*url.URL, error) {
	configured := config.APIBaseURL
	if strings.TrimSpace(configured) == "" {
		switch strings.ToLower(config.Provider) {
		case "gitlab":
			configured = config.GitLabURL
		case "bitbucket":
			configured = config.BitbucketURL
		}
	}
	if strings.TrimSpace(configured) != "" {
		base, err := url.Parse(strings.TrimSpace(configured))
		if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
			return nil, errors.New("provider URL is unsupported")
		}
		if config.Provider == "gitlab" && !strings.HasSuffix(strings.TrimRight(base.Path, "/"), "/api/v4") {
			base.Path = strings.TrimRight(base.Path, "/") + "/api/v4"
		}
		return base, nil
	}
	if strings.EqualFold(config.Provider, "github") {
		return GitHubAPIBase(config.GitHubHost)
	}
	return nil, errors.New("provider URL is missing")
}

func configuredBasePath(config Config, full *url.URL) string {
	if base, err := configuredAPIBase(config); err == nil {
		return base.Path
	}
	return full.Path
}
