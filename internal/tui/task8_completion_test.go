package tui

import (
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestTask8CompletionCountsOnlySelectedEligibleWork(t *testing.T) {
	tests := []struct {
		name       string
		selected   []viewmodel.SelectedComponent
		eligible   []string
		observed   []viewmodel.ComponentStatus
		wantStatus string
	}{
		{
			name:     "unselected optional MCP does not dilute installed skill",
			selected: []viewmodel.SelectedComponent{{Kind: "skill", Name: "guide"}},
			eligible: []string{"codex"},
			observed: []viewmodel.ComponentStatus{
				{AgentID: "codex", Kind: "skill", Name: "guide", Status: "installed"},
				{AgentID: "codex", Kind: "mcp", Name: "optional-server", Status: "absent"},
			},
			wantStatus: "Installed",
		},
		{
			name:       "selected required component missing",
			selected:   []viewmodel.SelectedComponent{{Kind: "skill", Name: "guide"}, {Kind: "mcp", Name: "required-server"}},
			eligible:   []string{"codex"},
			observed:   []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "guide", Status: "installed"}, {AgentID: "codex", Kind: "mcp", Name: "required-server", Status: "absent"}},
			wantStatus: "Partial",
		},
		{
			name:       "every selected eligible destination is required",
			selected:   []viewmodel.SelectedComponent{{Kind: "skill", Name: "guide"}},
			eligible:   []string{"codex", "opencode"},
			observed:   []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "guide", Status: "installed"}, {AgentID: "opencode", Kind: "skill", Name: "guide", Status: "absent"}},
			wantStatus: "Partial",
		},
		{
			name:       "unknown observation is not completion",
			selected:   []viewmodel.SelectedComponent{{Kind: "mcp", Name: "server"}},
			eligible:   []string{"codex"},
			observed:   []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "mcp", Name: "server", Status: "unavailable"}},
			wantStatus: "Unknown",
		},
		{
			name:       "unavailable selected destination is excluded and empty scope is not complete",
			selected:   []viewmodel.SelectedComponent{{Kind: "mcp", Name: "server"}},
			eligible:   []string{},
			observed:   []viewmodel.ComponentStatus{{AgentID: "retired", Kind: "mcp", Name: "server", Status: "installed"}},
			wantStatus: "Not installed",
		},
		{
			name:       "no selected optional components is not vacuously installed",
			selected:   []viewmodel.SelectedComponent{},
			eligible:   []string{"codex"},
			observed:   []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "mcp", Name: "optional", Status: "installed"}},
			wantStatus: "Not installed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, msg := homeFixture()
			msg.capabilityProfiles = map[string]viewmodel.CapabilityProfileSnapshot{
				"inspect": {Profiles: []viewmodel.CapabilityProfile{{
					Ref:                  config.ProfileRef{Name: "prod"},
					Key:                  state.Key{Source: "one", Package: "inspect", Target: "prod"},
					CompletionScopeKnown: true, CompletionScopeRelevant: true,
					SelectedComponents:   tc.selected,
					SelectedDestinations: []string{"codex"},
					EligibleDestinations: tc.eligible,
					Components:           tc.observed,
				}}},
			}
			m.Update(msg)

			status, _ := m.installationDetails(CapabilityRow{
				Source: "one", Package: "inspect", Skill: true, MCP: true, MCPNames: []string{"optional-server"},
			})
			if status != tc.wantStatus {
				t.Fatalf("completion status = %q, want %q", status, tc.wantStatus)
			}
		})
	}
}

func TestTask8UnknownCompletionScopeDoesNotClaimCompletion(t *testing.T) {
	m, msg := homeFixture()
	msg.capabilityProfiles = map[string]viewmodel.CapabilityProfileSnapshot{
		"inspect": {Profiles: []viewmodel.CapabilityProfile{{
			Ref:                     config.ProfileRef{Name: "prod"},
			CompletionScopeRelevant: true, CompletionScopeError: "preview unavailable",
			Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "guide", Status: "installed"}},
		}}},
	}
	m.Update(msg)
	status, _ := m.installationDetails(CapabilityRow{Source: "one", Package: "inspect", Skill: true})
	if status != "Unknown" {
		t.Fatalf("unresolved selected-work scope = %q, want Unknown", status)
	}
}

func TestTask8CompleteProfileDoesNotMaskOtherSelectedWork(t *testing.T) {
	for _, tc := range []struct {
		name, status string
	}{
		{name: "partial", status: "absent"},
		{name: "unknown", status: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, msg := homeFixture()
			msg.capabilityProfiles = map[string]viewmodel.CapabilityProfileSnapshot{
				"inspect": {Profiles: []viewmodel.CapabilityProfile{
					{
						Ref: config.ProfileRef{Name: "complete"}, CompletionScopeKnown: true, CompletionScopeRelevant: true,
						SelectedComponents:   []viewmodel.SelectedComponent{{Kind: "skill", Name: "guide"}},
						SelectedDestinations: []string{"codex"}, EligibleDestinations: []string{"codex"},
						Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "guide", Status: "installed"}},
					},
					{
						Ref: config.ProfileRef{Name: "incomplete"}, CompletionScopeKnown: true, CompletionScopeRelevant: true,
						SelectedComponents:   []viewmodel.SelectedComponent{{Kind: "skill", Name: "guide"}},
						SelectedDestinations: []string{"opencode"}, EligibleDestinations: []string{"opencode"},
						Components: []viewmodel.ComponentStatus{{AgentID: "opencode", Kind: "skill", Name: "guide", Status: tc.status}},
					},
					// A newly created profile with no selected work is not a
					// reason to dilute a capability's resolved completion.
					{Ref: config.ProfileRef{Name: "new"}, CompletionScopeKnown: true},
				}},
			}
			m.Update(msg)
			status, _ := m.installationDetails(CapabilityRow{Source: "one", Package: "inspect", Skill: true})
			if status != "Partial" {
				t.Fatalf("complete profile masked another profile's %s selected work: got %q, want Partial", tc.name, status)
			}
		})
	}
}
