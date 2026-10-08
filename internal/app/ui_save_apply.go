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
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !ordinaryCancellation(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return ordinaryCancellation(wrapped.Unwrap())
	}
	return errors.Is(err, picker.ErrCancelled) || errors.Is(err, context.Canceled)
}

func cancellationError(resultErrors []string, err error) error {
	if !errors.Is(err, picker.ErrCancelled) && !errors.Is(err, context.Canceled) {
		return err
	}
	if ordinaryCancellation(err) && len(resultErrors) == 0 {
		return picker.ErrCancelled
	}
	if ordinaryCancellation(err) {
		return errors.Join(errors.New(strings.Join(resultErrors, "; ")), picker.ErrCancelled)
	}
	return errors.Join(err, picker.ErrCancelled)
}

// credentialObservation reports the presence of package-managed material only.
// It deliberately does not interpret configured source paths as evidence that
// credentials were imported or that authentication currently works.
func (s *Service) credentialObservation(p catalog.Package, k state.Key) (string, string) {
	if p.MCP == nil || len(p.MCP.CredentialFiles) == 0 {
		return "unknown", "This capability does not declare managed credential files."
	}
	var paths []string
	for _, name := range p.MCP.CredentialFiles {
		path := filepath.Join(s.Store.AuthDir(k), name)
		rel, err := filepath.Rel(s.Store.AuthDir(k), path)
		if err != nil || filepath.IsAbs(name) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "unknown", fmt.Sprintf("Invalid managed credential path %q.", name)
		}
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
		paths = append(paths, path)
	}
	return "present", fmt.Sprintf("Managed credential material exists at %s; authentication was not checked.", strings.Join(paths, ", "))
}
