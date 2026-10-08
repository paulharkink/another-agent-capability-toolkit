package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// Registration tests that start from persisted keys derive their explicit
// profile reference the same way the profile UI does.
func profileRefFromTestKey(source, capability, name string) config.ProfileRef {
	return config.ProfileRef{PackID: source, CapabilityID: capability, Name: name}
}

func configureRegistrationsTest(ctx context.Context, s *Service, q RegistrationRequest) (Result, error) {
	if q.Ref.Name == "" {
		packID := q.Key.Source
		if packID == "" {
			packID = s.Source.ID
		}
		q.Ref = profileRefFromTestKey(packID, q.Key.Package, q.Key.Target)
	}
	ensureProfileForTest(s, q.Ref)
	return s.ConfigureRegistrations(ctx, q)
}

func configureUIRegistrationsTest(ctx context.Context, s *Service, q viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	if q.Ref.Name == "" {
		packID := q.Key.Source
		if packID == "" {
			packID = s.Source.ID
		}
		q.Ref = profileRefFromTestKey(packID, q.Key.Package, q.Key.Target)
	}
	ensureProfileForTest(s, q.Ref)
	return s.UIConfigureRegistrations(ctx, q)
}

func ensureProfileForTest(s *Service, ref config.ProfileRef) {
	if ref.PackID != s.Source.ID {
		return
	}
	if s.Source.ProfileRoot == "" {
		s.Source.ProfileRoot = filepath.Join(s.Store.Root(), "test-profiles")
	}
	dir := filepath.Join(s.Source.ProfileRoot, ref.CapabilityID)
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, ref.Name+".toml"), []byte(""), 0600)
}

func writeProfileForTest(t *testing.T, s *Service, capability, name, contents string) config.ProfileRef {
	t.Helper()
	ref := profileRefForFixture(s, s.Source.ID, capability, name)
	path := filepath.Join(s.Source.ProfileRoot, capability, name+".toml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return ref
}
