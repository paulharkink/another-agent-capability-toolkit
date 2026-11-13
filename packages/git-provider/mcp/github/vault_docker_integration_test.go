package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

// This opt-in integration uses a disposable synthetic gateway and local fake provider API.
// It never contacts a real provider or agent config and removes its test container/image.
func TestDockerVaultBackedGitHubGatewayIntegration(t *testing.T) {
	if os.Getenv("AACT_TEST_DOCKER") != "1" {
		t.Skip("set AACT_TEST_DOCKER=1 to run the disposable Docker gateway fixture")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Docker gateway fixture requires a Unix host")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	architectureOutput, err := exec.Command("docker", "info", "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatalf("docker daemon unavailable: %v", err)
	}
	goarch := map[string]string{"aarch64": "arm64", "x86_64": "amd64"}[strings.TrimSpace(string(architectureOutput))]
	if goarch == "" {
		t.Skipf("unsupported Docker architecture %q", architectureOutput)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certificateTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "host.docker.internal"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"host.docker.internal"}}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{certificateDER}, PrivateKey: privateKey}
	apiListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	apiPort := apiListener.Addr().(*net.TCPAddr).Port
	vaultStore, err := vault.Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("integration-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	api := vault.NewAPI(vaultStore, nil)
	apiServer := &http.Server{Handler: api, TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}}
	go func() { _ = apiServer.ServeTLS(apiListener, "", "") }()
	t.Cleanup(func() { _ = apiServer.Close() })
	apiCertificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}))
	database, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetSecretBackend(vaultStore)
	parentKey := state.Key{Source: "fixture-pack", Package: "git-provider", Target: "docker-gateway-integration"}
	const tokenValue = "synthetic-provider-token-not-for-a-real-account"
	if err := database.SaveSecretInputs(parentKey, map[string]any{"github_token": tokenValue}, nil); err != nil {
		t.Fatal(err)
	}
	secretRevision, err := database.SecretInputsRevision(parentKey)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &dockerFixtureVaultIssuer{api: api, info: vault.DaemonInfo{APIAddress: fmt.Sprintf("https://host.docker.internal:%d", apiPort), APICertificate: apiCertificate}}

	providerListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	providerPort := providerListener.Addr().(*net.TCPAddr).Port
	providerAuth := make(chan string, 4)
	providerServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerAuth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}), TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}}
	go func() { _ = providerServer.ServeTLS(providerListener, "", "") }()
	t.Cleanup(func() { _ = providerServer.Close() })

	contextDir := t.TempDir()
	gatewayBinary := filepath.Join(contextDir, "git-mcp-wrapper")
	helperBinary := filepath.Join(contextDir, "aact-vault-handoff")
	build := func(output, source string) {
		t.Helper()
		command := exec.Command("go", "build", "-trimpath", "-o", output, source)
		command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build fixture binary: %v: %s", err, output)
		}
	}
	build(gatewayBinary, ".")
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	build(helperBinary, filepath.Join(repoRoot, "cmd", "aact-vault-handoff"))
	fakeMCP := `package main
import ("encoding/json"; "net/http")
func main(){ http.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){ if r.Method==http.MethodHead { w.WriteHeader(200); return }; w.Header().Set("Content-Type","application/json"); _=json.NewEncoder(w).Encode(map[string]any{"jsonrpc":"2.0","id":1,"result":map[string]any{"protocolVersion":"2024-11-05"},"seen_authorization":r.Header.Get("Authorization")}) }); _=http.ListenAndServe("127.0.0.1:8082",nil) }`
	fakeSource := filepath.Join(contextDir, "fake_mcp.go")
	if err := os.WriteFile(fakeSource, []byte(fakeMCP), 0600); err != nil {
		t.Fatal(err)
	}
	fakeBinary := filepath.Join(contextDir, "fake-mcp")
	build(fakeBinary, fakeSource)
	if err := os.WriteFile(filepath.Join(contextDir, "vault-ca.crt"), []byte(apiCertificate), 0600); err != nil {
		t.Fatal(err)
	}
	dockerfile := fmt.Sprintf(`FROM --platform=linux/%s alpine:3.22
RUN apk add --no-cache ca-certificates
COPY git-mcp-wrapper /usr/local/bin/git-mcp-wrapper
COPY fake-mcp /usr/local/bin/fake-mcp
COPY aact-vault-handoff /usr/local/bin/aact-vault-handoff
COPY vault-ca.crt /usr/local/share/ca-certificates/fixture-vault.crt
RUN update-ca-certificates && chmod 0755 /usr/local/bin/git-mcp-wrapper /usr/local/bin/fake-mcp /usr/local/bin/aact-vault-handoff
ENV MCP_HEALTH_PROVIDER=github MCP_HEALTH_CHILD=/usr/local/bin/fake-mcp MCP_HEALTH_UPSTREAM=http://127.0.0.1:8082 MCP_HEALTH_LISTEN=0.0.0.0:8084
ENTRYPOINT ["/usr/local/bin/git-mcp-wrapper"]
`, goarch)
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	image := fmt.Sprintf("aact-gateway-vault-test:%d", time.Now().UnixNano())
	imageBuild := exec.Command("docker", "build", "--platform", "linux/"+goarch, "-t", image, contextDir)
	if out, err := imageBuild.CombinedOutput(); err != nil {
		t.Fatalf("build synthetic gateway image: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "--force", image).Run() })

	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostPort := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	key := parentKey
	key.MCP = "github"
	runtimeManager := mcp.NewDockerRuntime(database)
	runtimeManager.VaultTokenIssuer = issuer
	runtimeManager.VaultHandoffHelperPath = helperBinary
	spec := mcp.RunSpec{Image: image, Host: "127.0.0.1", HostPort: hostPort, ContainerPort: 8084, Transport: "streamable-http", EndpointPath: "/mcp", Env: map[string]string{"GITHUB_HOST": fmt.Sprintf("https://host.docker.internal:%d", providerPort), "AACT_VAULT_SECRET_INPUT_GITHUB_PERSONAL_ACCESS_TOKEN": "github_token", "AACT_VAULT_AUTH_ENV": "GITHUB_PERSONAL_ACCESS_TOKEN"}, VaultSecretInputs: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "github_token"}, VaultNamespaceID: parentKey.ID(), VaultSecretRevision: secretRevision}
	instance, err := runtimeManager.Start(context.Background(), key, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeManager.Stop(context.Background(), key) })
	healthURL, err := url.Parse(instance.URL)
	if err != nil {
		t.Fatal(err)
	}
	healthURL.Path = "/health"
	healthURL.RawQuery = ""
	response, err := http.Get(healthURL.String())
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Authenticated bool `json:"authenticated"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&health)
	_ = response.Body.Close()
	var healthAuthorization string
	select {
	case healthAuthorization = <-providerAuth:
	default:
	}
	if decodeErr != nil || response.StatusCode != http.StatusOK || !health.Authenticated {
		logs, _ := exec.Command("docker", "logs", instance.ID).CombinedOutput()
		diagnostic, _ := exec.Command("docker", "exec", instance.ID, "sh", "-c", fmt.Sprintf("printenv GITHUB_HOST AACT_VAULT_AUTH_ENV MCP_HEALTH_PROVIDER; busybox nslookup host.docker.internal; busybox wget -S -O /dev/null https://host.docker.internal:%d/api/v3/user", providerPort)).CombinedOutput()
		t.Logf("container provider connectivity diagnostic: %s", diagnostic)
		t.Logf("provider probe Authorization observed: %q", healthAuthorization)

		t.Fatalf("gateway health = %d %+v err=%v logs=%s", response.StatusCode, health, decodeErr, logs)
	}
	if healthAuthorization != "Bearer "+tokenValue {
		t.Fatalf("provider health Authorization=%q", healthAuthorization)
	}
	request, err := http.NewRequest(http.MethodPost, instance.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer agent-config-must-not-win")
	forwarded, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Seen string `json:"seen_authorization"`
	}
	decodeErr = json.NewDecoder(forwarded.Body).Decode(&message)
	_ = forwarded.Body.Close()
	if decodeErr != nil || message.Seen != "Bearer "+tokenValue {
		t.Fatalf("gateway MCP forwarded Authorization=%q err=%v", message.Seen, decodeErr)
	}
	inspect, err := exec.Command("docker", "inspect", instance.ID).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inspect), tokenValue) {
		t.Fatal("provider credential appeared in Docker inspect")
	}

	const rotatedToken = "synthetic-provider-token-rotated"
	if err := database.SaveSecretInputs(parentKey, map[string]any{"github_token": rotatedToken}, nil); err != nil {
		t.Fatal(err)
	}
	rotatedRevision, err := database.SecretInputsRevision(parentKey)
	if err != nil || rotatedRevision == secretRevision {
		t.Fatalf("rotated revision=%q previous=%q err=%v", rotatedRevision, secretRevision, err)
	}
	rotatedSpec := spec
	rotatedSpec.VaultSecretRevision = rotatedRevision
	if _, err := runtimeManager.Start(context.Background(), key, rotatedSpec); !errors.Is(err, mcp.ErrRunningWithDifferentSettings) {
		t.Fatalf("start with rotated secret revision error=%v", err)
	}
	if err := runtimeManager.Stop(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	rotatedInstance, err := runtimeManager.Start(context.Background(), key, rotatedSpec)
	if err != nil {
		t.Fatal(err)
	}
	healthURL, err = url.Parse(rotatedInstance.URL)
	if err != nil {
		t.Fatal(err)
	}
	healthURL.Path = "/health"
	response, err = http.Get(healthURL.String())
	if err != nil {
		t.Fatal(err)
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&health)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || !health.Authenticated {
		t.Fatalf("rotated gateway health = %d %+v err=%v", response.StatusCode, health, decodeErr)
	}
	select {
	case auth := <-providerAuth:
		if auth != "Bearer "+rotatedToken {
			t.Fatalf("rotated provider health Authorization=%q", auth)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rotated provider token was not used by health probe")
	}
	request, err = http.NewRequest(http.MethodPost, rotatedInstance.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer stale-agent-config")
	forwarded, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	message = struct {
		Seen string `json:"seen_authorization"`
	}{}
	decodeErr = json.NewDecoder(forwarded.Body).Decode(&message)
	_ = forwarded.Body.Close()
	if decodeErr != nil || message.Seen != "Bearer "+rotatedToken {
		t.Fatalf("rotated gateway forwarded Authorization=%q err=%v", message.Seen, decodeErr)
	}
	rotatedInspect, err := exec.Command("docker", "inspect", rotatedInstance.ID).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rotatedInspect), rotatedToken) || strings.Contains(string(rotatedInspect), tokenValue) {
		t.Fatal("rotated provider credential appeared in Docker inspect")
	}
}

type dockerFixtureVaultIssuer struct {
	api  *vault.API
	info vault.DaemonInfo
}

func (i *dockerFixtureVaultIssuer) IssueContainerHandoff(namespace string) (vault.ContainerHandoff, func() error, error) {
	token, err := i.api.IssueToken(namespace)
	if err != nil {
		return vault.ContainerHandoff{}, nil, err
	}
	handoff, err := vault.NewContainerHandoff(i.info, token)
	if err != nil {
		_ = i.api.RevokeToken(token)
		return vault.ContainerHandoff{}, nil, err
	}
	return handoff, func() error { return i.api.RevokeToken(token) }, nil
}
func (i *dockerFixtureVaultIssuer) BindContainerHandoff(containerID, token string) error {
	return i.api.BindTokenContainer(token, containerID)
}
func (i *dockerFixtureVaultIssuer) RevokeContainerHandoff(namespace, containerID string) error {
	return i.api.RevokeTokensForContainer(namespace, containerID)
}
func (i *dockerFixtureVaultIssuer) RevokeContainerHandoffs(namespace string) error {
	return i.api.RevokeTokensForInstance(namespace)
}
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root not found")
		}
		dir = parent
	}
}
