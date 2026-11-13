//go:build docker_integration

package vault

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDockerContainerReachesDefaultVaultRouteWithPinnedTLS(t *testing.T) {
	if os.Getenv("AACT_TEST_DOCKER") != "1" {
		t.Skip("set AACT_TEST_DOCKER=1 to run the disposable Docker route fixture")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	if output, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("Docker Engine is not available: %v: %s", err, output)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "")
	root := t.TempDir()
	password := []byte("synthetic-container-route-master-password")
	path := root + "/credentials.kdbx"
	store, err := Open(path, password)
	if err != nil {
		t.Fatal(err)
	}
	const namespace = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const unrelated = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := store.Put(namespace, "migration-token", []byte("synthetic-migrated-secret")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(unrelated, "unrelated-token", []byte("must-not-cross-scope")); err != nil {
		t.Fatal(err)
	}
	controlToken, err := ControlTokenForVault(path, password)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(controlToken)
	bootstrap, err := json.Marshal(daemonBootstrap{StateRoot: root, Password: password, ControlToken: controlToken})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemonDone := make(chan error, 1)
	bootstrapReader := strings.NewReader(string(bootstrap))
	clear(bootstrap)
	go func() { daemonDone <- RunDetached(ctx, bootstrapReader) }()
	var info DaemonInfo
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case daemonErr := <-daemonDone:
			if daemonErr != nil {
				t.Fatalf("vault daemon exited before startup: %v", daemonErr)
			}
			t.Fatal("vault daemon exited before publishing endpoint metadata")
		default:
		}
		info, err = ReadDaemonInfo(root)
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("vault daemon did not publish metadata: %v", err)
	}
	defer func() { _ = StopDetached(info, controlToken) }()
	info, err = EnsureContainerAPIReachable(info, controlToken)
	if err != nil {
		t.Fatalf("prepare default container route without bind override: %v", err)
	}
	token, err := IssueContainerToken(info, controlToken, namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = RevokeContainerToken(info, controlToken, token) }()
	handoff, err := NewContainerHandoff(info, token)
	if err != nil {
		t.Fatal(err)
	}
	js := `const https=require('https');const u=new URL(process.env.AACT_API);const req=https.get({hostname:u.hostname,port:u.port,path:'/v1/secrets',ca:process.env.AACT_CERT,headers:{Authorization:'Bearer '+process.env.AACT_TOKEN}},res=>{let body='';res.on('data',x=>body+=x);res.on('end',()=>{if(res.statusCode!==200)throw Error('HTTP '+res.statusCode+': '+body);const v=JSON.parse(body);if(v['migration-token']!=='synthetic-migrated-secret')throw Error('migrated value missing');if(v['unrelated-token']!==undefined)throw Error('namespace isolation failed');console.log('default host route and pinned TLS passed')})});req.on('error',e=>{console.error(e);process.exitCode=1});`
	args := []string{"run", "--rm", "--network", "bridge"}
	if runtime.GOOS == "linux" {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	args = append(args, "-e", "AACT_API="+handoff.APIAddress, "-e", "AACT_CERT="+handoff.Certificate, "-e", "AACT_TOKEN="+handoff.Token, "node:22-alpine", "node", "-e", js)
	commandCtx, commandCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer commandCancel()
	command := exec.CommandContext(commandCtx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("disposable container could not reach the automatically configured daemon at %s (%s): %v\n%s", info.APIAddress, info.APIListenIP, err, output)
	}
	if !strings.Contains(string(output), "pinned TLS passed") {
		t.Fatalf("container did not report a verified vault response: %s", output)
	}
}
