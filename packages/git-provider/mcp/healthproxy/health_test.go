package healthproxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestGitHubInstallationTokenUsesRepositoryMembershipProbe(t *testing.T) {
	for _, body := range []string{
		`{"total_count":0,"repositories":[]}`,
		`{"total_count":1,"repositories":[{"id":42}]}`,
		`{"total_count":2,"repositories":[{"id":42,"name":"repo","owner":{"login":"org"},"extra":true}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			var gotMethod, gotPath, gotAuthorization string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.RequestURI()
				gotAuthorization = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer provider.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer upstream.Close()

			handler := New(Config{Provider: "github", UpstreamURL: upstream.URL, APIBaseURL: provider.URL})
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			request.Header.Set("Authorization", "Bearer ghs_installation-token")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), true) {
				t.Fatalf("health = %d %q, want authenticated=true", recorder.Code, recorder.Body.String())
			}
			if gotMethod != http.MethodGet || gotPath != "/installation/repositories?per_page=1" {
				t.Fatalf("probe = %s %q, want GET /installation/repositories?per_page=1", gotMethod, gotPath)
			}
			if gotAuthorization != "Bearer ghs_installation-token" {
				t.Fatalf("probe Authorization = %q, want selected installation bearer", gotAuthorization)
			}
		})
	}
}

func TestGitHubInstallationTokenUsesResolvedEnterpriseAPIBase(t *testing.T) {
	var gotPath string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = io.WriteString(w, `{"total_count":0,"repositories":[]}`)
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()

	handler := New(Config{
		Provider: "github", UpstreamURL: upstream.URL,
		GitHubHost: provider.URL + "/ignored/configured/path",
	})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer ghs_installation-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), true) {
		t.Fatalf("health = %d %q, want authenticated=true", recorder.Code, recorder.Body.String())
	}
	if gotPath != "/api/v3/installation/repositories?per_page=1" {
		t.Fatalf("probe path = %q, want resolved enterprise API path", gotPath)
	}
}

func TestGitHubInstallationTokenRejectsInvalidAndMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`},
		{name: "server error", status: http.StatusInternalServerError, body: `{"total_count":0,"repositories":[]}`},
		{name: "missing total count", status: http.StatusOK, body: `{"repositories":[]}`},
		{name: "negative total count", status: http.StatusOK, body: `{"total_count":-1,"repositories":[]}`},
		{name: "fractional total count", status: http.StatusOK, body: `{"total_count":1.5,"repositories":[]}`},
		{name: "null total count", status: http.StatusOK, body: `{"total_count":null,"repositories":[]}`},
		{name: "boolean total count", status: http.StatusOK, body: `{"total_count":true,"repositories":[]}`},
		{name: "string total count", status: http.StatusOK, body: `{"total_count":"1","repositories":[]}`},
		{name: "count smaller than returned repositories", status: http.StatusOK, body: `{"total_count":0,"repositories":[{"id":1}]}`},
		{name: "missing repositories", status: http.StatusOK, body: `{"total_count":0}`},
		{name: "null repositories", status: http.StatusOK, body: `{"total_count":0,"repositories":null}`},
		{name: "wrong repositories type", status: http.StatusOK, body: `{"total_count":0,"repositories":{}}`},
		{name: "null repository", status: http.StatusOK, body: `{"total_count":1,"repositories":[null]}`},
		{name: "string repository", status: http.StatusOK, body: `{"total_count":1,"repositories":["repo"]}`},
		{name: "array repository", status: http.StatusOK, body: `{"total_count":1,"repositories":[[]]}`},
		{name: "missing repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{}]}`},
		{name: "null repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{"id":null}]}`},
		{name: "boolean repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{"id":true}]}`},
		{name: "zero repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{"id":0}]}`},
		{name: "negative repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{"id":-1}]}`},
		{name: "fractional repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{"id":1.5}]}`},
		{name: "string repository id", status: http.StatusOK, body: `{"total_count":1,"repositories":[{"id":"1"}]}`},
		{name: "message only", status: http.StatusOK, body: `{"message":"Not Found"}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"total_count":0,"repositories":[]`},
		{name: "non-object JSON", status: http.StatusOK, body: `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.RequestURI(); got != "/installation/repositories?per_page=1" {
					t.Errorf("provider path = %q", got)
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer provider.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer upstream.Close()

			handler := New(Config{Provider: "github", UpstreamURL: upstream.URL, APIBaseURL: provider.URL})
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			request.Header.Set("Authorization", "Bearer ghs_installation-token")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
				t.Fatalf("health = %d %q, want authenticated=false", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGitHubInstallationTokenMissingDoesNotProbeProvider(t *testing.T) {
	var providerRequests int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests++
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"total_count":0,"repositories":[]}`)
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()

	handler := New(Config{Provider: "github", UpstreamURL: upstream.URL, APIBaseURL: provider.URL})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
		t.Fatalf("health = %d %q, want authenticated=false", recorder.Code, recorder.Body.String())
	}
	if providerRequests != 0 {
		t.Fatalf("provider received %d probe(s) without a token", providerRequests)
	}
}

func TestGitHubInstallationTokenRedirectDoesNotForwardBearer(t *testing.T) {
	var redirectedAuthorization string
	var redirectRequests int
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectRequests++
		redirectedAuthorization = r.Header.Get("Authorization")
	}))
	defer redirectTarget.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghs_installation-token" {
			t.Errorf("source probe Authorization = %q", got)
		}
		http.Redirect(w, r, redirectTarget.URL+"/redirected", http.StatusFound)
	}))
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()

	handler := New(Config{Provider: "github", UpstreamURL: upstream.URL, APIBaseURL: provider.URL})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer ghs_installation-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
		t.Fatalf("health = %d %q, want authenticated=false", recorder.Code, recorder.Body.String())
	}
	if redirectedAuthorization != "" {
		t.Fatalf("cross-origin redirect received Authorization %q", redirectedAuthorization)
	}
	if redirectRequests != 0 {
		t.Fatalf("cross-origin redirect target received %d request(s)", redirectRequests)
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
		{input: "http://localhost:8443/ignored/base", want: "http://localhost:8443/api/v3/"},
		{input: "http://LOCALHOST:8443/ignored/base", want: "http://LOCALHOST:8443/api/v3/"},
		{input: "http://127.0.0.1:8443/ignored/base", want: "http://127.0.0.1:8443/api/v3/"},
		{input: "http://[::1]:8443/ignored/base", want: "http://[::1]:8443/api/v3/"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := GitHubAPIBase(test.input)
			if err != nil || got.String() != test.want {
				t.Fatalf("GitHub API base = %v, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestGitHubHTTPRequiresExactPinnedLoopbackHosts(t *testing.T) {
	for _, host := range []string{
		"127.0.0.2",
		"127.1",
		"127.0.1.1",
		"0:0:0:0:0:0:0:1",
	} {
		t.Run(host, func(t *testing.T) {
			got, err := GitHubAPIBase("http://" + net.JoinHostPort(host, "8443") + "/ignored/base")
			if err == nil || got != nil {
				t.Fatalf("GitHub API base = %v, %v; want unsupported host", got, err)
			}
		})
	}
}

func TestGitHubHTTPRejectsLoopbackAliasBeforeSendingCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("cannot bind exact loopback alias for no-probe assertion: %v", err)
	}
	var providerRequests int
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerRequests++
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("rejected GitHub host received credentials: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	provider.Listener = listener
	provider.Start()
	defer provider.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler := New(Config{
		Provider: "github", UpstreamURL: upstream.URL,
		GitHubHost: "http://" + listener.Addr().String() + "/ignored/base",
	})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer selected-secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !healthValue(recorder.Body.String(), false) {
		t.Fatalf("health = %d %q, want unauthenticated", recorder.Code, recorder.Body.String())
	}
	if providerRequests != 0 {
		t.Fatalf("rejected GitHub host received %d credential probe(s)", providerRequests)
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
