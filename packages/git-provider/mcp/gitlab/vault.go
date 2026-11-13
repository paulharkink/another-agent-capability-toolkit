package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const vaultHandoffPath = "/run/aact/vault.json"
const vaultEnvPrefix = "AACT_VAULT_SECRET_INPUT_"

type vaultHandoff struct {
	APIAddress  string `json:"api_address"`
	Certificate string `json:"certificate"`
	Token       string `json:"token"`
}

// loadVaultSecrets resolves only declared input names through the instance-scoped
// token delivered by AACT. Provider values never enter Docker environment config.
func loadVaultSecrets(environ []string, handoffPath string) (map[string]string, error) {
	requested := map[string]string{}
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(name, vaultEnvPrefix) && value != "" {
			target := strings.TrimPrefix(name, vaultEnvPrefix)
			requested[target] = value
		}
	}
	if len(requested) == 0 {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		return nil, errors.New("vault handoff is unavailable")
	}
	var handoff vaultHandoff
	if err := json.Unmarshal(data, &handoff); err != nil || handoff.APIAddress == "" || handoff.Token == "" || handoff.Certificate == "" {
		return nil, errors.New("vault handoff is invalid")
	}
	block, _ := pemDecode([]byte(handoff.Certificate))
	if block == nil {
		return nil, errors.New("vault certificate is invalid")
	}
	roots := x509.NewCertPool()
	cert, err := x509.ParseCertificate(block)
	if err != nil {
		return nil, errors.New("vault certificate is invalid")
	}
	roots.AddCert(cert)
	apiURL, err := url.Parse(handoff.APIAddress)
	if err != nil || apiURL.Scheme != "https" || apiURL.Hostname() == "" {
		return nil, errors.New("vault address is invalid")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: apiURL.Hostname(), MinVersion: tls.VersionTLS12}}}
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(handoff.APIAddress, "/")+"/v1/secrets/aact-private-inputs-v1", nil)
	if err != nil {
		return nil, errors.New("request vault private inputs")
	}
	request.Header.Set("Authorization", "Bearer "+handoff.Token)
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("vault request failed")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		return nil, errors.New("vault private inputs are unavailable")
	}
	var envelope struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("vault private inputs are invalid")
	}
	var record struct {
		Version int            `json:"version"`
		Values  map[string]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(envelope.Value), &record); err != nil || record.Version != 1 {
		return nil, errors.New("vault private inputs are invalid")
	}
	secrets := make(map[string]string, len(requested))
	for target, input := range requested {
		value, ok := record.Values[input].(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("vault input %q is empty or unavailable", input)
		}
		secrets[target] = value
	}
	return secrets, nil
}

func pemDecode(data []byte) ([]byte, []byte) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, rest
	}
	return block.Bytes, rest
}

func waitForVaultSecrets(environ []string, handoffPath string, timeout time.Duration) (map[string]string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Stat(handoffPath)
		if err == nil {
			return loadVaultSecrets(environ, handoffPath)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("vault handoff could not be read")
		}
		if time.Now().After(deadline) {
			return nil, errors.New("vault handoff was not delivered before startup timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
