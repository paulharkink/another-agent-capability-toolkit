package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	if _, err := StartDetached(script, root, password); err == nil || !strings.Contains(err.Error(), "broader than the requested") {
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
	want := "127.0.0.1:0"
	if runtime.GOOS == "linux" {
		gateway, available, gatewayErr := dockerBridgeGatewayIP()
		if gatewayErr != nil {
			t.Fatal(gatewayErr)
		}
		if available {
			want = net.JoinHostPort(gateway.String(), "0")
		}
	}
	if err != nil || address != want {
		t.Fatalf("default bind=%q want=%q err=%v", address, want, err)
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

func TestDefaultVaultAPIListenIPSeparatesDesktopAndNativeLinux(t *testing.T) {
	gateway := net.ParseIP("172.17.0.1")
	cases := []struct {
		name, goos      string
		desktop, active bool
		gateway         net.IP
		want            string
	}{
		{name: "mac desktop", goos: "darwin", desktop: true, active: true, gateway: gateway, want: "127.0.0.1"},
		{name: "windows desktop", goos: "windows", desktop: true, active: true, gateway: gateway, want: "127.0.0.1"},
		{name: "linux desktop proxy", goos: "linux", desktop: true, active: true, gateway: gateway, want: "127.0.0.1"},
		{name: "linux engine bridge", goos: "linux", active: true, gateway: gateway, want: "172.17.0.1"},
		{name: "engine unavailable", goos: "linux", want: "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultVaultAPIListenIP(tc.goos, tc.desktop, tc.active, tc.gateway).String(); got != tc.want {
				t.Fatalf("automatic bind=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestDockerEndpointDetectionRejectsRemoteContext(t *testing.T) {
	for _, endpoint := range []string{"unix:///var/run/docker.sock", "npipe:////./pipe/docker_engine", "tcp://127.0.0.1:2375", "tcp://192.0.2.20:2375", "ssh://docker.example"} {
		got := localDockerEndpoint(endpoint)
		want := endpoint == "unix:///var/run/docker.sock" || endpoint == "npipe:////./pipe/docker_engine" || endpoint == "tcp://127.0.0.1:2375"
		if got != want {
			t.Errorf("localDockerEndpoint(%q)=%v, want %v", endpoint, got, want)
		}
	}
}

func TestStartDetachedAdvertisesContainerReachableHostWithoutConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached process helper is currently Unix-only")
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "")
	root := t.TempDir()
	password := []byte("automatic-container-route-password")
	if _, err := Open(filepath.Join(root, "credentials.kdbx"), password); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(info.APIAddress, "host.docker.internal") {
		t.Fatalf("default API address is not container-reachable: %s", info.APIAddress)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := NewContainerHandoff(info, token); err != nil {
		t.Fatalf("default daemon cannot issue a container handoff: %v", err)
	}
}

func TestStartDetachedUpgradesExistingLoopbackDaemonForContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached process helper is currently Unix-only")
	}
	root := t.TempDir()
	password := []byte("automatic-container-upgrade-password")
	if _, err := Open(filepath.Join(root, "credentials.kdbx"), password); err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	oldInfo, err := StartDetached(script, root, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "")
	newInfo, err := StartDetached(script, root, password)
	if err != nil {
		t.Fatalf("existing loopback daemon was not upgraded automatically: %v", err)
	}
	defer func() { _ = StopDetached(newInfo, controlToken) }()
	if newInfo.ControlAddress != oldInfo.ControlAddress {
		t.Fatalf("upgrade replaced the daemon process: old=%s new=%s", oldInfo.ControlAddress, newInfo.ControlAddress)
	}
	if !strings.Contains(newInfo.APIAddress, "host.docker.internal") {
		t.Fatalf("upgraded API address is not container-reachable: %s", newInfo.APIAddress)
	}
	wantAddress, err := vaultAPIListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	wantIP, _, _ := net.SplitHostPort(wantAddress)
	if newInfo.APIListenIP != strings.Trim(wantIP, "[]") {
		t.Fatalf("upgraded daemon bind=%q, want automatic route %q", newInfo.APIListenIP, wantIP)
	}
}

func TestStartDetachedReplacesPreRouteDaemonAutomatically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detached process helper is currently Unix-only")
	}
	root := t.TempDir()
	password := []byte("upgrade-pre-route-daemon-password")
	if _, err := Open(filepath.Join(root, "credentials.kdbx"), password); err != nil {
		t.Fatal(err)
	}
	controlToken, err := ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(controlToken)
	var stopped atomic.Bool
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && r.Header.Get("Authorization") != "Bearer "+string(controlToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/health":
			if stopped.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/container-network":
			http.Error(w, "not found", http.StatusNotFound)
		case "/shutdown":
			stopped.Store(true)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer legacy.Close()
	legacyInfo := DaemonInfo{APIAddress: "https://localhost:12345", APIListenIP: "127.0.0.1", APICertificate: "legacy certificate", ControlAddress: legacy.URL}
	if err := writeDaemonInfo(filepath.Join(root, daemonInfoFilename), legacyInfo); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "aact-test-daemon")
	quotedExecutable := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nAACT_TEST_DAEMON_CHILD=1 exec "+quotedExecutable+" -test.run=^TestVaultDaemonChildHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AACT_VAULT_API_BIND_IP", "127.0.0.1")
	info, err := StartDetached(script, root, password)
	if err != nil {
		t.Fatalf("old daemon did not upgrade without manual stop: %v", err)
	}
	defer func() { _ = StopDetached(info, controlToken) }()
	if !stopped.Load() {
		t.Fatal("pre-route daemon was not shut down through its authenticated control endpoint")
	}
	if info.ControlAddress == legacyInfo.ControlAddress {
		t.Fatal("pre-route daemon control endpoint was reused")
	}
	if !strings.Contains(info.APIAddress, "host.docker.internal") {
		t.Fatalf("new daemon API address is not container reachable: %s", info.APIAddress)
	}
	if _, err := os.Stat(filepath.Join(root, "credentials.kdbx")); err != nil {
		t.Fatalf("replacing daemon lost the KeePass vault: %v", err)
	}
}

