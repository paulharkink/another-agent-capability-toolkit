package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
	vault "github.com/paulharkink/another-agent-capability-toolkit/main/internal/vault"
)

// This opt-in test uses only new Docker resources and an isolated vault.
func TestBrowserDaemonCredentialDockerFlow(t *testing.T) {
	if os.Getenv("AACT_RUN_BROWSER_DOCKER_TEST") != "1" {
		t.Skip("set AACT_RUN_BROWSER_DOCKER_TEST=1 for the isolated Docker test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("detached test helper uses a POSIX shell")
	}
	image := "aact/browser-authentication-helper:1.18.0"
	docker := func(input []byte, args ...string) []byte {
		t.Helper()
		c := exec.Command("docker", args...)
		c.Stdin = bytes.NewReader(input)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s failed: %v: %s", args[0], err, out)
		}
		return bytes.TrimSpace(out)
	}
	docker(nil, "image", "inspect", image)
	volume := string(docker(nil, "volume", "create", "--label", "aact.test=browser-daemon"))
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", volume).Run() })
	fixture := `import pathlib,sqlite3,os; p=pathlib.Path('/profile/.config/chromium/Default/Network'); p.mkdir(parents=True); c=sqlite3.connect(str(p/'Cookies')); c.execute('CREATE TABLE cookies(host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER, is_secure INTEGER)'); c.execute('INSERT INTO cookies VALUES(?,?,?,?,?,?)',('example.invalid','demo_session','synthetic-demo-cookie',b'',0,0)); c.commit(); c.close(); os.chmod(p/'Cookies',0o644); [(os.chown(root,1000,1000),[os.chown(os.path.join(root,n),1000,1000) for n in files]) for root,dirs,files in os.walk('/profile')]`
	docker(nil, "run", "--rm", "--user", "0", "--network", "none", "--entrypoint", "python3", "--mount", "source="+volume+",target=/profile", image, "-c", fixture)
	container := string(docker(nil, "run", "-d", "--entrypoint", "sleep", image, "300"))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", container).Run() })
	root := t.TempDir()
	password := []byte("synthetic-only-password")
	if _, err := vault.Open(filepath.Join(root, "credentials.kdbx"), password); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "0.0.0.0")
	control, err := vault.ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "aact-test-daemon")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nAACT_TEST_DAEMON_CHILD=1 exec "+quoted+" -test.run=^TestVaultDaemonChildHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := vault.StartDetached(script, root, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.StopDetached(info, control); wipeTestBytes(control) })
	info, err = vault.EnsureContainerAPIReachable(info, control)
	if err != nil {
		t.Fatal(err)
	}
	namespace := strings.Repeat("a", 64)
	token, err := vault.IssueContainerPrivateInputToken(info, control, namespace, []string{vault.BrowserOnlyPrivateInputScope})
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.BindContainerToken(info, control, container, token); err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{Origin: "https://example.invalid", StartupURL: "https://example.invalid", ProfileVolume: volume, ContainerName: "aact-test-browser-" + container[:16], Adapter: browsercontroller.BrowserAuthAdapter{Source: "def run(credentials, config):\n    return {'status': 'refreshed', 'synthetic_value': credentials[0]['value']}\n"}}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations", browserRegistration{Instance: namespace, ContainerID: container, Config: config}, nil); err != nil {
		t.Fatal(err)
	}
	handoff, err := vault.NewContainerHandoff(info, token)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(handoff)
	client := `import sys,json,ssl,urllib.request; h=json.load(sys.stdin); c=ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); c.load_verify_locations(cadata=h['certificate']); p=json.dumps({'selector':{'kind':'cookie','match':'exact','names':['demo_session']}}).encode(); r=urllib.request.Request(h['api_address']+'/v1/browser/credentials',data=p,headers={'Authorization':'Bearer '+h['token'],'Content-Type':'application/json'}); o=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=c)); d=json.load(o.open(r,timeout=90)); assert d['result']['synthetic_value']=='synthetic-demo-cookie'; print('synthetic credential read through daemon')`
	output := docker(payload, "exec", "-i", container, "python3", "-c", client)
	if string(output) != "synthetic credential read through daemon" {
		t.Fatalf("unexpected client result %q", output)
	}
	names := docker(nil, "ps", "-a", "--filter", "name=^/"+config.ContainerName+"$", "--format", "{{.Names}}")
	if len(names) != 0 {
		t.Fatal("offline read started a browser container")
	}
	if os.Getenv("AACT_BROWSER_DEMO_MCP_IMAGE") != "" {
		exerciseSyntheticMCP(t, docker, info, control, volume)
	}
	if os.Getenv("AACT_RUN_BROWSER_DOCKER_FULL") != "1" {
		return
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "demo_session", Value: "online-synthetic", Path: "/"})
		_, _ = w.Write([]byte("<html><body>Isolated browser authentication fixture</body></html>"))
	})}
	go func() { _ = web.Serve(listener) }()
	t.Cleanup(func() { _ = web.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	config.Origin = fmt.Sprintf("http://host.docker.internal:%d", port)
	config.StartupURL = config.Origin
	config.Adapter.Source = "def run(credentials, config):\n    return {'status': 'refreshed', 'synthetic_value': credentials[0]['value']}\n"
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations", browserRegistration{Instance: namespace, ContainerID: container, Config: config}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", config.ContainerName).Run() })
	onlineClient := strings.Replace(client, "synthetic-demo-cookie", "online-synthetic", 1)
	if out := docker(payload, "exec", "-i", container, "python3", "-c", onlineClient); string(out) != "synthetic credential read through daemon" {
		t.Fatalf("online result %q", out)
	}
	running := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName)
	if string(running) != "false" {
		t.Fatalf("browser remained running after automatic refresh: %s", running)
	}
	config.Adapter.Source = "def run(credentials, config):\n    return {'status': 'interaction_required'}\n"
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations", browserRegistration{Instance: namespace, ContainerID: container, Config: config}, nil); err != nil {
		t.Fatal(err)
	}
	interactiveClient := `import sys,json,ssl,urllib.request,urllib.error; h=json.load(sys.stdin); c=ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); c.load_verify_locations(cadata=h['certificate']); p=json.dumps({'selector':{'kind':'cookie','match':'exact','names':['demo_session']}}).encode(); r=urllib.request.Request(h['api_address']+'/v1/browser/credentials',data=p,headers={'Authorization':'Bearer '+h['token'],'Content-Type':'application/json'}); o=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=c)); exec("try:\n o.open(r,timeout=120)\n raise AssertionError('expected interaction')\nexcept urllib.error.HTTPError as e:\n assert e.code == 409\n d=json.load(e)\n assert d['viewer_url'].startswith('http://localhost:')\n print('interaction URL returned')")`
	if out := docker(payload, "exec", "-i", container, "python3", "-c", interactiveClient); string(out) != "interaction URL returned" {
		t.Fatalf("interactive result %q", out)
	}
	running = docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName)
	if string(running) != "true" {
		t.Fatal("interactive browser was not left running")
	}
	if err := testDaemonControlRequest(info, control, "DELETE", "/browser/configurations/"+container, nil, nil); err != nil {
		t.Fatal(err)
	}
	names = docker(nil, "ps", "-a", "--filter", "name=^/"+config.ContainerName+"$", "--format", "{{.Names}}")
	if len(names) != 0 {
		t.Fatal("browser container was not removed on unregister")
	}
	docker(nil, "volume", "inspect", volume)
}
