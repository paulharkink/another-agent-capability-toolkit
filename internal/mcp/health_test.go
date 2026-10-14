package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestWaitForAuthenticatedTransportFailureIncludesSafeCause(t *testing.T) {
	privateURL := "http://user-secret:password-secret@example.test/private-path-secret?token=query-secret"
	privateHeader := "header-secret"
	transportErr := &url.Error{
		Op:  "Get",
		URL: privateURL,
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Profile-Token") != privateHeader {
			t.Errorf("health request omitted profile header")
		}
		return nil, transportErr
	})}
	err := waitForAuthenticated(context.Background(), client, "http://example.test/instance-path-secret?instance-query-secret", "/health", http.Header{"X-Profile-Token": []string{privateHeader}}, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("transport error = %v, want safe connection refused diagnostic", err)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("transport error lost its underlying cause: %v", err)
	}
	for _, marker := range []string{"user-secret", "password-secret", "private-path-secret", "query-secret", "header-secret", "instance-path-secret", "instance-query-secret"} {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("transport error leaked sensitive marker %q: %v", marker, err)
		}
	}
}

func TestHealthWithHeadersTransportFailureSanitizesURL(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	endpoint := "http://user-secret:password-secret@" + address + "/private-path-secret?token=query-secret"
	err = HealthWithHeaders(context.Background(), endpoint, "streamable-http", http.Header{"X-Profile-Token": []string{"header-secret"}})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("health error = %v, want safe connection refused diagnostic", err)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("health error lost its underlying cause: %v", err)
	}
	for _, marker := range []string{"user-secret", "password-secret", "private-path-secret", "query-secret", "header-secret"} {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("health error leaked sensitive marker %q: %v", marker, err)
		}
	}
}

func TestHealthWithHeadersRejectsRedirectWithoutForwardingCredentials(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Get("Authorization") != ""
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()

	err := HealthWithHeaders(context.Background(), redirect.URL, "streamable-http", http.Header{"Authorization": []string{"Bearer private-token"}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect response = %v, want HTTP 302 readiness error", err)
	}
	if forwarded {
		t.Fatal("credential header was forwarded to redirect target")
	}
}

func TestHealthWithHeadersRejectsUnauthorizedInitialize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	if err := HealthWithHeaders(context.Background(), server.URL, "streamable-http", nil); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("unauthorized initialize result = %v, want HTTP 401 error", err)
	}
}

