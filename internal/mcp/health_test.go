package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