func TestStartDetachedClosesPreviousSpecificAPIListenerAfterRouteMove(t *testing.T) {
	if runtime.GOOS == "linux" {
		desktop, available, err := dockerDesktopEngine()
		if err != nil || !desktop || !available {
			t.Skip("loopback route movement is only valid with Docker Desktop on Linux")
		}
	}
	oldListener := newTrackedListener("127.0.0.1:32123")
	newListener := newTrackedListener("127.0.0.2:32123")
	t.Cleanup(func() { _ = newListener.Close() })
	listenCalls := 0
	manager := &apiListenerManager{
		server:    &http.Server{},
		info:      DaemonInfo{APIAddress: "https://host.docker.internal:32123", APIListenIP: "127.0.0.1", APICertificate: "test-cert"},
		infoPath:  filepath.Join(t.TempDir(), daemonInfoFilename),
		listeners: map[string]net.Listener{"127.0.0.1": oldListener},
		serveErr:  make(chan error, 2),
		listen: func(network, address string) (net.Listener, error) {
			listenCalls++
			if network != "tcp" || address != "127.0.0.2:32123" {
				t.Fatalf("unexpected replacement listener request: %s %s", network, address)
			}
			return newListener, nil
		},
	}
	updated, err := manager.ensureContainerReachable("127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	if listenCalls != 1 || updated.APIListenIP != "127.0.0.2" {
		t.Fatalf("listener update calls=%d metadata=%+v", listenCalls, updated)
	}
	select {
	case <-oldListener.closed:
	default:
		t.Fatal("old specific listener remained open after successful route move")
	}
	if manager.listeners["127.0.0.2"] != newListener {
		t.Fatal("new specific listener was not retained")
	}
}

type trackedListener struct {
	address net.Addr
	closed  chan struct{}
	once    sync.Once
}

func newTrackedListener(address string) *trackedListener {
	addr, _ := net.ResolveTCPAddr("tcp", address)
	return &trackedListener{address: addr, closed: make(chan struct{})}
}

func (l *trackedListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}
func (l *trackedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
func (l *trackedListener) Addr() net.Addr { return l.address }

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

func TestUnavailableDockerReusesOnlyMatchingLoopbackDaemon(t *testing.T) {
	for _, tc := range []struct {
		name, bind string
		want       bool
	}{
		{name: "same loopback", bind: "127.0.0.1:0", want: true},
		{name: "different loopback", bind: "127.0.0.2:0", want: false},
		{name: "ipv4 wildcard", bind: "127.0.0.1:0", want: false},
		{name: "ipv6 wildcard", bind: "127.0.0.1:0", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip := "127.0.0.1"
			if tc.name == "ipv4 wildcard" {
				ip = "0.0.0.0"
			}
			if tc.name == "ipv6 wildcard" {
				ip = "::"
			}
			info := DaemonInfo{APIListenIP: ip}
			if got := canReuseLoopbackDaemon(info, tc.bind); got != tc.want {
				t.Fatalf("canReuseLoopbackDaemon(%q, %q)=%v, want %v", ip, tc.bind, got, tc.want)
			}
		})
	}
}

func TestWaitForDetachedDaemonWaitsForDelayedReadinessAndReportsDiagnostics(t *testing.T) {
	root := t.TempDir()
	infoPath := filepath.Join(root, daemonInfoFilename)
	logPath := filepath.Join(root, "vault-daemon.log")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	info := DaemonInfo{APIAddress: "https://localhost:12345", APICertificate: "fixture-certificate", ControlAddress: server.URL}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = writeDaemonInfo(infoPath, info)
	}()
	got, err := waitForDetachedDaemon(infoPath, logPath, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got.ControlAddress != info.ControlAddress {
		t.Fatalf("ready daemon info = %#v, want %#v", got, info)
	}
}

func TestWaitForDetachedDaemonIncludesHealthAndLogOnTimeout(t *testing.T) {
	root := t.TempDir()
	infoPath := filepath.Join(root, daemonInfoFilename)
	logPath := filepath.Join(root, "vault-daemon.log")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	info := DaemonInfo{APIAddress: "https://localhost:12345", APICertificate: "fixture-certificate", ControlAddress: server.URL}
	if err := writeDaemonInfo(infoPath, info); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("fixture startup failure"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := waitForDetachedDaemon(infoPath, logPath, 80*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "fixture startup failure") {
		t.Fatalf("timeout diagnostic = %v", err)
	}
}

func TestWaitForDetachedDaemonExplainsEmptyLogTimeout(t *testing.T) {
	root := t.TempDir()
	_, err := waitForDetachedDaemon(filepath.Join(root, daemonInfoFilename), filepath.Join(root, "vault-daemon.log"), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "daemon log is empty") {
		t.Fatalf("empty-log diagnostic = %v", err)
	}
}

func TestDetachedDaemonLogDiagnosticUsesBoundedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault-daemon.log")
	content := strings.Repeat("x", detachedDaemonLogTailBytes+20) + "final-diagnostic"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readDetachedDaemonLogTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "earlier log lines truncated") || !strings.Contains(got, "final-diagnostic") {
		t.Fatalf("log tail missing truncation marker or final content: %q", got)
	}
	if len(got) > detachedDaemonLogTailBytes+64 {
		t.Fatalf("log tail length = %d, want bounded near %d", len(got), detachedDaemonLogTailBytes)
	}
}