func TestHealthSSEUsesEndpointEventAndReadsInitializeResponse(t *testing.T) {
	posted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sse":
			if r.Header.Get("X-Profile-Token") != "profile-secret" {
				t.Errorf("SSE request omitted profile header")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "event: endpoint\ndata: /messages?sessionId=fixture\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-posted:
			case <-r.Context().Done():
			case <-time.After(time.Second):
				t.Error("initialize was not posted before the SSE stream closed")
				return
			}
			_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2024-11-05\"}}\n\n")
			w.(http.Flusher).Flush()
		case r.Method == http.MethodPost && r.URL.Path == "/messages" && r.URL.Query().Get("sessionId") == "fixture":
			if r.Header.Get("X-Profile-Token") != "profile-secret" {
				t.Errorf("initialize POST omitted profile header")
			}
			if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("initialize content type = %q", got)
			}
			var request struct {
				Method string `json:"method"`
				ID     int    `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode initialize request: %v", err)
			} else if request.Method != "initialize" || request.ID != 1 {
				t.Errorf("initialize request = %+v", request)
			}
			close(posted)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected SSE health request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	err := HealthWithHeaders(context.Background(), server.URL+"/sse", "sse", http.Header{"X-Profile-Token": []string{"profile-secret"}})
	if err != nil {
		t.Fatalf("legacy SSE health check failed: %v", err)
	}
}

func TestSafeHealthTransportCauseClassifiesWindowsConnectionRefused(t *testing.T) {
	const wsaConnRefused syscall.Errno = 10061
	err := &net.OpError{Op: "dial", Net: "tcp", Err: wsaConnRefused}
	if got := safeHealthTransportCause(err); got != "connection refused" {
		t.Fatalf("safe transport cause = %q, want connection refused", got)
	}
	wrapped := safeHealthTransportError(err)
	if !errors.Is(wrapped, wsaConnRefused) {
		t.Fatalf("transport wrapper lost WSA error: %v", wrapped)
	}
	if !errors.Is(wrapped, syscall.ECONNREFUSED) {
		t.Fatalf("transport wrapper lost canonical connection-refused cause: %v", wrapped)
	}
	var opErr *net.OpError
	if !errors.As(wrapped, &opErr) {
		t.Fatalf("transport wrapper lost net.OpError: %v", wrapped)
	}
}

func TestHealthPathMustBeAbsolutePathWithoutURLComponents(t *testing.T) {
	for _, path := range []string{"", "health", "//evil.test/health", "https://evil.test/health", "/health?token=x", "/health#fragment", "/../health", "/foo/..", "/%2e%2e/health"} {
		t.Run(path, func(t *testing.T) {
			if err := validateHealthPath(path); err == nil {
				t.Fatalf("unsafe health path %q accepted", path)
			}
		})
	}
	if err := validateHealthPath("/health"); err != nil {
		t.Fatalf("valid health path rejected: %v", err)
	}
	if err := validateHealthPath("/api/health/"); err != nil {
		t.Fatalf("valid trailing slash rejected: %v", err)
	}
}

func TestHealthURLStaysOnOwnedInstanceOrigin(t *testing.T) {
	got, err := resolveHealthURL("https://owned.example:8443/mcp", "/health")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://owned.example:8443/health" {
		t.Fatalf("health URL = %q", got)
	}
	for _, path := range []string{"//attacker.example/health", "https://attacker.example/health"} {
		if got, err := resolveHealthURL("https://owned.example/mcp", path); err == nil || strings.Contains(got, "attacker.example") {
			t.Fatalf("origin changed for %q: %q, %v", path, got, err)
		}
	}
}

func TestWaitForAuthenticatedPollsFalseUntilExplicitTrue(t *testing.T) {
	responses := []string{`{"authenticated":false}`, `{"authenticated":true}`}
	var seen int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.Path != "/health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer selected-profile-token" {
			t.Errorf("authorization header missing from health request")
		}
		_, _ = w.Write([]byte(responses[min(seen-1, len(responses)-1)]))
	}))
	defer server.Close()
	headers := http.Header{"Authorization": []string{"Bearer selected-profile-token"}}
	if err := waitForAuthenticated(context.Background(), server.Client(), server.URL+"/mcp", "/health", headers, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("health polls = %d, want false then true", seen)
	}
}

func TestWaitForAuthenticatedRejectsCrossOriginRedirectWithoutForwardingHeaders(t *testing.T) {
	var secondRequests int
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondRequests++
		if got := r.Header.Get("X-Profile-Token"); got != "" {
			t.Errorf("sensitive profile header reached redirected origin: %q", got)
		}
		_, _ = w.Write([]byte(`{"authenticated":true}`))
	}))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", second.URL+"/health")
		w.WriteHeader(http.StatusFound)
	}))
	defer first.Close()

	err := waitForAuthenticated(context.Background(), first.Client(), first.URL, "/health", http.Header{"X-Profile-Token": []string{"selected-secret"}}, time.Millisecond)
	if err == nil {
		t.Fatal("health probe accepted a redirect to another origin")
	}
	if secondRequests != 0 {
		t.Fatalf("redirect target received %d requests, want none", secondRequests)
	}
}

func TestWaitForAuthenticatedRejectsMalformedAndUnexpectedResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"missing", `{}`, http.StatusOK},
		{"wrong type", `{"authenticated":"true"}`, http.StatusOK},
		{"malformed", `nope`, http.StatusOK},
		{"http error", `{"authenticated":true}`, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := waitForAuthenticated(ctx, server.Client(), server.URL, "/health", nil, time.Millisecond); err == nil {
				t.Fatal("invalid health response accepted")
			}
		})
	}
}

func TestWaitForAuthenticatedHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"authenticated":false}`)) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForAuthenticated(ctx, server.Client(), server.URL, "/health", nil, time.Millisecond); err == nil {
		t.Fatal("cancelled wait succeeded")
	}
}
