package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthChecksConfiguredIdentityAndDoesNotForwardArbitraryHeaders(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider string
		apiPath  string
		status   int
		want     bool
	}{
		{name: "github bearer identity", provider: "github", apiPath: "/user", status: http.StatusOK, want: true},
		{name: "gitlab bearer identity", provider: "gitlab", apiPath: "/api/v4/user", status: http.StatusOK, want: true},
		{name: "bitbucket private dashboard", provider: "bitbucket", apiPath: "/rest/api/latest/dashboard/pull-requests?limit=1", status: http.StatusOK, want: true},
		{name: "invalid token", provider: "github", apiPath: "/user", status: http.StatusUnauthorized, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotPath, gotAuthorization, gotPrivate string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.RequestURI()
				gotAuthorization = r.Header.Get("Authorization")
				gotPrivate = r.Header.Get("X-Private-Registration-Header")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"id":1,"token":"must-not-be-returned"}`)
			}))
			defer provider.Close()

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Upstream", "preserved")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "event: message\ndata: {\"ok\":true}\n\n")
			}))
			defer upstream.Close()

			handler := New(Config{
				Provider: test.provider, UpstreamURL: upstream.URL,
				APIBaseURL: provider.URL, BitbucketToken: "bitbucket-secret",
				ProbeTimeout: 250 * time.Millisecond,
			})
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			request.Header.Set("Authorization", "Bearer selected-secret")
			request.Header.Set("X-Private-Registration-Header", "do-not-send-to-provider")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), test.want) {
				t.Fatalf("health = %d %q, want authenticated=%t", recorder.Code, recorder.Body.String(), test.want)
			}
			if gotPath != test.apiPath {
				t.Fatalf("provider path = %q, want %q", gotPath, test.apiPath)
			}
			if gotPrivate != "" {
				t.Fatalf("arbitrary registration header reached provider: %q", gotPrivate)
			}
			if test.provider == "bitbucket" {
				if gotAuthorization != "Bearer bitbucket-secret" {
					t.Fatalf("Bitbucket auth = %q, want selected container token", gotAuthorization)
				}
			} else if gotAuthorization != "Bearer selected-secret" {
				t.Fatalf("provider auth = %q, want selected request token", gotAuthorization)
			}
			if strings.Contains(recorder.Body.String(), "must-not-be-returned") {
				t.Fatalf("provider response leaked through health route: %q", recorder.Body.String())
			}
		})
	}
}

func TestMCPTransportIsStreamedThroughWithoutRewriting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/mcp?session=abc" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.RequestURI())
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "client-stream-payload" {
			t.Errorf("upstream body = %q", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "session-42")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "event: message\ndata: first\n\ndata: second\n\n")
	}))
	defer upstream.Close()

	handler := New(Config{Provider: "gitlab", UpstreamURL: upstream.URL})
	request := httptest.NewRequest(http.MethodPost, "/mcp?session=abc", strings.NewReader("client-stream-payload"))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Content-Type") != "text/event-stream" || recorder.Header().Get("Mcp-Session-Id") != "session-42" {
		t.Fatalf("stream response = %d headers=%v", recorder.Code, recorder.Header())
	}
	if recorder.Body.String() != "event: message\ndata: first\n\ndata: second\n\n" {
		t.Fatalf("stream body changed: %q", recorder.Body.String())
	}
}

func TestExpiredMCPPostSessionIsTranslatedToNotFound(t *testing.T) {
	const expiredSessionBody = `{"jsonrpc":"2.0","error":{"code":-32000,"message":"Bad Request: No valid session ID provided"},"id":1}`
	for _, test := range []struct {
		name       string
		provider   string
		method     string
		sessionID  string
		body       string
		wantStatus int
	}{
		{name: "expired session on POST", method: http.MethodPost, sessionID: "expired-session", body: expiredSessionBody, wantStatus: http.StatusNotFound},
		{name: "headerless POST remains bad request", method: http.MethodPost, body: expiredSessionBody, wantStatus: http.StatusBadRequest},
		{name: "unrelated POST error remains bad request", method: http.MethodPost, sessionID: "session", body: `{"jsonrpc":"2.0","error":{"code":-32000,"message":"Bad Request: Invalid request"},"id":1}`, wantStatus: http.StatusBadRequest},
		{name: "different JSON-RPC error code remains bad request", method: http.MethodPost, sessionID: "session", body: `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Bad Request: No valid session ID provided"},"id":1}`, wantStatus: http.StatusBadRequest},
		{name: "different JSON-RPC version remains bad request", method: http.MethodPost, sessionID: "session", body: `{"jsonrpc":"1.0","error":{"code":-32000,"message":"Bad Request: No valid session ID provided"},"id":1}`, wantStatus: http.StatusBadRequest},
		{name: "oversized error body remains intact", method: http.MethodPost, sessionID: "session", body: strings.Repeat("x", mcpErrorBodyLimit+10), wantStatus: http.StatusBadRequest},
		{name: "other provider retains exact 400", provider: "gitlab", method: http.MethodPost, sessionID: "expired-session", body: expiredSessionBody, wantStatus: http.StatusBadRequest},
		{name: "noncanonical provider name retains exact 400", provider: "BITBUCKET", method: http.MethodPost, sessionID: "expired-session", body: expiredSessionBody, wantStatus: http.StatusBadRequest},
		{name: "expired session on GET remains unchanged", method: http.MethodGet, sessionID: "expired-session", body: expiredSessionBody, wantStatus: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Mcp-Session-Id"); got != test.sessionID {
					t.Errorf("upstream session ID = %q, want %q", got, test.sessionID)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Upstream", "preserved")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()

			provider := test.provider
			if provider == "" {
				provider = "bitbucket"
			}
			handler := New(Config{Provider: provider, UpstreamURL: upstream.URL})
			request := httptest.NewRequest(test.method, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
			if test.sessionID != "" {
				request.Header.Set("Mcp-Session-Id", test.sessionID)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if recorder.Body.String() != test.body {
				t.Fatalf("response body changed: got %q, want %q", recorder.Body.String(), test.body)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			if got := recorder.Header().Get("X-Upstream"); got != "preserved" {
				t.Fatalf("X-Upstream = %q, want preserved", got)
			}
		})
	}
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, item := range environment {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func TestStartChildNormalizesOnlyBitbucketURLInChildEnvironment(t *testing.T) {
	for _, test := range []struct {
		provider string
		wantURL  string
	}{
		{provider: "bitbucket", wantURL: "https://bitbucket.example/context"},
		{provider: "gitlab", wantURL: "https://bitbucket.example/context///"},
	} {
		t.Run(test.provider, func(t *testing.T) {
			t.Setenv("MCP_HEALTH_PROVIDER", test.provider)
			t.Setenv("BITBUCKET_URL", "https://bitbucket.example/context///")
			t.Setenv("BITBUCKET_TOKEN", "secret-token")
			t.Setenv("AACT_GIT_HEALTH_HELPER", "1")

			child, done, err := startChild([]string{os.Args[0], "-test.run=TestOwnedChildHelperProcess"})
			if err != nil {
				t.Fatal(err)
			}
			defer stopChild(child, done)

			if value := environmentValue(child.Env, "BITBUCKET_URL"); value != test.wantURL {
				t.Fatalf("child BITBUCKET_URL = %q, want %q", value, test.wantURL)
			}
			if value := environmentValue(child.Env, "BITBUCKET_TOKEN"); value != "secret-token" {
				t.Fatalf("child BITBUCKET_TOKEN changed: %q", value)
			}
			if value := os.Getenv("BITBUCKET_URL"); value != "https://bitbucket.example/context///" {
				t.Fatalf("parent BITBUCKET_URL was mutated: %q", value)
			}
		})
	}
}

type closeTrackingBody struct {
	*strings.Reader
	closed bool
}

func (body *closeTrackingBody) Close() error {
	body.closed = true
	return nil
}

func TestExpiredMCPSessionResponseClosesUpstreamBody(t *testing.T) {
	upstreamBody := &closeTrackingBody{Reader: strings.NewReader(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"Bad Request: No valid session ID provided"},"id":1}`)}
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Mcp-Session-Id", "expired-session")
	response := &http.Response{StatusCode: http.StatusBadRequest, Request: request, Body: upstreamBody}

	if err := translateExpiredSessionResponse(response); err != nil {
		t.Fatalf("translate response: %v", err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("read translated response: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close translated response: %v", err)
	}
	if !upstreamBody.closed {
		t.Fatal("closing translated response did not close upstream body")
	}
}

func TestGitHubHostNormalizationMatchesPinnedServer(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "", want: "https://api.github.com/"},
		{input: "https://github.com", want: "https://api.github.com/"},
		{input: "https://api.github.com", want: "https://api.github.com/"},
		{input: "https://tenant.ghe.com", want: "https://api.tenant.ghe.com/"},
		{input: "https://tenant.ghe.com:8443/ignored/base", want: "https://api.tenant.ghe.com/"},
		{input: "https://ghe.com", want: "https://api.ghe.com/"},
		{input: "https://code.example.test/ignored/base", want: "https://code.example.test/api/v3/"},
		{input: "https://code.example.test:8443/ignored/base", want: "https://code.example.test:8443/api/v3/"},
		{input: "https://[::1]:8443/ignored/base", want: "https://[::1]:8443/api/v3/"},
		{input: "https://GHE.COM", want: "https://GHE.COM/api/v3/"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := GitHubAPIBase(test.input)
			if err != nil || got.String() != test.want {
				t.Fatalf("GitHub API base = %v, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestGitHubHTTPHostRequiresPinnedExactLoopback(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{
		{input: "http://LOCALHOST:8080/ignored", want: "http://LOCALHOST:8080/api/v3/", valid: true},
		{input: "http://127.0.0.1:8080/ignored", want: "http://127.0.0.1:8080/api/v3/", valid: true},
		{input: "http://[::1]:8080/ignored", want: "http://[::1]:8080/api/v3/", valid: true},
		{input: "http://127.0.0.2:8080/ignored", valid: false},
		{input: "http://127.0.1.1:8080/ignored", valid: false},
		{input: "http://[0:0:0:0:0:0:0:1]:8080/ignored", valid: false},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := GitHubAPIBase(test.input)
			if test.valid {
				if err != nil || got.String() != test.want {
					t.Fatalf("GitHub API base = %v, %v; want %q", got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("GitHub API base = %q, want unsupported HTTP host", got)
			}
		})
	}
}

func TestHealthRejectsNonExactLoopbackBeforeCredentialProbe(t *testing.T) {
	for _, host := range []string{"127.0.0.2", "127.0.1.1", "[0:0:0:0:0:0:0:1]"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("HTTP_PROXY", "")
			t.Setenv("HTTPS_PROXY", "")
			t.Setenv("ALL_PROXY", "")
			t.Setenv("http_proxy", "")
			t.Setenv("https_proxy", "")
			t.Setenv("all_proxy", "")
			listener, err := net.Listen("tcp", net.JoinHostPort(strings.Trim(host, "[]"), "0"))
			if err != nil {
				t.Skipf("loopback address unavailable for local fake provider: %v", err)
			}
			var providerCalls atomic.Int32
			provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			provider.Listener = listener
			provider.Start()
			defer provider.Close()
			_, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer upstream.Close()
			providerURL := "http://" + net.JoinHostPort(strings.Trim(host, "[]"), port)
			handler := New(Config{Provider: "github", UpstreamURL: upstream.URL, GitHubHost: providerURL})
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			request.Header.Set("Authorization", "Bearer test-only-token")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
				t.Fatalf("health = %d %q, want unauthenticated", recorder.Code, recorder.Body.String())
			}
			if calls := providerCalls.Load(); calls != 0 {
				t.Fatalf("insecure provider received %d credential probe requests", calls)
			}
		})
	}
}

func TestGitLabHealthUsesSameHeaderPrecedenceAsPinnedUpstream(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		wantPath   string
		wantHeader string
		wantValue  string
		wantAuth   bool
	}{
		{name: "invalid private token overrides valid bearer", status: http.StatusUnauthorized, wantPath: "/api/v4/user", wantHeader: "Private-Token", wantValue: "invalid-private", wantAuth: false},
		{name: "job token uses job identity route", status: http.StatusOK, wantPath: "/api/v4/job", wantHeader: "JOB-TOKEN", wantValue: "selected-job", wantAuth: true},
		{name: "bearer token uses user identity route", status: http.StatusOK, wantPath: "/api/v4/user", wantHeader: "Authorization", wantValue: "Bearer selected", wantAuth: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotPath, gotHeader, gotValue string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.RequestURI()
				for _, name := range []string{"Private-Token", "JOB-TOKEN", "Authorization"} {
					if value := r.Header.Get(name); value != "" {
						gotHeader, gotValue = name, value
					}
				}
				w.WriteHeader(test.status)
			}))
			defer provider.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer upstream.Close()

			handler := New(Config{Provider: "gitlab", UpstreamURL: upstream.URL, APIBaseURL: provider.URL + "/api/v4"})
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			request.Header.Set("Authorization", "Bearer selected")
			if test.wantHeader == "Private-Token" {
				request.Header.Set("Private-Token", "invalid-private")
			}
			if test.wantHeader == "JOB-TOKEN" {
				request.Header.Set("JOB-TOKEN", "selected-job")
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), test.wantAuth) {
				t.Fatalf("health = %d %q, want authenticated=%t", recorder.Code, recorder.Body.String(), test.wantAuth)
			}
			if gotPath != test.wantPath || gotHeader != test.wantHeader || gotValue != test.wantValue {
				t.Fatalf("probe = %q %s:%q, want %q %s:%q", gotPath, gotHeader, gotValue, test.wantPath, test.wantHeader, test.wantValue)
			}
		})
	}
}

func TestHealthRejectsProviderRedirectWithoutFollowingIt(t *testing.T) {
	var redirected bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer redirectTarget.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/stolen", http.StatusFound)
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()

	handler := New(Config{Provider: "github", UpstreamURL: upstream.URL, APIBaseURL: provider.URL})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer selected")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
		t.Fatalf("redirect health = %d %q, want false", recorder.Code, recorder.Body.String())
	}
	if redirected {
		t.Fatal("health probe followed provider redirect")
	}
}

func TestHealthReturnsFalseWhenChildBackendIsNotReady(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer provider.Close()
	handler := New(Config{Provider: "github", UpstreamURL: "http://127.0.0.1:1", APIBaseURL: provider.URL})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer selected")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
		t.Fatalf("unready child health = %d %q, want false", recorder.Code, recorder.Body.String())
	}
}

func TestHealthReturnsFalseWhenChildHasExited(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	handler := New(Config{
		Provider: "github", UpstreamURL: upstream.URL, APIBaseURL: provider.URL,
		ChildReady: func() bool { return false },
	})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer selected")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
		t.Fatalf("exited child health = %d %q, want false", recorder.Code, recorder.Body.String())
	}
}

func healthValue(data string, want bool) bool {
	return strings.Contains(data, `"authenticated":`+boolText(want))
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestOwnedChildHelperProcess(t *testing.T) {
	if os.Getenv("AACT_GIT_HEALTH_HELPER") != "1" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestWrapperShutdownStopsOwnedChildProcess(t *testing.T) {
	t.Setenv("AACT_GIT_HEALTH_HELPER", "1")
	child, done, err := startChild([]string{os.Args[0], "-test.run=TestOwnedChildHelperProcess"})
	if err != nil {
		t.Fatal(err)
	}
	// startChild inherits the environment; set the helper marker through the
	// current test process so only this subprocess takes the long-running path.
	stopChild(child, done)
	if child.ProcessState == nil || child.ProcessState.Success() {
		t.Fatal("owned child was not reaped after wrapper shutdown")
	}
}
