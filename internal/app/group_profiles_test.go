package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestUIGroupProfileSnapshotMergesEditableMembershipAndObservedInstances(t *testing.T) {
	s, _, _ := profileApplyFixture(t)
	packID := s.Source.ID
	ref := config.InstanceRef{PackID: packID, CapabilityID: "demo", Name: "ota"}
	s.Source.GroupProfiles = []config.GroupProfile{{PackID: packID, Name: "Operations", Description: "Pack supplied", Members: []config.InstanceRef{ref}}}
	if err := s.Store.SaveGroupProfile(state.GroupProfile{PackID: packID, Name: "Operations", Members: []state.GroupProfileMember{}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveGroupProfile(state.GroupProfile{PackID: packID, Name: "Local", Members: []state.GroupProfileMember{{CapabilityID: ref.CapabilityID, InstanceName: ref.Name}}}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := s.UIGroupProfileSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("merged profiles = %#v", snapshot.Profiles)
	}
	packProfile := snapshot.Profiles[0]
	if packProfile.Name != "Local" || packProfile.Origin != "local" || !packProfile.Editable || len(packProfile.Members) != 1 {
		t.Fatalf("local profile row = %#v", packProfile)
	}
	packProfile = snapshot.Profiles[1]
	if packProfile.Name != "Operations" || packProfile.Origin != "pack" || !packProfile.Editable || packProfile.Description != "Pack supplied" || len(packProfile.Members) != 0 {
		t.Fatalf("pack profile membership override = %#v", packProfile)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].Ref != ref || snapshot.Candidates[0].ConfigStatus != "configured" {
		t.Fatalf("candidate instance preview = %#v", snapshot.Candidates)
	}
}

func TestUIGroupProfileSnapshotReportsMissingMemberWithoutDroppingProfile(t *testing.T) {
	s, _, _ := profileApplyFixture(t)
	packID := s.Source.ID
	s.Source.GroupProfiles = []config.GroupProfile{{PackID: packID, Name: "Legacy", Members: []config.InstanceRef{{PackID: packID, CapabilityID: "demo", Name: "removed"}}}}
	snapshot, err := s.UIGroupProfileSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 1 || len(snapshot.Profiles[0].Members) != 1 || snapshot.Profiles[0].Members[0].Status != "missing" {
		t.Fatalf("missing member should remain visible: %#v", snapshot.Profiles)
	}
}

func TestGroupProfileSnapshotDoesNotTreatUnauthenticatedRunningMCPAsReady(t *testing.T) {
	member := memberFromInstance(viewmodel.CapabilityInstance{Ref: config.InstanceRef{PackID: "pack", CapabilityID: "grafana", Name: "prod"}, ConfigStatus: "configured", MCPs: []viewmodel.RuntimeStatus{{MCPID: "grafana", Status: "running", AuthenticationStatus: viewmodel.AuthenticationFailed}}})
	if member.Status != "needs input" {
		t.Fatalf("running unauthenticated instance status = %q, want needs input", member.Status)
	}
}

func TestGroupProfileRetryRejectsNonMemberInstance(t *testing.T) {
	s, _, _ := profileApplyFixture(t)
	ref := config.InstanceRef{PackID: s.Source.ID, CapabilityID: "demo", Name: "not-in-profile"}
	if err := s.Store.SaveGroupProfile(state.GroupProfile{PackID: s.Source.ID, Name: "Ops"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UIGroupProfileRetry(context.Background(), "Ops", ref); err == nil {
		t.Fatal("Retry silently accepted an instance outside the Profile")
	}
}

func TestPackProfileMissingMemberDoesNotBlockRemainingLaunches(t *testing.T) {
	s, _, _ := profileApplyFixture(t)
	s.Source.GroupProfiles = []config.GroupProfile{{PackID: s.Source.ID, Name: "Operations", Members: []config.InstanceRef{{PackID: s.Source.ID, CapabilityID: "demo", Name: "removed"}, {PackID: s.Source.ID, CapabilityID: "demo", Name: "ota"}}}}
	runtime := &groupLaunchRuntime{}
	s.Options.Runtime = runtime
	results, err := s.UIGroupProfileLaunch(context.Background(), "Operations")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Status != "skipped" || results[1].Status != "ready" {
		t.Fatalf("missing member blocked valid launch: %+v", results)
	}
}

func TestGroupProfileNeedsInputIsIndependentOfSiblingMCPOutcomes(t *testing.T) {
	for _, results := range [][]viewmodel.GroupProfileRuntimeResult{
		{{Status: "ready"}, {Status: "needs-input"}},
		{{Status: "failed"}, {Status: "needs-input"}},
	} {
		if !groupProfileNeedsInput(results) {
			t.Fatalf("mixed MCP results lost auth requirement: %+v", results)
		}
	}
}

type groupLaunchRuntime struct {
	starts    []state.Key
	instances []mcp.Instance
}

func (r *groupLaunchRuntime) Start(_ context.Context, key state.Key, _ mcp.RunSpec) (mcp.Instance, error) {
	r.starts = append(r.starts, key)
	if key.Target == "fail" {
		return mcp.Instance{}, errors.New("fixture start failure")
	}
	instance := mcp.Instance{Key: key, ID: fmt.Sprintf("container-%d", len(r.starts)), Status: "running", Ownership: "local", URL: "http://127.0.0.1:8765/mcp"}
	r.instances = append(r.instances, instance)
	return instance, nil
}
func (*groupLaunchRuntime) Stop(context.Context, state.Key) error { return nil }
func (r *groupLaunchRuntime) List(context.Context) ([]mcp.Instance, error) {
	return append([]mcp.Instance{}, r.instances...), nil
}
func (*groupLaunchRuntime) Logs(context.Context, state.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func TestGroupProfileLaunchContinuesAfterNeedsInputFailureAndSkip(t *testing.T) {
	s, _, request := profileApplyFixture(t)
	request.Inputs = nil
	s.Source.Catalog[0].MCPs[0].EnabledInput = "enabled"
	if err := s.CreateInstance(context.Background(), config.InstanceRef{PackID: s.Source.ID, CapabilityID: "demo", Name: "needs-auth"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInstance(context.Background(), config.InstanceRef{PackID: s.Source.ID, CapabilityID: "demo", Name: "fail"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Source.ProfileRoot, "demo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Source.ProfileRoot, "demo", "fail.toml"), []byte("[inputs]\nlabel='company'\nenabled=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plainDir := t.TempDir()
	s.Source.Catalog = append(s.Source.Catalog, catalog.Package{ID: "plain", Name: "Plain skill", Dir: plainDir, Skill: &catalog.Skill{Name: "plain"}})
	runtime := &groupLaunchRuntime{}
	s.Options.Runtime = runtime
	refs := []config.InstanceRef{
		{PackID: s.Source.ID, CapabilityID: "demo", Name: "needs-auth"},
		{PackID: s.Source.ID, CapabilityID: "demo", Name: "fail"},
		{PackID: s.Source.ID, CapabilityID: "plain", Name: "default"},
		{PackID: s.Source.ID, CapabilityID: "demo", Name: "ota"},
	}
	members := make([]state.GroupProfileMember, 0, len(refs))
	for _, ref := range refs {
		members = append(members, state.GroupProfileMember{CapabilityID: ref.CapabilityID, InstanceName: ref.Name})
	}
	if err := s.Store.SaveGroupProfile(state.GroupProfile{PackID: s.Source.ID, Name: "Operations", Members: members}); err != nil {
		t.Fatal(err)
	}

	var progress []viewmodel.OperationProgress
	launchCtx := viewmodel.WithOperationProgress(context.Background(), func(event viewmodel.OperationProgress) { progress = append(progress, event) })
	results, err := s.UIGroupProfileLaunch(launchCtx, "Operations")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("launch results = %#v", results)
	}
	want := []string{"needs-input", "failed", "skipped", "ready"}
	for i, status := range want {
		if results[i].Status != status {
			t.Errorf("member %s status = %q, want %q (result: %+v)", refs[i].Name, results[i].Status, status, results[i])
		}
	}
	if len(runtime.starts) != 2 { // disabled alpha is omitted; beta runs for fail and ota.
		t.Fatalf("start attempts = %d, want 2 with disabled alpha omitted: %+v", len(runtime.starts), runtime.starts)
	}
	for _, key := range runtime.starts {
		if key.MCP != "beta" && key.MCPDefinition != "beta" {
			t.Fatalf("disabled MCP was started: %+v", key)
		}
	}
	if len(results[3].MCPs) != 1 || results[3].MCPs[0].Status != "ready" {
		t.Fatalf("successful runtime status must come from fresh observation: %+v", results[3].MCPs)
	}
	var completed []viewmodel.OperationProgress
	for _, event := range progress {
		if event.MemberStatus != "" {
			completed = append(completed, event)
		}
	}
	if len(completed) != 4 || completed[0].MemberStatus != "needs-input" || completed[0].InstanceRef != refs[0] || completed[1].MemberStatus != "failed" {
		t.Fatalf("per-member progress events did not preserve continuation order: %+v", progress)
	}
}
