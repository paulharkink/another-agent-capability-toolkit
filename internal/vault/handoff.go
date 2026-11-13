package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

const (
	ContainerHandoffPath  = "/run/aact/vault.json"
	ContainerHandoffTmpfs = "/run/aact:rw,noexec,nosuid,nodev,size=16k,mode=1777"
)

// ContainerHandoff is sent only over the container process stdin. Token is
// written inside the container to a private tmpfs file by the receiver helper.
type ContainerHandoff struct {
	APIAddress  string `json:"api_address"`
	Certificate string `json:"certificate"`
	Token       string `json:"token"`
}

type ContainerHandoffIssuer interface {
	IssueContainerHandoff(namespace string) (ContainerHandoff, func() error, error)
	BindContainerHandoff(containerID, token string) error
	RevokeContainerHandoff(namespace, containerID string) error
	RevokeContainerHandoffs(namespace string) error
}

// ScopedContainerHandoffIssuer issues tokens restricted to read-only access to
// the named private input values in one instance namespace.
type ScopedContainerHandoffIssuer interface {
	IssueContainerHandoffForPrivateInputs(namespace string, names []string) (ContainerHandoff, func() error, error)
}

// ContainerHandoffContainerRevoker revokes tokens by exact owner ID when the
// container has disappeared and its namespace label can no longer be read.
type ContainerHandoffContainerRevoker interface {
	RevokeContainerHandoffByContainer(containerID string) error
}

func NewContainerHandoff(info DaemonInfo, token string) (ContainerHandoff, error) {
	apiURL, err := url.Parse(info.APIAddress)
	if err != nil || apiURL.Hostname() != "host.docker.internal" {
		return ContainerHandoff{}, errors.New("vault daemon is loopback-only; set AACT_VAULT_API_BIND_IP to an interface reachable by containers")
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return ContainerHandoff{}, errors.New("invalid per-container vault token")
	}
	handoff := ContainerHandoff{APIAddress: info.APIAddress, Certificate: info.APICertificate, Token: token}
	if err := validateHandoff(handoff); err != nil {
		return ContainerHandoff{}, err
	}
	return handoff, nil
}

func WriteContainerHandoff(dst io.Writer, handoff ContainerHandoff) error {
	if err := validateHandoff(handoff); err != nil {
		return err
	}
	return json.NewEncoder(dst).Encode(handoff)
}

func ReceiveContainerHandoff(src io.Reader, destination string) error {
	decoder := json.NewDecoder(io.LimitReader(src, 16<<10))
	decoder.DisallowUnknownFields()
	var handoff ContainerHandoff
	if err := decoder.Decode(&handoff); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected data after vault handoff")
	}
	if err := validateHandoff(handoff); err != nil {
		return err
	}
	if destination == "" {
		return errors.New("vault handoff destination is required")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return err
	}
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = os.Remove(destination)
		}
	}()
	if err := json.NewEncoder(file).Encode(handoff); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	removeOnError = false
	return nil
}

func validateHandoff(handoff ContainerHandoff) error {
	parsed, err := url.Parse(handoff.APIAddress)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("vault API address must use HTTPS")
	}
	if handoff.Certificate == "" {
		return errors.New("vault API certificate is required")
	}
	raw, err := base64.RawURLEncoding.DecodeString(handoff.Token)
	if err != nil || len(raw) != 32 {
		return errors.New("vault bearer token must contain 256 bits")
	}
	return nil
}
