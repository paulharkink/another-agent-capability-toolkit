package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDaemonCertificatePairStoredAsOneRecordAndRepairsLegacyPartial(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("certificate-pair-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(daemonSecretNamespace, daemonTLSCertSecretName, []byte("orphaned-cert")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateDaemonCertificate(store); err != nil {
		t.Fatal(err)
	}
	values, err := store.List(daemonSecretNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values[daemonTLSKeyPairSecretName]; !ok {
		t.Fatal("atomic key pair record was not stored")
	}
	if _, ok := values[daemonTLSCertSecretName]; ok {
		t.Fatal("partial legacy certificate was not removed")
	}
	if _, ok := values[daemonTLSKeySecretName]; ok {
		t.Fatal("legacy key record unexpectedly remains")
	}
}

func TestVaultDaemonChildHelper(t *testing.T) {
	if os.Getenv("AACT_TEST_DAEMON_CHILD") != "1" {
		return
	}
	if err := RunDetached(context.Background(), os.Stdin); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestStartDetachedOwnsIndependentPortableProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper is Unix-only")
	}
	root := t.TempDir()
	password := []byte("detached-process-test-password")
	if _, err := Open(filepath.Join(root, "credentials.kdbx"), password); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "0.0.0.0")
	controlToken, err := ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(controlToken)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "aact-test-daemon")
	quotedExecutable := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	body := "#!/bin/sh\nAACT_TEST_DAEMON_CHILD=1 exec " + quotedExecutable + " -test.run=^TestVaultDaemonChildHelper$\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := StartDetached(script, root, password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = StopDetached(info, controlToken) }()
	metadata, err := os.ReadFile(filepath.Join(root, daemonInfoFilename))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadata, controlToken) {
		t.Fatal("plaintext admin bearer appears in daemon metadata")
	}
	if _, err := os.Stat(filepath.Join(root, "vault-daemon-key.pem")); !os.IsNotExist(err) {
		t.Fatalf("TLS private key stored outside KDBX: %v", err)
	}
	database, err := os.ReadFile(filepath.Join(root, "credentials.kdbx"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, controlToken) {
		t.Fatal("plaintext admin bearer appears in KDBX bytes")
	}
	if !DaemonRunning(info) {
		t.Fatal("detached daemon is not healthy")
	}
	reused, err := StartDetached(script, root, password)
	if err != nil || reused.ControlAddress != info.ControlAddress {
		t.Fatalf("daemon was not reused: info=%#v err=%v", reused, err)
	}
	if info.APIListenIP != "0.0.0.0" {
		t.Fatalf("persisted bind IP=%q", info.APIListenIP)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "192.0.2.10")
	if _, err := StartDetached(script, root, password); err == nil || !strings.Contains(err.Error(), "API bind IP") {
		t.Fatalf("wildcard listener reused for narrower bind: %v", err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "0.0.0.0")
	if err := StopDetached(info, controlToken); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for DaemonRunning(info) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if DaemonRunning(info) {
		t.Fatal("detached daemon failed to stop")
	}
}

func TestDetachedDaemonControlIssuesScopedTokenAndStops(t *testing.T) {
	t.Setenv("AACT_VAULT_API_BIND_IP", "0.0.0.0")
	root := t.TempDir()
	store, err := Open(root+"/credentials.kdbx", []byte("daemon-test-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	controlToken, err := ControlTokenForVault(root+"/credentials.kdbx", []byte("daemon-test-master-password"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(controlToken)
	config, err := json.Marshal(daemonBootstrap{StateRoot: root, Password: []byte("daemon-test-master-password"), ControlToken: controlToken})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunDetached(ctx, strings.NewReader(string(config))) }()
	var info DaemonInfo
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case daemonErr := <-done:
			if daemonErr != nil {
				t.Fatalf("daemon exited before publishing info: %v", daemonErr)
			}
			t.Fatal("daemon exited before publishing info without an error")
		default:
		}
		info, err = ReadDaemonInfo(root)
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("daemon did not publish info: %v", err)
	}
	instance := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token, err := IssueContainerToken(info, controlToken, instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindContainerToken(info, controlToken, "old-container", token); err != nil {
		t.Fatal(err)
	}
	replacementToken, err := IssueContainerToken(info, controlToken, instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindContainerToken(info, controlToken, "new-container", replacementToken); err != nil {
		t.Fatal(err)
	}
	apiURL := strings.Replace(info.APIAddress, "host.docker.internal", "127.0.0.1", 1)
	client, err := NewContainerHTTPClient(info)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, apiURL+"/v1/secrets", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("API status=%d", response.StatusCode)
	}
	if err := RevokeContainerTokens(info, controlToken, instance, "old-container"); err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token status after container-scoped revoke=%d", response.StatusCode)
	}
	newRequest, err := http.NewRequest(http.MethodGet, apiURL+"/v1/secrets", nil)
	if err != nil {
		t.Fatal(err)
	}
	newRequest.Header.Set("Authorization", "Bearer "+replacementToken)
	response, err = client.Do(newRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replacement token status=%d", response.StatusCode)
	}
	store.ConfigureDetachedDaemon(info, controlToken)
	if err := store.RevokeContainerHandoffs(instance); err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked container token status=%d", response.StatusCode)
	}
	if err := StopDetached(info, controlToken); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := ReadDaemonInfo(root); err == nil {
		t.Fatal("daemon info remains after stop")
	}
}

func TestVaultAPIListenerDefaultsToLoopbackAndRequiresExplicitIPOverride(t *testing.T) {
	t.Setenv("AACT_VAULT_API_BIND_IP", "")
	address, err := vaultAPIListenAddress()
	if err != nil || address != "127.0.0.1:0" {
		t.Fatalf("default bind=%q err=%v", address, err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "192.0.2.10")
	address, err = vaultAPIListenAddress()
	if err != nil || address != "192.0.2.10:0" {
		t.Fatalf("override bind=%q err=%v", address, err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "not-an-ip")
	if _, err := vaultAPIListenAddress(); err == nil {
		t.Fatal("invalid bind override accepted")
	}
}

func TestDaemonMetadataCannotRedirectControlBearerOffLoopback(t *testing.T) {
	forwarded := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true; w.WriteHeader(http.StatusNoContent) }))
	defer remote.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, remote.URL, http.StatusFound) }))
	defer redirect.Close()
	info := DaemonInfo{APIAddress: "https://host.docker.internal:12345", APICertificate: "public-cert", ControlAddress: redirect.URL}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if err := controlRequest(info, token, http.MethodPost, "/shutdown", nil, nil); err == nil {
		t.Fatal("redirected control request succeeded")
	}
	if forwarded {
		t.Fatal("control bearer followed redirect to another host")
	}
	info.ControlAddress = "http://attacker.example:12345"
	if DaemonRunning(info) {
		t.Fatal("remote control host was trusted")
	}
}
