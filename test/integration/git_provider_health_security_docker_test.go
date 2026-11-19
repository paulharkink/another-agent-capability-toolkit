//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

// This exercises the shipped gateway executables through their HTTP boundary.
// All provider and Vault endpoints are synthetic and all Docker resources are
// removed by the test.
func TestDockerGitProviderHealthSecurityBoundary(t *testing.T) {
	if os.Getenv("AACT_TEST_DOCKER") != "1" {
		t.Skip("set AACT_TEST_DOCKER=1 to run isolated provider gateway containers")
	}
	if runtime.GOOS == "windows" {
		t.Skip("provider gateway fixture requires a Unix host")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	architecture, err := exec.Command("docker", "info", "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatalf("Docker daemon unavailable: %v", err)
	}
	goarch := map[string]string{"aarch64": "arm64", "x86_64": "amd64"}[strings.TrimSpace(string(architecture))]
	if goarch == "" {
		t.Skipf("unsupported Docker architecture %q", architecture)
	}

	cert, certPEM := providerFixtureCertificate(t)
	apiListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	apiPort := apiListener.Addr().(*net.TCPAddr).Port
	vaultStore, err := vault.Open(filepath.Join(t.TempDir(), "synthetic-vault.kdbx"), []byte("synthetic-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := vault.NewAPI(vaultStore, nil)
	apiServer := &http.Server{Handler: api, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	go func() { _ = apiServer.ServeTLS(apiListener, "", "") }()
	t.Cleanup(func() { _ = apiServer.Close() })
	database, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetSecretBackend(vaultStore)
	issuer := &dockerFixtureVaultIssuer{api: api, info: vault.DaemonInfo{APIAddress: fmt.Sprintf("https://host.docker.internal:%d", apiPort), APICertificate: certPEM}}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	contextDir := t.TempDir()
	build := func(output, source string) {
		t.Helper()
		command := exec.Command("go", "build", "-trimpath", "-o", output, source)
		command.Dir = repoRoot
		command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
		if result, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build synthetic gateway binary: %v: %s", err, result)
		}
	}
	for _, provider := range []string{"github", "gitlab", "bitbucket"} {
		build(filepath.Join(contextDir, "gateway-"+provider), "./main/packages/git-provider/mcp/"+provider)
	}
	fakeChildSource := `package main
import ("encoding/json"; "net"; "net/http")
func main() {
  mcp := http.NewServeMux()
  mcp.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK); if r.Method != http.MethodHead { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc":"2.0","id":1,"result":map[string]any{"protocolVersion":"2024-11-05"}}) } })
  go func(){ _ = http.ListenAndServe("127.0.0.1:8082", mcp) }()
  provider := http.NewServeMux()
  provider.HandleFunc("/__fixture__/ready", func(w http.ResponseWriter, _ *http.Request){ w.WriteHeader(http.StatusOK) })
  provider.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request){ w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(map[string]any{"id":1}) })
  ln, err := net.Listen("tcp", "0.0.0.0:8085")
  if err == nil { go func(){ _ = http.Serve(ln, provider) }() }
  select {}
}`
	fakeSource := filepath.Join(contextDir, "fake_child.go")
	if err := os.WriteFile(fakeSource, []byte(fakeChildSource), 0600); err != nil {
		t.Fatal(err)
	}
	build(filepath.Join(contextDir, "fake-child"), fakeSource)
	helperBinary := filepath.Join(contextDir, "aact-vault-handoff")
	build(helperBinary, "./main/cmd/aact-vault-handoff")
	if err := os.WriteFile(filepath.Join(contextDir, "vault-ca.crt"), []byte(certPEM), 0600); err != nil {
		t.Fatal(err)
	}
	dockerfile := fmt.Sprintf(`FROM --platform=linux/%s alpine:3.22
RUN apk add --no-cache ca-certificates
COPY gateway-github /usr/local/bin/gateway-github
COPY gateway-gitlab /usr/local/bin/gateway-gitlab
COPY gateway-bitbucket /usr/local/bin/gateway-bitbucket
COPY fake-child /usr/local/bin/fake-child
COPY vault-ca.crt /usr/local/share/ca-certificates/fixture-vault.crt
RUN update-ca-certificates && chmod 0755 /usr/local/bin/gateway-github /usr/local/bin/gateway-gitlab /usr/local/bin/gateway-bitbucket /usr/local/bin/fake-child
`, goarch)
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	image := fmt.Sprintf("aact-git-health-security:%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "--force", image).Run() })
	if output, err := exec.Command("docker", "build", "--platform", "linux/"+goarch, "-t", image, contextDir).CombinedOutput(); err != nil {
		t.Fatalf("build isolated provider image: %v: %s", err, output)
	}

	for _, provider := range []struct {
		name, authEnv, inputName, inputTarget, urlEnv string
		urlSuffix                                     string
	}{
		{name: "github", authEnv: "GITHUB_PERSONAL_ACCESS_TOKEN", inputName: "github_token", inputTarget: "GITHUB_PERSONAL_ACCESS_TOKEN", urlEnv: "GITHUB_HOST", urlSuffix: "/api/v3/user"},
		{name: "gitlab", authEnv: "GITLAB_TOKEN", inputName: "gitlab_token", inputTarget: "GITLAB_TOKEN", urlEnv: "GITLAB_API_URL", urlSuffix: "/api/v4/user"},
		{name: "bitbucket", authEnv: "BITBUCKET_TOKEN", inputName: "bitbucket_token", inputTarget: "BITBUCKET_TOKEN", urlEnv: "BITBUCKET_URL", urlSuffix: "/rest/api/latest/dashboard/pull-requests?limit=1"},
	} {
		t.Run(provider.name, func(t *testing.T) {
			providerSecurityScenarios(t, database, issuer, cert, image, helperBinary, provider.name, provider.authEnv, provider.inputName, provider.inputTarget, provider.urlEnv, provider.urlSuffix)
		})
	}
}

func providerSecurityScenarios(t *testing.T, database *state.Store, issuer *dockerFixtureVaultIssuer, certificate tls.Certificate, image, helperBinary, provider, authEnv, inputName, inputTarget, urlEnv, probePath string) {
	t.Helper()
	const token = "synthetic-git-provider-token"
	var mode atomic.Int32
	const modeRedirect = int32(1)
	redirectCredential := make(chan string, 8)
	providerListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	providerServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/credential-receiver" {
			auth := r.Header.Get("Authorization")
			select {
			case redirectCredential <- auth:
			default:
			}
			if auth == "Bearer "+token {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusUnauthorized)
			}
			return
		}
		if r.URL.RequestURI() != probePath {
			http.NotFound(w, r)
			return
		}
		if mode.Load() == modeRedirect {
			http.Redirect(w, r, strings.TrimRight(providerServerURL(r), "/")+"/credential-receiver", http.StatusFound)
			return
		}
		if r.Header.Get("X-Private-Registration-Header") != "" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	providerServer.Listener = providerListener
	providerServer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	providerServer.StartTLS()
	defer providerServer.Close()
	// The redirect remains on the same origin so that only the gateway's
	// redirect policy determines whether the provider credential is propagated.
	providerEndpoint := fmt.Sprintf("https://host.docker.internal:%d", providerListener.Addr().(*net.TCPAddr).Port)

	validKey := state.Key{Source: "fixture", Package: "git-provider", Target: "security-" + provider, MCP: provider}
	if err := database.SaveSecretInputs(validKey, map[string]any{inputName: token}, nil); err != nil {
		t.Fatal(err)
	}
	validRevision, err := database.SecretInputsRevision(validKey)
	if err != nil {
		t.Fatal(err)
	}
	validInstance := startProviderGateway(t, database, issuer, image, helperBinary, validKey, provider, authEnv, inputName, inputTarget, urlEnv, providerEndpoint, validRevision)

	healthURL := "http://127.0.0.1:8084/health"
	if err := waitProviderContainerEndpoint(validInstance.ID, healthURL, 5*time.Second); err != nil {
		t.Fatalf("gateway HTTP health endpoint: %v", err)
	}
	healthBody := providerContainerGet(t, validInstance.ID, healthURL, "Authorization: Bearer agent-header-must-not-be-forwarded", "X-Private-Registration-Header: synthetic-private-value")
	var health struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(healthBody, &health); err != nil || !health.Authenticated {
		t.Fatalf("synthetic provider health with arbitrary request header = %s err=%v", healthBody, err)
	}

	mode.Store(modeRedirect)
	healthBody = providerContainerGet(t, validInstance.ID, healthURL)
	if err := json.Unmarshal(healthBody, &health); err != nil || health.Authenticated {
		t.Fatalf("redirected provider health = %s err=%v; want unauthenticated", healthBody, err)
	}
	for {
		select {
		case received := <-redirectCredential:
			if received == "Bearer "+token {
				t.Fatal("provider credential reached the redirect target")
			}
		default:
			goto loopback
		}
	}

loopback:
	if provider != "github" {
		return
	}
	// A reachable fake API on a non-exact loopback address would authenticate
	// successfully if the gateway sent it a credential. The public health
	// result must remain false for this unsafe HTTP endpoint.
	unsafeKey := state.Key{Source: "fixture", Package: "git-provider", Target: "unsafe-loopback-" + provider, MCP: provider}
	if err := database.SaveSecretInputs(unsafeKey, map[string]any{inputName: token}, nil); err != nil {
		t.Fatal(err)
	}
	unsafeRevision, err := database.SecretInputsRevision(unsafeKey)
	if err != nil {
		t.Fatal(err)
	}
	unsafeInstance := startProviderGateway(t, database, issuer, image, helperBinary, unsafeKey, provider, authEnv, inputName, inputTarget, urlEnv, "http://127.0.0.2:8085", unsafeRevision)
	if err := waitProviderContainerEndpoint(unsafeInstance.ID, "http://127.0.0.1:8085/__fixture__/ready", 5*time.Second); err != nil {
		t.Fatalf("reachable synthetic provider API: %v", err)
	}
	if err := waitProviderContainerEndpoint(unsafeInstance.ID, healthURL, 5*time.Second); err != nil {
		t.Fatalf("gateway HTTP health endpoint: %v", err)
	}
	healthBody = providerContainerGet(t, unsafeInstance.ID, healthURL)
	if err := json.Unmarshal(healthBody, &health); err != nil || health.Authenticated {
		t.Fatalf("non-exact HTTP loopback health = %s err=%v; want unauthenticated", healthBody, err)
	}
}

func startProviderGateway(t *testing.T, database *state.Store, issuer *dockerFixtureVaultIssuer, image, helperBinary string, key state.Key, provider, authEnv, inputName, inputTarget, urlEnv, providerURL, revision string) mcp.Instance {
	t.Helper()
	mainPort := reserveProviderHostPort(t)
	env := map[string]string{
		"MCP_HEALTH_PROVIDER":                    provider,
		"MCP_HEALTH_UPSTREAM":                    "http://127.0.0.1:8082",
		"MCP_HEALTH_LISTEN":                      "0.0.0.0:8084",
		"AACT_VAULT_AUTH_ENV":                    authEnv,
		"AACT_VAULT_SECRET_INPUT_" + inputTarget: inputName,
		urlEnv:                                   providerURL,
	}
	runtimeManager := mcp.NewDockerRuntime(database)
	runtimeManager.VaultTokenIssuer = issuer
	runtimeManager.VaultHandoffHelperPath = helperBinary
	spec := mcp.RunSpec{
		Image: image,
		Args:  []string{"/usr/local/bin/gateway-" + provider, "/usr/local/bin/fake-child"},
		Host:  "127.0.0.1", HostPort: mainPort, ContainerPort: 8084,
		Transport: "streamable-http", EndpointPath: "/mcp", Env: env,
		VaultSecretInputs: map[string]string{inputTarget: inputName}, VaultNamespaceID: key.ID(), VaultSecretRevision: revision,
		DeferProtocolHealth: true,
	}
	instance, err := runtimeManager.Start(context.Background(), key, spec)
	if err != nil {
		t.Fatalf("start isolated %s gateway: %v", provider, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := runtimeManager.Stop(ctx, key); err != nil {
			t.Errorf("stop isolated %s gateway: %v", provider, err)
		}
	})
	return instance
}

func reserveProviderHostPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func waitProviderContainerEndpoint(containerID, endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := providerContainerGetCommand(containerID, endpoint); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("synthetic API in container %s did not become ready within %s", containerID, timeout)
}

func providerContainerGet(t *testing.T, containerID, endpoint string, headers ...string) []byte {
	t.Helper()
	body, err := providerContainerGetCommand(containerID, endpoint, headers...)
	if err != nil {
		t.Fatalf("GET gateway HTTP endpoint %s: %v", endpoint, err)
	}
	return body
}

func providerContainerGetCommand(containerID, endpoint string, headers ...string) ([]byte, error) {
	args := []string{"exec", containerID, "busybox", "wget", "-q", "-O", "-"}
	for _, header := range headers {
		args = append(args, "--header="+header)
	}
	args = append(args, endpoint)
	output, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("docker exec HTTP request failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func providerFixtureCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "host.docker.internal"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
		IsCA: true, DNSNames: []string{"host.docker.internal"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func providerServerURL(request *http.Request) string {
	return "https://" + request.Host
}
