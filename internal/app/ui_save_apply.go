package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// ordinaryCancellation recognizes cancellation only when the returned error
// preserves it structurally. Error-message text is never interpreted as a
// cancellation signal.
func ordinaryCancellation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, picker.ErrCancelled) || errors.Is(err, context.Canceled) {
		return true
	}
	return false
}

func cancellationResult(result Result, err error) (Result, error) {
	if !ordinaryCancellation(err) {
		return result, err
	}
	if len(result.Errors) == 0 {
		return result, picker.ErrCancelled
	}
	return result, errors.Join(errors.New(strings.Join(result.Errors, "; ")), picker.ErrCancelled)
}

// credentialObservation reports the presence of package-managed material only.
// It deliberately does not interpret configured source paths as evidence that
// credentials were imported or that authentication currently works.
func (s *Service) credentialObservation(p catalog.Package, k state.Key) (string, string) {
	name := ""
	switch p.ID {
	case "cluster-inspector":
		name = "kubeconfig"
	case "grafana-inspector":
		name = "auth.json"
	default:
		return "unknown", "This package does not expose an observable managed credential file."
	}
	path := filepath.Join(s.Store.AuthDir(k), name)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "missing", fmt.Sprintf("Managed credential material is missing at %s.", path)
	}
	if err != nil || !info.Mode().IsRegular() {
		return "unknown", fmt.Sprintf("Managed credential material at %s could not be observed.", path)
	}
	if info.Size() == 0 {
		return "missing", fmt.Sprintf("Managed credential material is empty at %s.", path)
	}
	return "present", fmt.Sprintf("Managed credential material exists at %s; authentication was not checked.", path)
}
