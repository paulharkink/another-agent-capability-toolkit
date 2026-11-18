//go:build integration

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
	"time"

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
	container := string(docker(nil, "run", "-d", "--add-host", "host.docker.internal:host-gateway", "--entrypoint", "sleep", image, "300"))
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
	var opened struct {
		ViewerURL string `json:"viewer_url"`
	}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations/"+container+"/start", nil, &opened); err != nil {
		t.Fatalf("open generic browser login: %v", err)
	}
	if !strings.HasPrefix(opened.ViewerURL, "http://localhost:") {
		t.Fatalf("browser login returned an invalid viewer URL %q", opened.ViewerURL)
	}
	runningHelper := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName)
	if string(runningHelper) != "true" {
		t.Fatalf("browser login did not start its helper: %s", runningHelper)
	}
	foreignContainer := strings.Repeat("f", 64)
	if foreignContainer == container {
		t.Fatal("unexpected fixture ID collision")
	}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations/"+foreignContainer+"/start", nil, &opened); err == nil {
		t.Fatal("browser login opened a helper for an unregistered container")
	}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations/"+container+"/stop", nil, nil); err != nil {
		t.Fatalf("close login helper before the fixture's offline cookie read: %v", err)
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
	stoppedHelper := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName)
	if string(stoppedHelper) != "false" {
		t.Fatalf("successful offline credential handoff left the login helper running: %s", stoppedHelper)
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
	names := docker(nil, "ps", "-a", "--filter", "name=^/"+config.ContainerName+"$", "--format", "{{.Names}}")
	if len(names) != 0 {
		t.Fatal("browser container was not removed on unregister")
	}
	docker(nil, "volume", "inspect", volume)
}

