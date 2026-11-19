package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

const (
	InfoFilename       = "aact-daemon.json"
	LogFilename        = "aact-daemon.log"
	LegacyInfoFilename = "vault-daemon.json"
	LegacyLogFilename  = "vault-daemon.log"
)

// Info describes the AACT daemon's container-facing API and local control endpoint.
// The API currently hosts the vault service at the root path.
type Info struct {
	APIAddress     string   `json:"api_address"`
	APIListenIP    string   `json:"api_listen_ip,omitempty"`
	APICertificate string   `json:"api_certificate"`
	ControlAddress string   `json:"control_address"`
	Services       []string `json:"services,omitempty"`
}

func ReadInfo(path string) (Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, err
	}
	if err := ValidateInfo(info); err != nil {
		return Info{}, err
	}
	return info, nil
}

func ReadStateInfo(stateRoot string) (Info, error) {
	info, err := ReadInfo(filepath.Join(stateRoot, InfoFilename))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return info, err
	}
	return ReadInfo(filepath.Join(stateRoot, LegacyInfoFilename))
}

func WriteInfo(path string, info Info) error {
	if err := ValidateInfo(info); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aact-daemon-*.tmp")
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
	return replaceInfoFile(name, path)
}

func ValidateInfo(info Info) error {
	api, err := url.Parse(info.APIAddress)
	if err != nil || api.Scheme != "https" || api.User != nil || api.Path != "" || api.RawQuery != "" || api.Fragment != "" || !validPort(api.Port()) {
		return errors.New("invalid API address in daemon metadata")
	}
	control, err := url.Parse(info.ControlAddress)
	if err != nil || control.Scheme != "http" || control.User != nil || control.Path != "" || control.RawQuery != "" || control.Fragment != "" || !validPort(control.Port()) {
		return errors.New("invalid daemon control address in metadata")
	}
	ip := net.ParseIP(control.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("daemon control address must use loopback")
	}
	if info.APICertificate == "" {
		return errors.New("daemon API certificate is missing")
	}
	return nil
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}
