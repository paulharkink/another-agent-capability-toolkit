package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const (
	defaultProbeTimeout = 3 * time.Second
	probeBodyLimit      = 64 << 10
)

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
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, probeBodyLimit+1))
	return readErr == nil && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
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