func TestBrowserLoginHandoffAuthenticatesSyntheticMCPRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached test helper uses a POSIX shell")
	}
	image := "aact/browser-authentication-helper:1.18.0"
	docker := func(input []byte, args ...string) []byte {
		t.Helper()
		cmd := exec.Command("docker", args...)
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s failed: %v: %s", args[0], err, out)
		}
		return bytes.TrimSpace(out)
	}
	docker(nil, "image", "inspect", image)
	volume := string(docker(nil, "volume", "create", "--label", "aact.test=browser-mcp-flow"))
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", volume).Run() })
	fixturePath, err := filepath.Abs("../../testdata/browser-auth-mcp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	fixturePath = fixtureWithPrefetchedPythonBase(t, fixturePath)
	mcpImage := fmt.Sprintf("aact-test/browser-auth-mcp:%x", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", mcpImage).Run() })
	docker(nil, "build", "--tag", mcpImage, fixturePath)

	mcpUpstreamURL := "http://127.0.0.1:8766"
	mcpContainer := string(docker(nil, "run", "-d", "--add-host", "host.docker.internal:host-gateway", "--tmpfs", "/run/aact:rw,noexec,nosuid,nodev,size=16k,mode=1777", "--env", "AACT_SYNTHETIC_UPSTREAM="+mcpUpstreamURL, mcpImage, "sh", "-c", "while [ ! -f /run/aact/vault.json ]; do sleep 0.1; done; exec python3 /app/server.py"))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", mcpContainer).Run() })
	browserAddress := string(docker(nil, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", mcpContainer))
	browserOrigin := fmt.Sprintf("http://%s:8766", browserAddress)

	root := t.TempDir()
	password := []byte("synthetic-browser-mcp-password")
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
	if err := vault.BindContainerToken(info, control, mcpContainer, token); err != nil {
		t.Fatal(err)
	}
	accessConfig, err := json.Marshal(map[string]any{"allowed_urls": []string{browserOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	config := browsercontroller.ServiceConfig{
		Origin: browserOrigin, StartupURL: browserOrigin,
		AccessConfigJSON: string(accessConfig), ProfileVolume: volume, ContainerName: "aact-test-login-" + mcpContainer[:16],
		Adapter: browsercontroller.BrowserAuthAdapter{Source: "def run(credentials, config):\n    if not credentials:\n        return {'status': 'interaction_required'}\n    return {'status': 'refreshed', 'access_token': credentials[0]['value']}\n"},
	}
	helperName := config.ContainerName
	t.Cleanup(func() {
		owner, err := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "aact.browser-controller"}} {{index .Config.Labels "aact.browser-controller.profile-volume"}}`, helperName).Output()
		if err == nil && strings.TrimSpace(string(owner)) == "1 "+volume {
			_ = exec.Command("docker", "rm", "-f", helperName).Run()
		}
	})
	register := func() {
		t.Helper()
		if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations", browserRegistration{Instance: namespace, ContainerID: mcpContainer, Config: config}, nil); err != nil {
			t.Fatal(err)
		}
	}
	register()
	handoff, err := vault.NewContainerHandoff(info, token)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(handoff)
	redactFixtureSecrets := func(value string) string {
		for _, secret := range [][]byte{[]byte(token), control, password, payload, []byte("synthetic-signed-in-token")} {
			if len(secret) > 0 {
				value = strings.ReplaceAll(value, string(secret), "[redacted]")
			}
		}
		return value
	}
	fixtureDiagnostics := func() string {
		inspect := exec.Command("docker", "inspect", "--format", "status={{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} error={{.State.Error}}", mcpContainer)
		state, _ := inspect.CombinedOutput()
		logsCommand := exec.Command("docker", "logs", "--tail", "80", mcpContainer)
		logs, _ := logsCommand.CombinedOutput()
		diagnostics := redactFixtureSecrets(string(logs))
		return fmt.Sprintf("container %s; recent fixture logs:\n%s", strings.TrimSpace(string(state)), diagnostics)
	}
	install := `import os,sys; p='/run/aact/vault.json'; tmp=p+'.tmp'; fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o400); f=os.fdopen(fd,'wb'); f.write(sys.stdin.buffer.read()); f.flush(); os.fsync(f.fileno()); f.close(); os.replace(tmp,p)`
	docker(payload, "exec", "-i", mcpContainer, "python3", "-c", install)
	route := docker(nil, "exec", mcpContainer, "python3", "-c", `import json,socket,urllib.parse
h=json.load(open('/run/aact/vault.json')); u=urllib.parse.urlsplit(h['api_address']); host=socket.gethostbyname(u.hostname); s=socket.create_connection((host,u.port),timeout=5); s.close(); print('vault API gateway reachable:',host,u.port)`)
	t.Log(string(route))
	readiness := exec.Command("docker", "exec", mcpContainer, "python3", "-c", `import socket,time
deadline=time.time()+20
for port in (8765,8766):
 while time.time()<deadline:
  s=socket.socket(); s.settimeout(.2)
  try: s.connect(('127.0.0.1',port)); s.close(); break
  except OSError: time.sleep(.1)
 else: raise RuntimeError('synthetic capability runtime did not start')`)
	if output, err := readiness.CombinedOutput(); err != nil {
		t.Fatalf("synthetic MCP startup probe failed: %v: %s; %s", err, redactFixtureSecrets(string(output)), fixtureDiagnostics())
	}

	var opened struct {
		ViewerURL string `json:"viewer_url"`
	}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations/"+mcpContainer+"/start", nil, &opened); err != nil {
		t.Fatalf("start generic browser login: %v", err)
	}
	if !strings.HasPrefix(opened.ViewerURL, "http://localhost:") {
		t.Fatalf("invalid browser viewer URL %q", opened.ViewerURL)
	}

	client := `import json,sys,urllib.request
def rpc(i,m,p=None):
 d=json.dumps({'jsonrpc':'2.0','id':i,'method':m,'params':p or {}}).encode(); q=urllib.request.Request('http://127.0.0.1:8765/mcp',data=d,headers={'Content-Type':'application/json','Accept':'application/json, text/event-stream'}); return json.load(urllib.request.urlopen(q,timeout=60))
rpc(1,'initialize',{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'aact-integration','version':'1'}})
q=urllib.request.Request('http://127.0.0.1:8765/mcp',data=b'{"jsonrpc":"2.0","method":"notifications/initialized"}',headers={'Content-Type':'application/json'}); urllib.request.urlopen(q,timeout=10).close()
listed=rpc(2,'tools/list'); assert any(t['name']=='list_projects' for t in listed['result']['tools'])
r=rpc(3,'tools/call',{'name':'list_projects','arguments':{}}); print(json.dumps(r))`
	callTool := func() map[string]any {
		t.Helper()
		cmd := exec.Command("docker", "exec", mcpContainer, "python3", "-c", client)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic MCP client call failed: %v: %s; %s", err, redactFixtureSecrets(string(out)), fixtureDiagnostics())
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("decode MCP client result %q: %v", out, err)
		}
		return result
	}
	first := callTool()
	if !strings.Contains(fmt.Sprint(first), opened.ViewerURL) {
		t.Fatalf("initial MCP operation did not return the login URL %q: %v", opened.ViewerURL, first)
	}

	if running := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName); string(running) != "true" {
		t.Fatalf("login helper stopped during initial interaction: %s", running)
	}
	retry := callTool()
	if !strings.Contains(fmt.Sprint(retry), opened.ViewerURL) {
		t.Fatalf("retry changed or lost login URL %q: %v", opened.ViewerURL, retry)
	}

	if running := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName); string(running) != "true" {
		t.Fatalf("login helper stopped during retry: %s", running)
	}
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations/"+mcpContainer+"/stop", nil, nil); err != nil {
		t.Fatalf("stop helper to simulate completed sign-in: %v", err)
	}
	fixture := `import pathlib,sqlite3,os; p=pathlib.Path('/profile/.config/chromium/Default/Network'); p.mkdir(parents=True); c=sqlite3.connect(str(p/'Cookies')); c.execute('CREATE TABLE cookies(host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER, is_secure INTEGER)'); c.execute('INSERT INTO cookies VALUES(?,?,?,?,?,?)',('` + browserAddress + `','session','synthetic-signed-in-token',b'',0,0)); c.commit(); c.close(); os.chmod(p/'Cookies',0o644); [(os.chown(root,1000,1000),[os.chown(os.path.join(root,n),1000,1000) for n in files if os.path.exists(os.path.join(root,n))]) for root,dirs,files in os.walk('/profile')]`
	docker(nil, "run", "--rm", "--user", "0", "--network", "none", "--entrypoint", "python3", "--mount", "source="+volume+",target=/profile", image, "-c", fixture)
	if err := testDaemonControlRequest(info, control, "POST", "/browser/configurations/"+mcpContainer+"/start", nil, &opened); err != nil {
		t.Fatalf("reopen helper after synthetic sign-in: %v", err)
	}
	authenticated := callTool()
	if !strings.Contains(fmt.Sprint(authenticated), "project-from-authenticated-upstream") {
		t.Fatalf("post-login MCP tool did not reach the authenticated upstream: %v", authenticated)
	}
	if running := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName); string(running) != "false" {
		t.Fatalf("successful handoff left login helper running: %s", running)
	}
	afterShutdown := callTool()
	if !strings.Contains(fmt.Sprint(afterShutdown), "project-from-authenticated-upstream") {
		t.Fatalf("post-shutdown MCP tool call did not use the offline session: %v", afterShutdown)
	}
	if running := docker(nil, "inspect", "--format", "{{.State.Running}}", config.ContainerName); string(running) != "false" {
		t.Fatalf("offline MCP call restarted the stopped helper: %s", running)
	}
	if err := testDaemonControlRequest(info, control, "DELETE", "/browser/configurations/"+mcpContainer, nil, nil); err != nil {
		t.Fatal(err)
	}
}
