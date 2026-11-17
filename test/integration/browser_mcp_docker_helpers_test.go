package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
	vault "github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

// Optional external capability smoke test. Every input is synthetic and supplied
// through the test environment so the toolkit remains independent of any site.
func exerciseSyntheticMCP(t *testing.T, docker func([]byte, ...string) []byte, info vault.DaemonInfo, control []byte, volume string) {
	t.Helper()
	image, origin, envName := os.Getenv("AACT_BROWSER_DEMO_MCP_IMAGE"), os.Getenv("AACT_BROWSER_DEMO_ORIGIN"), os.Getenv("AACT_BROWSER_DEMO_UPSTREAM_ENV")
	adapter, err := os.ReadFile(os.Getenv("AACT_BROWSER_DEMO_ADAPTER_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(os.Getenv("AACT_BROWSER_DEMO_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	expected = bytes.TrimSpace(expected)
	defer wipeTestBytes(expected)
	if !regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(envName) || origin == "" {
		t.Fatal("synthetic MCP test configuration is invalid")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "host.docker.internal"}, DNSNames: []string{"host.docker.internal"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, ca, 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+string(expected) {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":["synthetic-bucket"]}`))
	}))
	_ = server.Listener.Close()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	upstream := "https://host.docker.internal:" + strings.Split(listener.Addr().String(), ":")[len(strings.Split(listener.Addr().String(), ":"))-1]
	container := string(docker(nil, "run", "-d", "--tmpfs", "/run/aact:rw,noexec,nosuid,nodev,size=16k,mode=1777", "--mount", "type=bind,source="+caPath+",target=/tmp/test-ca.pem,readonly", "--env", "REQUESTS_CA_BUNDLE=/tmp/test-ca.pem", "--env", "NO_PROXY=host.docker.internal", "--env", envName+"="+upstream, image))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", container).Run() })
	namespace := strings.Repeat("d", 64)
	token, err := vault.IssueContainerPrivateInputToken(info, control, namespace, []string{vault.BrowserOnlyPrivateInputScope})
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.BindContainerToken(info, control, container, token); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: origin, StartupURL: origin, ProfileVolume: volume, ContainerName: "aact-test-mcp-browser-" + container[:16], Adapter: browsercontroller.BrowserAuthAdapter{Source: string(adapter)}}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations", browserRegistration{Instance: namespace, ContainerID: container, Config: config}, nil); err != nil {
		t.Fatal(err)
	}
	handoff, err := vault.NewContainerHandoff(info, token)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(handoff)
	install := `import os,sys; p='/run/aact/vault.json'; fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o400); os.write(fd,sys.stdin.buffer.read()); os.close(fd)`
	docker(payload, "exec", "-i", container, "python3", "-c", install)
	call := `import asyncio,time; from fastmcp import Client
async def run():
 for attempt in range(60):
  try:
   async with Client('http://127.0.0.1:8765/mcp') as c:
    a=await c.call_tool('list_buckets',{}); b=await c.call_tool('send_message',{'request':{'message':'synthetic'}}); assert 'synthetic-bucket' in str(a) and 'synthetic-bucket' in str(b); print('both synthetic MCP tools passed'); return
  except OSError:
   await asyncio.sleep(.5)
 raise RuntimeError('MCP did not start')
asyncio.run(run())`
	out := docker(nil, "exec", container, "python3", "-c", call)
	if !strings.Contains(string(out), "both synthetic MCP tools passed") {
		t.Fatalf("MCP integration failed: %s", out)
	}
}
