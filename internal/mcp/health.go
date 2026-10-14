package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	healthWaitLimit = 5 * time.Minute
	healthBodyLimit = 64 << 10
)

// Health checks MCP protocol initialization readiness. Authentication health
// is a separate package-declared signal handled by WaitForAuthenticated.
func Health(ctx context.Context, endpoint, transport string) error {
	return health(ctx, endpoint, transport, nil, true)
}

// HealthWithHeaders checks MCP protocol readiness while including registration
// headers. Redirects are never followed because headers may contain credentials.
func HealthWithHeaders(ctx context.Context, endpoint, transport string, headers http.Header) error {
	return health(ctx, endpoint, transport, headers, false)
}

func health(ctx context.Context, endpoint, transport string, headers http.Header, followRedirects bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	method := http.MethodPost
	var body io.Reader = bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"aact","version":"0.1"}}}`)
	if transport == "sse" {
		method = http.MethodGet
		body = nil
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	if !followRedirects {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("MCP health HTTP %d", response.StatusCode)
	}
	if transport == "sse" {
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			return errors.New("MCP SSE content type missing")
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var data []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if initializeResponse([]byte(strings.Join(data, "\n"))) {
					return nil
				}
				data = nil
			} else if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return errors.New("MCP initialize returned invalid response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if !initializeResponse(data) {
		return errors.New("MCP initialize returned invalid response")
	}
	return nil
}

func initializeResponse(data []byte) bool {
	var response struct {
		ID     json.RawMessage `json:"id"`
		Result map[string]any  `json:"result"`
		Error  any             `json:"error"`
	}
	return json.Unmarshal(data, &response) == nil && string(response.ID) == "1" && response.Error == nil && response.Result != nil && response.Result["protocolVersion"] != nil
}

func validateHealthPath(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" || strings.Contains(value, `\`) || strings.Contains(value, "#") {
		return errors.New("health path must be an absolute path without origin, query, or fragment")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return errors.New("health path must not escape its origin path")
		}
	}
	return nil
}

func resolveHealthURL(instanceURL, healthPath string) (string, error) {
	if err := validateHealthPath(healthPath); err != nil {
		return "", err
	}
	base, err := url.Parse(instanceURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return "", errors.New("owned MCP instance has an invalid HTTP URL")
	}
	path, err := url.Parse(healthPath)
	if err != nil {
		return "", errors.New("health path is invalid")
	}
	base.Path = path.Path
	base.RawPath = path.RawPath
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return base.String(), nil
}

func waitForAuthenticated(ctx context.Context, client *http.Client, instanceURL, healthPath string, headers http.Header, interval time.Duration) error {
	endpoint, err := resolveHealthURL(instanceURL, healthPath)
	if err != nil {
		return err
	}
	if client == nil {
		client = http.DefaultClient
	}
	// Health probes may carry profile registration headers, so never follow a
	// redirect that could send them to another origin. Copy the client to keep
	// this policy local to the probe and preserve all other caller settings.
	probeClient := *client
	probeClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, healthWaitLimit)
	defer cancel()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return errors.New("could not create MCP health request")
		}
		for name, values := range headers {
			for _, value := range values {
				request.Header.Add(name, value)
			}
		}
		response, err := probeClient.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("MCP health request failed")
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("MCP health endpoint returned HTTP %d", response.StatusCode)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, healthBodyLimit+1))
		response.Body.Close()
		if readErr != nil || len(body) > healthBodyLimit {
			return errors.New("MCP health response was unreadable or too large")
		}
		var payload struct {
			Authenticated *bool `json:"authenticated"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil || payload.Authenticated == nil {
			return errors.New("MCP health response must contain a boolean authenticated field")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return errors.New("MCP health response must contain exactly one JSON object")
		}
		if *payload.Authenticated {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func WaitForAuthenticated(ctx context.Context, instanceURL, healthPath string, headers http.Header) error {
	return waitForAuthenticated(ctx, http.DefaultClient, instanceURL, healthPath, headers, time.Second)
}
