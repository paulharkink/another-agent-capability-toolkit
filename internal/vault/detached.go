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
	"strconv"
	"strings"
	"time"
)

const daemonInfoFilename = "vault-daemon.json"

type DaemonInfo struct {
	APIAddress     string `json:"api_address"`
	APIListenIP    string `json:"api_listen_ip,omitempty"`
	APICertificate string `json:"api_certificate"`
	ControlAddress string `json:"control_address"`
}

type daemonBootstrap struct {
	StateRoot    string `json:"state_root"`
	Password     []byte `json:"password"`
	ControlToken []byte `json:"control_token"`
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
			bindAddress, bindErr := vaultAPIListenAddress()
			if bindErr != nil {
				return DaemonInfo{}, bindErr
			}
			expectedHost := "localhost"
			bindHost, _, splitErr := net.SplitHostPort(bindAddress)
			if splitErr != nil {
				return DaemonInfo{}, splitErr
			}
			bindIP := net.ParseIP(strings.Trim(bindHost, "[]"))
			if bindIP != nil && !bindIP.IsLoopback() {
				expectedHost = "host.docker.internal"
			}
			currentURL, _ := url.Parse(info.APIAddress)
			requestedIP := net.ParseIP(strings.Trim(bindHost, "[]")).String()
			existingIP := info.APIListenIP
			if existingIP == "" {
				if currentURL.Hostname() == "localhost" {
					existingIP = net.IPv4(127, 0, 0, 1).String()
				} else {
					existingIP = net.IPv4zero.String()
				}
			}
			if currentURL.Hostname() != expectedHost || net.ParseIP(existingIP) == nil || net.ParseIP(existingIP).String() != requestedIP {
				return DaemonInfo{}, fmt.Errorf("vault daemon is running with API bind IP %s (requested %s); run `aact vault stop`, then restart with the desired AACT_VAULT_API_BIND_IP", existingIP, requestedIP)
			}
			return info, nil
		}
		_ = os.Remove(infoPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return DaemonInfo{}, err
	}
	bootstrap := daemonBootstrap{StateRoot: root, Password: append([]byte(nil), password...), ControlToken: append([]byte(nil), controlToken...)}
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
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := readDaemonInfo(infoPath); err == nil && daemonHealthy(info) {
			_ = cmd.Process.Release()
			return info, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	log, _ := os.ReadFile(filepath.Join(root, "vault-daemon.log"))
	return DaemonInfo{}, fmt.Errorf("background vault daemon did not become ready: %s", strings.TrimSpace(string(log)))
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

func (s *Store) IssueContainerHandoff(namespace string) (ContainerHandoff, func() error, error) {
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
	bindAddress, err := vaultAPIListenAddress()
	if err != nil {
		return err
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
	bindHost, _, err := net.SplitHostPort(bindAddress)
	if err != nil {
		_ = apiListener.Close()
		_ = controlListener.Close()
		return err
	}
	apiHost := "localhost"
	if ip := apiListener.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
		apiHost = "host.docker.internal"
	}
	info := DaemonInfo{
		APIAddress:     "https://" + apiHost + ":" + portOf(apiListener.Addr()),
		APIListenIP:    strings.Trim(bindHost, "[]"),
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
	controlServer := &http.Server{Handler: controlHandler(api, string(controlToken), cancel), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 2)
	apiTLSListener := tls.NewListener(apiListener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	go func() { serveErr <- apiServer.Serve(apiTLSListener) }()
	go func() { serveErr <- controlServer.Serve(controlListener) }()
	select {
	case <-ctx.Done():
	case serveFailure := <-serveErr:
		if !errors.Is(serveFailure, http.ErrServerClosed) {
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

// vaultAPIListenAddress uses loopback by default. Container handoff users can
// explicitly opt into a host-reachable interface with AACT_VAULT_API_BIND_IP.
func vaultAPIListenAddress() (string, error) {
	address := strings.TrimSpace(os.Getenv("AACT_VAULT_API_BIND_IP"))
	if address == "" {
		address = "127.0.0.1"
	}
	ip := net.ParseIP(address)
	if ip == nil || ip.IsMulticast() {
		return "", errors.New("AACT_VAULT_API_BIND_IP must be a unicast IP address")
	}
	return net.JoinHostPort(ip.String(), "0"), nil
}

func controlHandler(api *API, token string, stop context.CancelFunc) http.Handler {
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

func daemonHealthy(info DaemonInfo) bool {
	if validateDaemonInfo(info) != nil {
		return false
	}
	request, err := http.NewRequest(http.MethodGet, info.ControlAddress+"/health", nil)
	if err != nil {
		return false
	}
	response, err := controlHTTPClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusNoContent
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
		return fmt.Errorf("vault daemon control request failed with HTTP %d", response.StatusCode)
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

var controlHTTPClient = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

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
