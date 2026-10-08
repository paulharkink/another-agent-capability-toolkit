package app

import (
	"context"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

// Fixture helpers create explicit local ProfileRefs and translate fixture
// agent scopes. Operations below always call the production profile API.
func profileRefForFixture(s *Service, pack, capability, profile string) config.ProfileRef {
	if pack == "" {
		pack = s.Source.ID
	}
	// Environment and target names are not profile selectors. Tests that need
	// a named profile pass it explicitly through Ref.
	if profile == "" {
		profile = "default"
	}
	ref := config.ProfileRef{PackID: pack, CapabilityID: capability, Name: profile}
	if pack == s.Source.ID {
		ensureProfileForTest(s, ref)
	}
	return ref
}

func profileDestinationsForTest(s *Service, envs []agents.Environment) []string {
	ids := make([]string, 0, len(envs))
	for _, env := range envs {
		id := env.Kind
		if id == "" {
			id = env.ID
		}
		ids = append(ids, id)
		if env.ID != "" && (env.Home != "" || env.ConfigPath != "") {
			if s.Options.AgentScopes == nil {
				s.Options.AgentScopes = map[string]agents.Scope{}
			}
			scope := s.Options.AgentScopes[id]
			scope.ID, scope.Home, scope.ConfigPathOverride = id, env.Home, env.ConfigPath
			s.Options.AgentScopes[id] = scope
		}
	}
	return ids
}

func (s *Service) applyProfileFixture(ctx context.Context, q InstallRequest) (Result, error) {
	ref := q.Ref
	if ref.CapabilityID == "" {
		ref = profileRefForFixture(s, s.Source.ID, q.Package, "")
	}
	urls := q.ExternalURLs
	if q.ExternalURL != "" {
		if urls == nil {
			urls = map[string]string{}
		}
		if p, err := s.packageByID(q.Package); err == nil {
			defs := p.MCPDefinitions()
			if len(defs) == 1 {
				urls[defs[0].Name] = q.ExternalURL
			}
		}
	}
	result, err := s.ApplyProfile(ctx, ProfileRequest{Ref: ref, Inputs: q.Inputs, ResetInputs: q.ResetInputs, DestinationIDs: profileDestinationsForTest(s, q.Agents), SkillsOnly: q.SkillsOnly, Interactive: q.Interactive, ExternalURLs: urls})
	return Result{Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, Message: result.Message, Step: result.Step, Target: result.Target}, err
}

func (s *Service) removeProfileFixture(ctx context.Context, q InstallRequest) (Result, error) {
	ref := q.Ref
	if ref.CapabilityID == "" {
		ref = profileRefForFixture(s, s.Source.ID, q.Package, "")
	}
	profileDestinationsForTest(s, q.Agents)
	key, keyErr := s.Store.ResolveProfileKey(ref.PackID, ref.CapabilityID, ref.Name)
	if keyErr != nil {
		return Result{}, keyErr
	}
	remaining := []string{}
	if records, e := s.Store.Profiles(); e == nil {
		for _, record := range records {
			if record.Key == key && record.Selection != nil {
				for _, id := range record.Selection.DestinationIDs {
					removed := false
					for _, target := range q.Agents {
						targetID := target.Kind
						if targetID == "" {
							targetID = target.ID
						}
						if targetID == id {
							removed = true
							break
						}
					}
					if !removed {
						remaining = append(remaining, id)
					}
				}
			}
		}
	}
	items := []string(nil)
	if len(remaining) == 0 {
		items = []string{}
	}
	result, err := s.ApplyProfile(ctx, ProfileRequest{Ref: ref, ItemIDs: items, DestinationIDs: remaining})
	return Result{Changes: result.Changes, Errors: result.Errors, Saved: result.Saved, Message: result.Message, Step: result.Step, Target: result.Target}, err
}

func (s *Service) previewProfileFixture(ctx context.Context, q viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	ref := q.Ref
	if ref.CapabilityID == "" {
		ref = profileRefForFixture(s, q.SourceID, q.PackageID, "")
	}
	return s.PreviewProfile(ctx, ProfileRequest{Ref: ref})
}

func (s *Service) applyProfileFixtureUI(ctx context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	ref := q.Ref
	if ref.CapabilityID == "" {
		ref = profileRefForFixture(s, q.SourceID, q.PackageID, "")
	}
	urls := q.ExternalURLs
	if q.ExternalURL != "" {
		if urls == nil {
			urls = map[string]string{}
		}
		if p, err := s.packageByID(q.PackageID); err == nil {
			defs := p.MCPDefinitions()
			if len(defs) == 1 {
				urls[defs[0].Name] = q.ExternalURL
			}
		}
	}
	return s.UIInstall(ctx, viewmodel.SetupInstallRequest{Ref: ref, ItemIDs: q.ItemIDs, Inputs: q.Inputs, ResetInputs: q.ResetInputs, DestinationIDs: q.DestinationIDs, ExternalURLs: urls})
}
