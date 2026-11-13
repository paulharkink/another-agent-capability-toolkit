//go:build !windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/vault"
)

func TestFirstLaunchPTYMigratesLegacySecretsIntoKeePassAndReopens(t *testing.T) {
	t.Setenv("AACT_VAULT_API_BIND_IP", "")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable for the PTY smoke test")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate PTY test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	work := t.TempDir()
	home := filepath.Join(work, "home")
	stateHome := filepath.Join(home, ".local", "state")
	legacyRoot := filepath.Join(stateHome, "agent-skills")
	newRoot := filepath.Join(stateHome, "aact")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	// Seed the historical default root through the same state APIs that wrote
	// existing instance answers, private inputs, and auth aliases.
	t.Setenv("AACT_STATE_DIR", legacyRoot)
	legacy, err := state.Open("")
	if err != nil {
		t.Fatal(err)
	}
	first := state.Key{Source: "fixture", Package: "migration", Target: "one"}
	second := state.Key{Source: "fixture", Package: "migration", Target: "two"}
	for _, key := range []state.Key{first, second} {
		if err := legacy.RecordInstance(state.InstanceRecord{Key: key, Name: key.Target}); err != nil {
			t.Fatal(err)
		}
		if err := legacy.SaveAnswers(key, map[string]any{"region": "eu-west"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.SaveSecretInputs(first, map[string]any{"token": "legacy-instance-secret"}, nil); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(legacyRoot, "profiles", "shared-auth")
	if err := legacy.AdoptAuth(second, aliasDir); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SaveSecretInputs(second, map[string]any{"token": "legacy-aliased-secret"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy.Root(), "manager", "installation-id"), []byte("fixture-installation-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_STATE_DIR", "")

	binary := filepath.Join(work, "aact")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/aact")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual AACT binary: %v\n%s", err, output)
	}
	pythonScript, err := os.ReadFile(filepath.Join(repoRoot, "internal/cli/testdata/vault_startup_pty.py"))
	if err != nil {
		t.Fatal(err)
	}
	smokeCtx, cancelSmoke := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancelSmoke()
	smoke := exec.CommandContext(smokeCtx, python, "-c", string(pythonScript), binary)
	smoke.Dir = work
	smoke.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome, "AACT_STATE_DIR=", "AACT_VAULT_API_BIND_IP=", "AACT_BUNDLED_ROOT="+filepath.Join(repoRoot, "packages"))
	if output, err := smoke.CombinedOutput(); err != nil {
		t.Fatalf("actual AACT startup PTY flow: %v\n%s", err, output)
	}
	cleanupInfo, infoErr := vault.ReadDaemonInfo(newRoot)
	cleanupControl, controlErr := vault.ControlTokenForVault(filepath.Join(newRoot, "credentials.kdbx"), []byte("first launch actual password"))
	if infoErr == nil && controlErr == nil {
		t.Cleanup(func() { _ = vault.StopDetached(cleanupInfo, cleanupControl); clear(cleanupControl) })
	}

	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy default root remains after migration: %v", err)
	}
	store, err := state.Open(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(newRoot, "manager", "installation-id")); err != nil || string(got) != "fixture-installation-id\n" {
		t.Fatalf("installation identity after migration=%q err=%v", got, err)
	}
	instances, err := store.Instances()
	if err != nil || len(instances) != 2 {
		t.Fatalf("instances after migration=%+v err=%v", instances, err)
	}
	for _, key := range []state.Key{first, second} {
		answers, err := store.Answers(key)
		if err != nil || answers["region"] != "eu-west" {
			t.Fatalf("answers for %s after migration=%v err=%v", key.Target, answers, err)
		}
	}
	if got := store.AuthDir(second); got != filepath.Join(newRoot, "profiles", "shared-auth") {
		t.Fatalf("auth alias did not follow the migrated root: %s", got)
	}
	vaultPath := filepath.Join(newRoot, "credentials.kdbx")
	if _, err := os.Stat(vaultPath); err != nil {
		t.Fatalf("first launch did not create KeePass vault: %v", err)
	}
	if _, err := vault.Open(vaultPath, []byte("first launch actual password")); err != nil {
		t.Fatalf("created vault cannot be reopened by KeePass password: %v", err)
	}
	vaultStore, err := vault.Open(vaultPath, []byte("first launch actual password"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetSecretBackend(vaultStore)
	for key, want := range map[state.Key]string{first: "legacy-instance-secret", second: "legacy-aliased-secret"} {
		values, err := store.SecretInputs(key)
		if err != nil || values["token"] != want {
			t.Fatalf("migrated secret for %s=%v err=%v, want %q", key.Target, values, err, want)
		}
		if _, err := os.Stat(filepath.Join(store.AuthDir(key), "aact-private-inputs.json")); !os.IsNotExist(err) {
			t.Fatalf("plaintext private inputs remain for %s: %v", key.Target, err)
		}
	}
	info, err := vault.ReadDaemonInfo(newRoot)
	if err != nil || !vault.DaemonRunning(info) {
		t.Fatalf("actual first launch did not start/reuse the vault daemon: info=%+v err=%v", info, err)
	}
	control, err := vault.ControlTokenForVault(vaultPath, []byte("first launch actual password"))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AACT_TEST_DOCKER") == "1" {
		t.Cleanup(func() { clear(control) })
		verifyMigratedSecretFromDisposableContainer(t, info, control, vaultStore, first.ID())
	} else {
		clear(control)
	}
}

func verifyMigratedSecretFromDisposableContainer(t *testing.T, info vault.DaemonInfo, control []byte, store *vault.Store, namespace string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("AACT_TEST_DOCKER=1 but docker CLI is unavailable")
	}
	if output, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Fatalf("AACT_TEST_DOCKER=1 but Docker Engine is unavailable: %v: %s", err, output)
	}
	store.ConfigureDetachedDaemon(info, control)
	handoff, revoke, err := store.IssueContainerHandoff(namespace)
	if err != nil {
		t.Fatalf("issue handoff for the secret migrated during this PTY run: %v", err)
	}
	defer func() {
		if err := revoke(); err != nil {
			t.Errorf("revoke disposable container token: %v", err)
		}
	}()
	js := `const https=require('https');const u=new URL(process.env.AACT_API);const req=https.get({hostname:u.hostname,port:u.port,path:'/v1/secrets',ca:process.env.AACT_CERT,headers:{Authorization:'Bearer '+process.env.AACT_TOKEN}},res=>{let body='';res.on('data',x=>body+=x);res.on('end',()=>{if(res.statusCode!==200)throw Error('HTTP '+res.statusCode+': '+body);const v=JSON.parse(body);const record=JSON.parse(v['aact-private-inputs-v1']);if(record.values.token!=='legacy-instance-secret')throw Error('migrated secret missing');if(body.includes('legacy-aliased-secret'))throw Error('another instance secret crossed the grant');console.log('migrated secret and scoped TLS handoff passed')})});req.on('error',e=>{console.error(e);process.exitCode=1});`
	args := []string{"run", "--rm", "--network", "bridge"}
	if runtime.GOOS == "linux" {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	args = append(args, "-e", "AACT_API="+handoff.APIAddress, "-e", "AACT_CERT="+handoff.Certificate, "-e", "AACT_TOKEN="+handoff.Token, "node:22-alpine", "node", "-e", js)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "scoped TLS handoff passed") {
		t.Fatalf("disposable container could not read the migrated secret from %s (%s): %v\n%s", info.APIAddress, info.APIListenIP, err, output)
	}
}
