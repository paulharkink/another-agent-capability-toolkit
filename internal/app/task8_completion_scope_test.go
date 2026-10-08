package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type task8StrictRegistry struct{ adapter agents.Adapter }

func (r task8StrictRegistry) Adapter(id string) (agents.Adapter, error) {
	if id != r.adapter.ID() {
		return nil, fmt.Errorf("unsupported fixture adapter %q", id)
	}
	return r.adapter, nil
}
func (r task8StrictRegistry) Adapters() []agents.Adapter { return []agents.Adapter{r.adapter} }

func recordTask8Selection(t *testing.T, s *Service, q ProfileRequest, items, destinations []string) {
	t.Helper()
	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordProfile(state.ProfileRecord{
		Key: key, Name: q.Ref.Name,
		Selection: &state.ProfileSelection{ItemIDs: items, DestinationIDs: destinations},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTask8SnapshotKeepsSelectedIntentSeparateFromObservedCompletion(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	s.Options.Adapters = task8StrictRegistry{adapter: a}
	a.observe = func(agents.ObservationRequest) agents.Observation {
		return agents.Observation{Components: []agents.ComponentObservation{{Kind: "skill", Name: "optional", Status: "installed"}}}
	}
	recordTask8Selection(t, s, q, []string{"skill:optional"}, []string{a.ID()})
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if !profile.CompletionScopeKnown || !profile.CompletionScopeRelevant || profile.CompletionScopeError != "" {
		t.Fatalf("completion scope was not resolved: %+v", profile)
	}
	if len(profile.SelectedComponents) != 1 || profile.SelectedComponents[0].Kind != "skill" || profile.SelectedComponents[0].Name != "optional" {
		t.Fatalf("selected component intent included unselected components: %+v", profile.SelectedComponents)
	}
	if len(profile.SelectedDestinations) != 1 || profile.SelectedDestinations[0] != a.ID() {
		t.Fatalf("selected destination intent changed: %v", profile.SelectedDestinations)
	}
	if len(profile.EligibleDestinations) != 1 || profile.EligibleDestinations[0] != a.ID() {
		t.Fatalf("skill-only work incorrectly inherited the optional MCP config restriction: %v", profile.EligibleDestinations)
	}
	observedInstalled := false
	for _, component := range profile.Components {
		observedInstalled = observedInstalled || (component.AgentID == a.ID() && component.Kind == "skill" && component.Name == "optional" && component.Status == "installed")
	}
	if !observedInstalled {
		t.Fatalf("completion intent replaced or lost actual observation facts: %+v", profile.Components)
	}
}

func TestTask8ExplicitEmptySelectionDoesNotFallBackToDefaults(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	s.Options.Adapters = task8StrictRegistry{adapter: a}
	if err := s.UISetDefaultAgents(context.Background(), []string{a.ID()}); err != nil {
		t.Fatal(err)
	}
	recordTask8Selection(t, s, q, []string{}, []string{})
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if !profile.CompletionScopeKnown || !profile.CompletionScopeRelevant || len(profile.SelectedComponents) != 0 || len(profile.SelectedDestinations) != 0 || len(profile.EligibleDestinations) != 0 {
		t.Fatalf("explicitly empty work fell back to defaults: %+v", profile)
	}
}

func TestTask8LegacyProfileUsesResolvedDefaultSelection(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	s.Options.Adapters = task8StrictRegistry{adapter: a}
	s.Source.Catalog[0].MCPs = nil
	s.Source.Catalog[0].Sets = nil
	if err := s.UISetDefaultAgents(context.Background(), []string{a.ID()}); err != nil {
		t.Fatal(err)
	}
	key, err := s.Store.ResolveProfileKey(q.Ref.PackID, q.Ref.CapabilityID, q.Ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Record(state.Installation{Key: key, AgentID: a.ID(), AgentKind: a.ID(), Component: "skill"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if !profile.CompletionScopeKnown || !profile.CompletionScopeRelevant || len(profile.SelectedComponents) != 3 {
		t.Fatalf("legacy profile did not resolve existing default skill selection: %+v", profile)
	}
	if len(profile.SelectedDestinations) != 1 || profile.SelectedDestinations[0] != a.ID() || len(profile.EligibleDestinations) != 1 {
		t.Fatalf("legacy profile did not resolve its configured default destination: selected=%v eligible=%v", profile.SelectedDestinations, profile.EligibleDestinations)
	}
}

func TestTask8UnavailableSelectedAgentIsExcludedFromDenominator(t *testing.T) {
	s, a, q := profileApplyFixture(t)
	s.Options.Adapters = task8StrictRegistry{adapter: a}
	recordTask8Selection(t, s, q, []string{"skill:optional"}, []string{"retired-agent"})
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if len(profile.SelectedDestinations) != 1 || profile.SelectedDestinations[0] != "retired-agent" || len(profile.EligibleDestinations) != 0 {
		t.Fatalf("unavailable selected destination was counted as eligible: selected=%v eligible=%v", profile.SelectedDestinations, profile.EligibleDestinations)
	}
	foundUnavailable := false
	for _, component := range profile.Components {
		if component.AgentID == "retired-agent" && component.Status == "unavailable" {
			foundUnavailable = true
		}
	}
	if !foundUnavailable {
		t.Fatalf("actual unavailable observation was lost while excluding denominator: %+v", profile.Components)
	}
}

func TestTask8SelectedMCPIsNotEligibleOnSkillsOnlyAdapter(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	s.Source.Catalog[0].Sets = nil
	adapter := &uiBoundaryAdapter{
		features:  agents.FeatureSet{Skills: true},
		detection: agents.Detection{State: "installed", Installed: true},
	}
	s.Options.Adapters = task8StrictRegistry{adapter: adapter}
	recordTask8Selection(t, s, q, []string{"mcp:alpha"}, []string{adapter.ID()})
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if len(profile.SelectedComponents) != 1 || profile.SelectedComponents[0].Kind != "mcp" || len(profile.SelectedDestinations) != 1 || len(profile.EligibleDestinations) != 0 {
		t.Fatalf("selected unsupported MCP work was treated as eligible: profile=%+v", profile)
	}
}

func TestTask8MCPDisabledReasonDoesNotExcludeSelectedSkill(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	adapter := &uiBoundaryAdapter{
		features: agents.FeatureSet{Skills: true},
		detection: agents.Detection{
			State: "installed", Installed: true,
			MCPDisabledReason: "fixture MCP endpoint policy unavailable",
		},
	}
	s.Options.Adapters = task8StrictRegistry{adapter: adapter}
	recordTask8Selection(t, s, q, []string{"skill:optional"}, []string{adapter.ID()})
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if len(profile.EligibleDestinations) != 1 || profile.EligibleDestinations[0] != adapter.ID() {
		t.Fatalf("MCP-only disabled reason excluded detected skills-only work: selected=%v eligible=%v", profile.SelectedDestinations, profile.EligibleDestinations)
	}
	preview, err := s.PreviewProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var previewDestination *viewmodel.SetupDestination
	for i := range preview.Destinations {
		if preview.Destinations[i].ID == adapter.ID() {
			previewDestination = &preview.Destinations[i]
			break
		}
	}
	if previewDestination == nil || !previewDestination.Detected || previewDestination.MCPRegistrationAvailable {
		t.Fatalf("preview did not preserve distinct detected-skill and unavailable-MCP facts: %+v", preview.Destinations)
	}
}

func TestTask8NewProfileWithoutSelectionOrEffectsIsNotRelevant(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	q.Ref.Name = "new"
	if err := s.CreateProfile(context.Background(), q.Ref); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref.Name == q.Ref.Name {
			if profile.CompletionScopeRelevant {
				t.Fatalf("unconfigured profile with no saved selection or component effects became completion work: %+v", profile)
			}
			return
		}
	}
	t.Fatal("new profile missing from capability snapshot")
}

func TestTask8GenericSkillsDestinationIsDetectedWithoutAgentBinary(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	s.Source.Catalog[0].MCPs = nil
	s.Source.Catalog[0].Sets = nil
	home := t.TempDir()
	s.Options.Adapters = agents.NewRegistry(agents.Dependencies{
		Store: s.Store,
		Probe: agents.DiscoveryProbe{
			GOOS: "linux", Home: home,
			LookPath: func(string) (string, error) { return "", fmt.Errorf("fixture binary absent") },
			Getenv:   func(string) string { return "" },
		},
	})
	recordTask8Selection(t, s, q, []string{"skill:one"}, []string{"generic"})
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if !profile.CompletionScopeRelevant || len(profile.EligibleDestinations) != 1 || profile.EligibleDestinations[0] != "generic" {
		t.Fatalf("generic shared-skill destination was treated as an undetected agent: %+v", profile)
	}
}

func TestTask8GenericDestinationEligibleForSelectedSkillsInMixedPackage(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	home := t.TempDir()
	s.Options.Adapters = agents.NewRegistry(agents.Dependencies{
		Store: s.Store,
		Probe: agents.DiscoveryProbe{
			GOOS: "linux", Home: home,
			LookPath: func(string) (string, error) { return "", fmt.Errorf("fixture binary absent") },
			Getenv:   func(string) string { return "" },
		},
	})
	q.ItemIDs = []string{"skill:optional"}
	q.DestinationIDs = []string{"generic"}
	preview, err := s.PreviewProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	available := false
	for _, destination := range preview.Destinations {
		if destination.ID == "generic" && destination.Detected && destination.Features.Skills && !destination.MCPRegistrationAvailable {
			available = true
		}
	}
	if !available {
		t.Fatalf("selected skill-only work omitted the generic shared-skills destination: %+v", preview.Destinations)
	}
	q.Inputs = map[string]any{"label": "valid", "enabled": false}
	if _, err := s.ApplyProfile(context.Background(), q); err != nil {
		t.Fatalf("apply selected skill-only work to generic destination: %v", err)
	}
	snapshot, err := s.ProfileSnapshot(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot.Profiles[0]
	if len(profile.EligibleDestinations) != 1 || profile.EligibleDestinations[0] != "generic" {
		t.Fatalf("mixed-package skill-only completion scope excluded generic: %+v", profile)
	}
	installed := false
	for _, component := range profile.Components {
		installed = installed || (component.AgentID == "generic" && component.Kind == "skill" && component.Name == "optional" && component.Status == "installed")
	}
	if !installed {
		t.Fatalf("generic skill install was not reflected as an achieved effect: %+v", profile.Components)
	}
}

func TestTask8GenericDestinationRemainsExcludedWhenSelectedWorkIncludesMCP(t *testing.T) {
	s, _, q := profileApplyFixture(t)
	q.ItemIDs = nil
	q.Inputs = map[string]any{"label": "valid", "enabled": true}
	q.DestinationIDs = []string{"generic"}
	preview, err := s.PreviewProfile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range preview.Destinations {
		if destination.ID == "generic" {
			t.Fatalf("generic destination was exposed for selected MCP work: %+v", destination)
		}
	}
}
