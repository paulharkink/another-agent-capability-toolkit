package vault

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	daemonInfoFilename           = "vault-daemon.json"
	detachedDaemonStartupTimeout = 30 * time.Second
	detachedDaemonPollInterval   = 100 * time.Millisecond
	detachedDaemonLogTailBytes   = 16 << 10
)

type DaemonInfo struct {
	APIAddress     string `json:"api_address"`
	APIListenIP    string `json:"api_listen_ip,omitempty"`
	APICertificate string `json:"api_certificate"`
	ControlAddress string `json:"control_address"`
}

type daemonBootstrap struct {
	StateRoot        string `json:"state_root"`
	Password         []byte `json:"password"`
	ControlToken     []byte `json:"control_token"`
	APIListenAddress string `json:"api_listen_address,omitempty"`
}

var errContainerRouteEndpointUnsupported = errors.New("vault daemon does not support automatic container route setup")

type controlStatusError struct{ statusCode int }

func (e *controlStatusError) Error() string {
	return fmt.Sprintf("vault daemon control request failed with HTTP %d", e.statusCode)
}

// StartDetached starts or reuses AACT's portable background vault process.
// Its bootstrap password is delivered only over the child process stdin pipe.
func StartDetached(executable, stateRoot string, password []byte) (DaemonInfo, error) {
	if executable == "" {
		return DaemonInfo{}, errors.New("AACT executable path is required")
	}
	if len(password) == 0 {
		return DaemonInfo{}, errors.New("vault master password must not be empty")
	}
	root, err := filepath.Abs(stateRoot)
	if err != nil {
		return DaemonInfo{}, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return DaemonInfo{}, err
	}
	if err := os.Chmod(root, 0700); err != nil {
		return DaemonInfo{}, err
	}
	bindAddress, err := vaultAPIListenAddress()
	if err != nil {
		return DaemonInfo{}, err
	}
	controlToken, err := ControlTokenForVault(filepath.Join(root, "credentials.kdbx"), password)
	if err != nil {
		return DaemonInfo{}, err
	}
	defer clear(controlToken)
	unlock, err := lockVaultFile(filepath.Join(root, "daemon-manager"))
	if err != nil {
		return DaemonInfo{}, err
	}
	defer unlock()
	infoPath := filepath.Join(root, daemonInfoFilename)
	if info, err := readDaemonInfo(infoPath); err == nil {
		if daemonHealthy(info) {
			updated, routeErr := EnsureContainerAPIReachable(info, controlToken)
			if routeErr == nil {
				return updated, nil
			}
			var statusErr *controlStatusError
			if errors.As(routeErr, &statusErr) && statusErr.statusCode == http.StatusServiceUnavailable && runtime.GOOS == "linux" && isLoopbackListenAddress(bindAddress) {
				if !canReuseLoopbackDaemon(info, bindAddress) {
					return DaemonInfo{}, fmt.Errorf("reuse vault daemon while Docker is unavailable: existing bind %q does not match requested loopback bind %q", info.APIListenIP, bindAddress)
				}
				// The UI can keep using the vault while Docker is stopped. A later
				// container handoff retries route setup after Docker starts.
				return info, nil
			}
			if !errors.Is(routeErr, errContainerRouteEndpointUnsupported) && !(errors.As(routeErr, &statusErr) && statusErr.statusCode == http.StatusNotFound) {
				return DaemonInfo{}, routeErr
			}
			// Older daemons have no authenticated route-management endpoint. Stop
			// that process with the durable control token and start the current
			// implementation using the new host-reachable listener policy.
			if err := StopDetached(info, controlToken); err != nil {
				return DaemonInfo{}, fmt.Errorf("replace old vault daemon for container access: %w", err)
			}
		}
		_ = os.Remove(infoPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return DaemonInfo{}, err
	}
	bootstrap := daemonBootstrap{StateRoot: root, Password: append([]byte(nil), password...), ControlToken: append([]byte(nil), controlToken...), APIListenAddress: bindAddress}
	payload, err := json.Marshal(bootstrap)
	clear(bootstrap.Password)
	clear(bootstrap.ControlToken)
	if err != nil {
		return DaemonInfo{}, err
	}
	logFile, err := os.OpenFile(filepath.Join(root, "vault-daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		clear(payload)
		return DaemonInfo{}, err
	}
	cmd := exec.Command(executable, "__vault-daemon")
	configureDetachedProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = logFile.Close()
		clear(payload)
		return DaemonInfo{}, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = logFile.Close()
		clear(payload)
		return DaemonInfo{}, err
	}
	if _, err := stdin.Write(payload); err != nil {
		_ = stdin.Close()
		clear(payload)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		return DaemonInfo{}, err
	}
	clear(payload)
	if err := stdin.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		return DaemonInfo{}, err
	}
	_ = logFile.Close()
	info, err := waitForDetachedDaemon(infoPath, filepath.Join(root, "vault-daemon.log"), detachedDaemonStartupTimeout)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return DaemonInfo{}, err
	}
	_ = cmd.Process.Release()
	return info, nil
}

func isLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func canReuseLoopbackDaemon(info DaemonInfo, requestedAddress string) bool {
	requestedHost, _, err := net.SplitHostPort(requestedAddress)
	if err != nil {
		return false
	}
	requestedIP := net.ParseIP(strings.Trim(requestedHost, "[]"))
	currentIP := net.ParseIP(info.APIListenIP)
	return requestedIP != nil && requestedIP.IsLoopback() && currentIP != nil && !currentIP.IsUnspecified() && currentIP.Equal(requestedIP)
}

func ReadDaemonInfo(stateRoot string) (DaemonInfo, error) {
	return readDaemonInfo(filepath.Join(stateRoot, daemonInfoFilename))
}

func DaemonRunning(info DaemonInfo) bool { return daemonHealthy(info) }

func IssueContainerToken(info DaemonInfo, controlToken []byte, namespace string) (string, error) {
	if err := validateInstanceNamespace(namespace); err != nil {
		return "", err
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := controlRequest(info, string(controlToken), http.MethodPost, "/tokens", map[string]string{"namespace": namespace}, &result); err != nil {
		return "", err
	}
	return result.Token, nil
}

func RevokeContainerToken(info DaemonInfo, controlToken []byte, token string) error {
	return controlRequest(info, string(controlToken), http.MethodDelete, "/tokens/"+token, nil, nil)
}

func BindContainerToken(info DaemonInfo, controlToken []byte, containerID, token string) error {
	return controlRequest(info, string(controlToken), http.MethodPut, "/tokens/"+token+"/container", map[string]string{"container_id": containerID}, nil)
}

func RevokeContainerTokens(info DaemonInfo, controlToken []byte, namespace, containerID string) error {
	return controlRequest(info, string(controlToken), http.MethodDelete, "/tokens/containers/"+containerID, map[string]string{"namespace": namespace}, nil)
}

func EnsureContainerAPIReachable(info DaemonInfo, controlToken []byte) (DaemonInfo, error) {
	address, err := vaultAPIListenAddress()
	if err != nil {
		return DaemonInfo{}, fmt.Errorf("determine automatic container listener: %w", err)
	}
	var updated DaemonInfo
	if err := controlRequest(info, string(controlToken), http.MethodPost, "/container-network", map[string]string{"api_listen_address": address}, &updated); err != nil {
		return DaemonInfo{}, err
	}
	if err := validateDaemonInfo(updated); err != nil {
		return DaemonInfo{}, fmt.Errorf("vault daemon returned invalid container route metadata: %w", err)
	}
	if !strings.Contains(updated.APIAddress, "host.docker.internal") {
		return DaemonInfo{}, errors.New("vault daemon did not prepare a host.docker.internal container route")
	}
	return updated, nil
}

func StopDetached(info DaemonInfo, controlToken []byte) error {
	if !daemonHealthy(info) {
		return nil
	}
	if err := controlRequest(info, string(controlToken), http.MethodPost, "/shutdown", nil, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for daemonHealthy(info) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if daemonHealthy(info) {
		return errors.New("vault daemon did not stop before timeout")
	}
	return nil
}

func ControlTokenForVault(path string, password []byte) ([]byte, error) {
	store, err := Open(path, password)
	if err != nil {
		return nil, err
	}
	value, found, err := store.Get(daemonSecretNamespace, daemonControlSecretName)
	if err != nil {
		return nil, err
	}
	if found {
		if len(value) != 32 {
			clear(value)
			return nil, errors.New("stored vault daemon control token is invalid")
		}
		encoded := []byte(base64.RawURLEncoding.EncodeToString(value))
		clear(value)
		return encoded, nil
	}
	value = make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	if err := store.Put(daemonSecretNamespace, daemonControlSecretName, value); err != nil {
		clear(value)
		return nil, err
	}
	encoded := []byte(base64.RawURLEncoding.EncodeToString(value))
	clear(value)
	return encoded, nil
}

func (s *Store) ConfigureDetachedDaemon(info DaemonInfo, controlToken []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controlToken != nil {
		clear(s.controlToken)
	}
	copyInfo := info
	s.daemonInfo = &copyInfo
	s.controlToken = append([]byte(nil), controlToken...)
}

func (s *Store) EnsureContainerAPIReachable() error {
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("vault daemon is not configured for container handoff")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	updated, err := EnsureContainerAPIReachable(info, controlToken)
	if err != nil {
		return fmt.Errorf("prepare automatic vault route for Docker containers: %w", err)
	}
	s.ConfigureDetachedDaemon(updated, controlToken)
	return nil
}

func (s *Store) IssueContainerHandoff(namespace string) (ContainerHandoff, func() error, error) {
	if err := s.EnsureContainerAPIReachable(); err != nil {
		return ContainerHandoff{}, nil, err
	}
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return ContainerHandoff{}, nil, errors.New("vault daemon is not configured for container handoff")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	token, err := IssueContainerToken(info, controlToken, namespace)
	if err != nil {
		return ContainerHandoff{}, nil, err
	}
	handoff, err := NewContainerHandoff(info, token)
	if err != nil {
		_ = RevokeContainerToken(info, controlToken, token)
		return ContainerHandoff{}, nil, err
	}
	revokeControl := append([]byte(nil), controlToken...)
	revoke := func() error {
		defer clear(revokeControl)
		return RevokeContainerToken(info, revokeControl, token)
	}
	return handoff, revoke, nil
}

func (s *Store) RevokeContainerHandoffs(namespace string) error {
	if err := validateInstanceNamespace(namespace); err != nil {
		return err
	}
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("vault daemon is not configured for container handoff")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	return controlRequest(info, string(controlToken), http.MethodDelete, "/tokens/instances/"+namespace, nil, nil)
}

func (s *Store) BindContainerHandoff(containerID, token string) error {
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("vault daemon is not configured for container handoff")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	return BindContainerToken(info, controlToken, containerID, token)
}

func (s *Store) RevokeContainerHandoff(namespace, containerID string) error {
	if err := validateInstanceNamespace(namespace); err != nil {
		return err
	}
	s.mu.Lock()
	if s.daemonInfo == nil || len(s.controlToken) == 0 {
		s.mu.Unlock()
		return errors.New("vault daemon is not configured for container handoff")
	}
	info := *s.daemonInfo
	controlToken := append([]byte(nil), s.controlToken...)
	s.mu.Unlock()
	defer clear(controlToken)
	return RevokeContainerTokens(info, controlToken, namespace, containerID)
}

func RunDetached(parent context.Context, in io.Reader) error {
	var config daemonBootstrap
	decoder := json.NewDecoder(io.LimitReader(in, 1<<20))
	if err := decoder.Decode(&config); err != nil {
		return fmt.Errorf("read vault daemon bootstrap: %w", err)
	}
	password := config.Password
	config.Password = nil
	defer clear(password)
	controlToken := config.ControlToken
	config.ControlToken = nil
	defer clear(controlToken)
	if config.StateRoot == "" || len(controlToken) != 43 {
		return errors.New("vault daemon bootstrap is incomplete")
	}
	store, err := Open(filepath.Join(config.StateRoot, "credentials.kdbx"), password)
	if err != nil {
		return err
	}
	api := NewAPI(store, nil)
	if err := api.LoadTokens(); err != nil {
		return err
	}
	bindAddress := config.APIListenAddress
	if bindAddress == "" {
		bindAddress, err = vaultAPIListenAddress()
		if err != nil {
			return err
		}
	}
	apiListener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return err
	}
	certificate, publicCert, err := loadOrCreateDaemonCertificate(store)
	if err != nil {
		_ = apiListener.Close()
		return err
	}
	controlListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = apiListener.Close()
		return err
	}
	bindIP, _, err := net.SplitHostPort(bindAddress)
	if err != nil {
		_ = apiListener.Close()
		_ = controlListener.Close()
		return err
	}
	bindIP = strings.Trim(bindIP, "[]")
	info := DaemonInfo{
		APIAddress:     "https://host.docker.internal:" + portOf(apiListener.Addr()),
		APIListenIP:    bindIP,
		APICertificate: publicCert,
		ControlAddress: "http://" + controlListener.Addr().String(),
	}
	if err := writeDaemonInfo(filepath.Join(config.StateRoot, daemonInfoFilename), info); err != nil {
		_ = apiListener.Close()
		_ = controlListener.Close()
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	apiServer := &http.Server{Handler: api, ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 16)
	network := &apiListenerManager{server: apiServer, certificate: certificate, info: info, infoPath: filepath.Join(config.StateRoot, daemonInfoFilename), listeners: map[string]net.Listener{bindIP: apiListener}, serveErr: serveErr}
	controlServer := &http.Server{Handler: controlHandler(api, string(controlToken), cancel, network), ReadHeaderTimeout: 5 * time.Second}
	serveAPILisener(apiServer, apiListener, certificate, serveErr)
	go func() { serveErr <- controlServer.Serve(controlListener) }()
	select {
	case <-ctx.Done():
	case serveFailure := <-serveErr:
		if !errors.Is(serveFailure, http.ErrServerClosed) && !errors.Is(serveFailure, net.ErrClosed) {
			cancel()
			_ = os.Remove(filepath.Join(config.StateRoot, daemonInfoFilename))
			return serveFailure
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = apiServer.Shutdown(shutdownCtx)
	_ = controlServer.Shutdown(shutdownCtx)
	_ = os.Remove(filepath.Join(config.StateRoot, daemonInfoFilename))
	return nil
}

type apiListenerManager struct {
	mu          sync.Mutex
	server      *http.Server
	certificate tls.Certificate
	info        DaemonInfo
	infoPath    string
	listeners   map[string]net.Listener
	serveErr    chan<- error
	listen      func(string, string) (net.Listener, error)
}

func (m *apiListenerManager) ensureContainerReachable(address string) (DaemonInfo, error) {
	requestedHost, requestedPort, err := net.SplitHostPort(address)
	if err != nil {
		return DaemonInfo{}, err
	}
	if requestedPort != "0" {
		return DaemonInfo{}, errors.New("automatic vault listener must use an ephemeral port")
	}
	requestedIP := net.ParseIP(strings.Trim(requestedHost, "[]"))
	if requestedIP == nil || requestedIP.IsMulticast() {
		return DaemonInfo{}, errors.New("automatic vault listener address is invalid")
	}
	if runtime.GOOS == "linux" && requestedIP.IsLoopback() {
		desktop, available, err := dockerDesktopEngine()
		if err != nil {
			return DaemonInfo{}, err
		}
		if !desktop || !available {
			return DaemonInfo{}, errors.New("automatic vault handoff needs the local Docker Engine running so AACT can bind its default bridge gateway")
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	currentIP := net.ParseIP(m.info.APIListenIP)
	if currentIP == nil {
		return DaemonInfo{}, errors.New("existing vault API bind address is missing from daemon metadata; restart the vault daemon")
	}
	if currentIP.IsUnspecified() && !requestedIP.IsUnspecified() {
		return DaemonInfo{}, errors.New("vault API bind IP is broader than the requested container route; restart the vault daemon to apply the narrower bind")
	}
	if currentIP.Equal(requestedIP) {
		if !strings.Contains(m.info.APIAddress, "host.docker.internal") {
			m.info.APIAddress = "https://host.docker.internal:" + mustPort(m.info.APIAddress)
			if err := writeDaemonInfo(m.infoPath, m.info); err != nil {
				return DaemonInfo{}, err
			}
		}
		return m.info, nil
	}
	port := mustPort(m.info.APIAddress)
	listenAddress := net.JoinHostPort(requestedIP.String(), port)
	listen := m.listen
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", listenAddress)
	if err != nil {
		return DaemonInfo{}, fmt.Errorf("bind vault API to Docker host gateway %s: %w", requestedIP, err)
	}
	updated := m.info
	updated.APIAddress = "https://host.docker.internal:" + port
	updated.APIListenIP = requestedIP.String()
	if err := writeDaemonInfo(m.infoPath, updated); err != nil {
		_ = listener.Close()
		return DaemonInfo{}, err
	}
	m.info = updated
	oldListeners := m.listeners
	m.listeners = map[string]net.Listener{requestedIP.String(): listener}
	serveAPILisener(m.server, listener, m.certificate, m.serveErr)
	for oldIP, oldListener := range oldListeners {
		if oldIP != requestedIP.String() {
			_ = oldListener.Close()
		}
	}
	return updated, nil
}

func mustPort(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || !validPort(parsed.Port()) {
		return ""
	}
	return parsed.Port()
}

func serveAPILisener(server *http.Server, listener net.Listener, certificate tls.Certificate, errorsOut chan<- error) {
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	go func() {
		if err := server.Serve(tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			select {
			case errorsOut <- err:
			default:
			}
		}
	}()
}

// vaultAPIListenAddress binds Docker Desktop clients through its host bridge to
// loopback. On Linux Engine it binds only the local default-bridge gateway.
// An explicit AACT_VAULT_API_BIND_IP always takes precedence.
func vaultAPIListenAddress() (string, error) {
	address := strings.TrimSpace(os.Getenv("AACT_VAULT_API_BIND_IP"))
	explicit := address != ""
	if address == "" {
		var gateway net.IP
		var dockerActive, dockerDesktop bool
		var err error
		if runtime.GOOS == "linux" {
			dockerDesktop, dockerActive, err = dockerDesktopEngine()
			if err != nil {
				return "", err
			}
			if dockerActive && !dockerDesktop {
				var available bool
				gateway, available, err = dockerBridgeGatewayIP()
				if err != nil {
					return "", err
				}
				dockerActive = available
			}
		} else {
			_, _, err := dockerDesktopEngine()
			if err != nil {
				return "", err
			}
		}
		address = defaultVaultAPIListenIP(runtime.GOOS, dockerDesktop, dockerActive, gateway).String()
	}
	ip := net.ParseIP(address)
	if ip == nil || ip.IsMulticast() || !explicit && ip.IsUnspecified() {
		return "", errors.New("AACT_VAULT_API_BIND_IP must be a unicast IP address")
	}
	return net.JoinHostPort(ip.String(), "0"), nil
}

func defaultVaultAPIListenIP(goos string, dockerDesktop, dockerActive bool, gateway net.IP) net.IP {
	if goos == "linux" && dockerActive && !dockerDesktop && gateway != nil {
		return append(net.IP(nil), gateway...)
	}
	return net.ParseIP("127.0.0.1")
}

func dockerDesktopEngine() (desktop, available bool, err error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return false, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.OperatingSystem}}").Output()
	if err != nil {
		return false, false, nil
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	endpointOutput, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return false, true, fmt.Errorf("inspect active Docker context before opening the vault route: %w", err)
	}
	endpoint := strings.TrimSpace(string(endpointOutput))
	if !localDockerEndpoint(endpoint) {
		return false, true, fmt.Errorf("active Docker endpoint %q is remote; automatic vault handoff requires a local Docker engine", endpoint)
	}
	return strings.Contains(strings.ToLower(string(output)), "docker desktop"), true, nil
}

func localDockerEndpoint(endpoint string) bool {
	if strings.HasPrefix(endpoint, "unix://") || strings.HasPrefix(endpoint, "npipe://") {
		return true
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "tcp" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	if address == nil {
		return false
	}
	if address.IsLoopback() {
		return true
	}
	local, err := isLocalInterfaceIP(address)
	return err == nil && local
}

func dockerBridgeGatewayIP() (net.IP, bool, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		return nil, false, nil
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}").Output()
	if err != nil {
		return nil, true, fmt.Errorf("Docker Engine is running but its default bridge gateway could not be discovered: %w", err)
	}
	address := net.ParseIP(strings.TrimSpace(string(output)))
	if address == nil || address.IsLoopback() || address.IsMulticast() || address.IsUnspecified() {
		return nil, true, errors.New("Docker Engine returned an invalid default bridge gateway; automatic vault handoff cannot bind safely")
	}
	local, err := isLocalInterfaceIP(address)
	if err != nil {
		return nil, true, err
	}
	if !local {
		return nil, true, fmt.Errorf("Docker bridge gateway %s is not assigned to this host; remote Docker contexts cannot use local vault handoff automatically", address)
	}
	return address, true, nil
}

func isLocalInterfaceIP(address net.IP) (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, candidate := range addresses {
			var ip net.IP
			switch value := candidate.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip != nil && ip.Equal(address) {
				return true, nil
			}
		}
	}
	return false, nil
}

func controlHandler(api *API, token string, stop context.CancelFunc, network *apiListenerManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare([]byte(token), []byte(strings.TrimPrefix(header, "Bearer "))) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/container-network" && r.Method == http.MethodPost:
			var request struct {
				APIListenAddress string `json:"api_listen_address"`
			}
			if err := decodeControlJSON(w, r, &request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			info, err := network.ensureContainerReachable(request.APIListenAddress)
			if err != nil {
				http.Error(w, "cannot prepare automatic container route: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
			writeJSON(w, http.StatusOK, info)
		case r.URL.Path == "/tokens" && r.Method == http.MethodPost:
			var request struct {
				Namespace string `json:"namespace"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			issued, err := api.IssueToken(request.Namespace)
			if err != nil {
				http.Error(w, "cannot issue token", http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]string{"token": issued})
		case strings.HasPrefix(r.URL.Path, "/tokens/instances/") && r.Method == http.MethodDelete:
			namespace := strings.TrimPrefix(r.URL.Path, "/tokens/instances/")
			if err := api.RevokeTokensForInstance(namespace); err != nil {
				http.Error(w, "cannot revoke instance tokens", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/tokens/containers/") && r.Method == http.MethodDelete:
			containerID := strings.TrimPrefix(r.URL.Path, "/tokens/containers/")
			var request struct {
				Namespace string `json:"namespace"`
			}
			if err := decodeControlJSON(w, r, &request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if err := api.RevokeTokensForContainer(request.Namespace, containerID); err != nil {
				http.Error(w, "cannot revoke container tokens", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/tokens/") && strings.HasSuffix(r.URL.Path, "/container") && r.Method == http.MethodPut:
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/tokens/"), "/")
			if len(parts) != 2 || parts[1] != "container" {
				http.NotFound(w, r)
				return
			}
			var request struct {
				ContainerID string `json:"container_id"`
			}
			if err := decodeControlJSON(w, r, &request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if err := api.BindTokenContainer(parts[0], request.ContainerID); err != nil {
				http.Error(w, "cannot bind token to container", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/tokens/") && r.Method == http.MethodDelete:
			if err := api.RevokeToken(strings.TrimPrefix(r.URL.Path, "/tokens/")); err != nil {
				http.Error(w, "token not found", http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/shutdown" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			go stop()
		default:
			http.NotFound(w, r)
		}
	})
}

func decodeControlJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing control request data")
	}
	return nil
}

func daemonHealthy(info DaemonInfo) bool { return daemonHealthError(info) == nil }

func daemonHealthError(info DaemonInfo) error {
	if err := validateDaemonInfo(info); err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, info.ControlAddress+"/health", nil)
	if err != nil {
		return err
	}
	response, err := daemonHealthHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func waitForDetachedDaemon(infoPath, logPath string, timeout time.Duration) (DaemonInfo, error) {
	if timeout <= 0 {
		timeout = detachedDaemonStartupTimeout
	}
	deadline := time.Now().Add(timeout)
	lastError := errors.New("daemon metadata is not available")
	for {
		info, err := readDaemonInfo(infoPath)
		if err != nil {
			lastError = fmt.Errorf("read daemon metadata: %w", err)
		} else if err := daemonHealthError(info); err != nil {
			lastError = fmt.Errorf("daemon health check: %w", err)
		} else {
			return info, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		delay := detachedDaemonPollInterval
		if remaining < delay {
			delay = remaining
		}
		time.Sleep(delay)
	}
	logText, err := readDetachedDaemonLogTail(logPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logText = fmt.Sprintf("could not read daemon log: %v", err)
	} else if strings.TrimSpace(logText) == "" {
		logText = "daemon log is empty"
	}
	return DaemonInfo{}, fmt.Errorf("background vault daemon did not become ready within %s (%v): %s", timeout, lastError, logText)
}

func readDetachedDaemonLogTail(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	truncated := info.Size() > detachedDaemonLogTailBytes
	if truncated {
		if _, err := file.Seek(-detachedDaemonLogTailBytes, io.SeekEnd); err != nil {
			return "", err
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, detachedDaemonLogTailBytes))
	if err != nil {
		return "", err
	}
	if truncated {
		return "[earlier log lines truncated]\n" + string(data), nil
	}
	return string(data), nil
}

// NewContainerHTTPClient pins the daemon's public certificate for API calls
// made by an authenticated container client.
func NewContainerHTTPClient(info DaemonInfo) (*http.Client, error) {
	if err := validateDaemonInfo(info); err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(info.APICertificate))
	if block == nil {
		return nil, errors.New("vault daemon certificate is missing")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse vault daemon certificate: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	apiURL, _ := url.Parse(info.APIAddress)
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: apiURL.Hostname(), MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func loadOrCreateDaemonCertificate(store *Store) (tls.Certificate, string, error) {
	values, err := store.List(daemonSecretNamespace)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	var certPEM, keyPEM []byte
	if pair, ok := values[daemonTLSKeyPairSecretName]; ok {
		var stored struct {
			Certificate []byte `json:"certificate"`
			PrivateKey  []byte `json:"private_key"`
		}
		if err := json.Unmarshal(pair, &stored); err != nil || len(stored.Certificate) == 0 || len(stored.PrivateKey) == 0 {
			return tls.Certificate{}, "", errors.New("stored vault TLS key pair is invalid")
		}
		certPEM, keyPEM = stored.Certificate, stored.PrivateKey
	} else if legacyCert, certOK := values[daemonTLSCertSecretName]; certOK {
		legacyKey, keyOK := values[daemonTLSKeySecretName]
		if !keyOK {
			// Older versions used two records. A partial pair can only have been
			// produced by an interrupted write; discard it and create a fresh pair.
			if err := store.Delete(daemonSecretNamespace, daemonTLSCertSecretName); err != nil {
				return tls.Certificate{}, "", err
			}
		} else {
			certPEM, keyPEM = legacyCert, legacyKey
		}
	} else if legacyKey, keyOK := values[daemonTLSKeySecretName]; keyOK {
		if err := store.Delete(daemonSecretNamespace, daemonTLSKeySecretName); err != nil {
			return tls.Certificate{}, "", err
		}
		_ = legacyKey
	}
	if len(certPEM) > 0 {
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return tls.Certificate{}, "", fmt.Errorf("load stored vault TLS key pair: %w", err)
		}
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) || leaf.VerifyHostname("host.docker.internal") != nil {
			return tls.Certificate{}, "", errors.New("stored vault TLS certificate is expired or has the wrong host; rotate after stopping containers")
		}
		return certificate, string(certPEM), nil
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "AACT Vault"}, DNSNames: []string{"host.docker.internal", "localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	encodedCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	pair, err := json.Marshal(struct {
		Certificate []byte `json:"certificate"`
		PrivateKey  []byte `json:"private_key"`
	}{encodedCert, keyBytes})
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := store.Put(daemonSecretNamespace, daemonTLSKeyPairSecretName, pair); err != nil {
		return tls.Certificate{}, "", err
	}
	certificate, err := tls.X509KeyPair(encodedCert, keyBytes)
	return certificate, string(encodedCert), err
}

func controlRequest(info DaemonInfo, controlToken, method, path string, body any, result any) error {
	if err := validateDaemonInfo(info); err != nil {
		return err
	}
	if len(controlToken) != 43 {
		return errors.New("vault control token is invalid")
	}
	var encoded io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		encoded = strings.NewReader(string(raw))
	}
	request, err := http.NewRequest(method, info.ControlAddress+path, encoded)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+controlToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := controlHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%w: %s", &controlStatusError{statusCode: response.StatusCode}, strings.TrimSpace(string(message)))
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
	}
	return nil
}

func readDaemonInfo(path string) (DaemonInfo, error) {
	var info DaemonInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	if err := validateDaemonInfo(info); err != nil {
		return DaemonInfo{}, err
	}
	return info, nil
}

var controlHTTPClient = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
var daemonHealthHTTPClient = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func validateDaemonInfo(info DaemonInfo) error {
	api, err := url.Parse(info.APIAddress)
	if err != nil || api.Scheme != "https" || (api.Hostname() != "host.docker.internal" && api.Hostname() != "localhost") || api.User != nil || api.Path != "" || api.RawQuery != "" || api.Fragment != "" || !validPort(api.Port()) {
		return errors.New("invalid vault API address in daemon metadata")
	}
	control, err := url.Parse(info.ControlAddress)
	if err != nil || control.Scheme != "http" || control.User != nil || control.Path != "" || control.RawQuery != "" || control.Fragment != "" || !validPort(control.Port()) {
		return errors.New("invalid vault control address in daemon metadata")
	}
	ip := net.ParseIP(control.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("vault control address must use loopback")
	}
	if info.APICertificate == "" {
		return errors.New("vault daemon certificate is missing")
	}
	return nil
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}

func writeDaemonInfo(path string, info DaemonInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vault-daemon-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}

func portOf(addr net.Addr) string {
	_, port, _ := net.SplitHostPort(addr.String())
	return port
}
