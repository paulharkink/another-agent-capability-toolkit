package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type groupScreenBackend struct {
	Backend
	snapshot viewmodel.GroupProfileSnapshot
	updates  []viewmodel.GroupProfileUpdate
	launched []string
	saved    *viewmodel.SetupInstallRequest
}

func TestNeedsInputProgressOpensPromptWithoutStoppingLaunchOperation(t *testing.T) {
	ref := config.InstanceRef{PackID: "pack", CapabilityID: "grafana", Name: "prod"}
	b := &groupScreenBackend{}
	m := NewContext(context.Background(), b)
	m.width = 100
	m.height = 24
	m.view = "Profiles"
	m.setupOperationID = 8
	m.setupOperationPending = true
	m.setupProgressEvents = make(chan viewmodel.OperationProgress)
	m.setupProgressDone = make(chan struct{})
	_, cmd := m.Update(setupProgressMsg{setupID: 8, progress: viewmodel.OperationProgress{Step: "Needs input", ProfileName: "Ops", MemberName: "grafana/prod", MemberStatus: "partial", MemberNeedsInput: true, InstanceRef: ref}})
	if cmd == nil || !m.setupOperationPending || !m.groupProfileAuthPending {
		t.Fatalf("needs-input progress did not open a concurrent prompt: pending=%v auth=%v", m.setupOperationPending, m.groupProfileAuthPending)
	}
	_, _ = m.Update(groupProfileAuthPreviewMsg{preview: viewmodel.SetupPreview{Sections: []catalog.Section{{ID: "authentication", Fields: []string{"token"}}}, Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "token", Label: "Token", Type: "secret", AuthRequired: true}, Editable: true}}}, ref: ref})
	if m.form == nil || !m.setupOperationPending {
		t.Fatal("auth form blocked or replaced the running launch operation")
	}
	_, _ = m.Update(setupProgressMsg{setupID: 8, progress: viewmodel.OperationProgress{Step: "Starting another MCP instance"}})
	if !strings.Contains(m.form.View().Content, "Launch continues") {
		t.Fatal("auth form did not show live progress for other members")
	}
	_, _ = m.Update(forms.ExitRequestMsg{Reason: "cancel"})
	if m.form != nil || m.groupProfileAuthPending || !m.setupOperationPending {
		t.Fatal("skipping auth prompt did not leave background launch running")
	}
}

func TestGroupProfileRetryMergesAndOutranksOlderLaunchResults(t *testing.T) {
	m := NewContext(context.Background(), &groupScreenBackend{})
	m.groupProfileResults = []viewmodel.GroupProfileLaunchResult{{Name: "other/one", Status: "ready"}, {Name: "grafana/prod", Status: "ready"}}
	m.groupProfileResultVersion = map[string]int{"other/one": 1, "grafana/prod": 2}
	m.groupProfileRetryOperation = true
	_, _ = m.Update(groupProfileLaunchMsg{attempt: 1, results: []viewmodel.GroupProfileLaunchResult{{Name: "grafana/prod", Status: "needs-input"}}})
	if len(m.groupProfileResults) != 2 || m.groupProfileResults[0].Name != "other/one" || m.groupProfileResults[1].Status != "ready" {
		t.Fatalf("stale launch replaced newer retry or sibling results: %+v", m.groupProfileResults)
	}
	m.groupProfileRetryOperation = false
	_, _ = m.Update(groupProfileLaunchMsg{attempt: 3, results: []viewmodel.GroupProfileLaunchResult{{Name: "grafana/prod", Status: "partial"}}})
	if len(m.groupProfileResults) != 2 || m.groupProfileResults[1].Status != "partial" {
		t.Fatalf("targeted retry did not merge into prior outcomes: %+v", m.groupProfileResults)
	}
}

func TestNewLaunchResetsRetryGenerationAndManualActionsCannotOrphanProgress(t *testing.T) {
	ref := config.InstanceRef{PackID: "pack", CapabilityID: "grafana", Name: "prod"}
	b := &groupScreenBackend{snapshot: viewmodel.GroupProfileSnapshot{Profiles: []viewmodel.GroupProfile{{PackID: "pack", Name: "Ops", Members: []viewmodel.GroupProfileMember{{Ref: ref}}}}}}
	m := NewContext(context.Background(), b)
	m.view = "Profiles"
	m.groupProfileSnapshot = &b.snapshot
	m.width = 100
	m.height = 24
	m.groupProfileResultVersion = map[string]int{"grafana/prod": 3}
	m.groupProfileResults = []viewmodel.GroupProfileLaunchResult{{Name: "grafana/prod", Status: "failed"}}
	cmd := m.groupProfilesKey("l")
	if cmd == nil || len(m.groupProfileResultVersion) != 0 || len(m.groupProfileResults) != 0 {
		t.Fatalf("new launch retained an earlier retry generation: versions=%v results=%v", m.groupProfileResultVersion, m.groupProfileResults)
	}
	operationID := m.setupOperationID
	m.busy = false
	m.setupOperationPending = true
	if cmd := m.groupProfilesKey("r"); cmd != nil || m.setupOperationID != operationID {
		t.Fatal("manual retry replaced progress channel during active launch")
	}
	m.setupOperationPending = false
	m.groupProfileConcurrentRetries = 1
	version := m.groupProfileResultVersion["grafana/prod"]
	if cmd := m.groupProfilesKey("l"); cmd != nil || m.groupProfileResultVersion["grafana/prod"] != version {
		t.Fatal("new launch overlapped an outstanding auth retry")
	}
	_, _ = m.Update(groupProfileRetryMsg{attempt: 4, results: []viewmodel.GroupProfileLaunchResult{{Name: "grafana/prod", Status: "ready"}}})
	if m.groupProfileConcurrentRetries != 0 {
		t.Fatal("retry completion did not release the launch guard")
	}
	if cmd := m.groupProfilesKey("l"); cmd == nil || len(m.groupProfileResultVersion) != 0 {
		t.Fatal("new launch did not reset result generation after retry completion")
	}
}

func (b *groupScreenBackend) UIGroupProfileSnapshot(context.Context) (viewmodel.GroupProfileSnapshot, error) {
	return b.snapshot, nil
}
func (b *groupScreenBackend) UISetGroupProfile(_ context.Context, u viewmodel.GroupProfileUpdate) error {
	b.updates = append(b.updates, u)
	return nil
}
func (b *groupScreenBackend) UIDeleteGroupProfile(context.Context, string) error { return nil }
func (b *groupScreenBackend) UIGroupProfileLaunch(_ context.Context, name string) ([]viewmodel.GroupProfileLaunchResult, error) {
	b.launched = append(b.launched, name)
	return nil, nil
}
func (b *groupScreenBackend) UIGroupProfileRetry(context.Context, string, config.InstanceRef) ([]viewmodel.GroupProfileLaunchResult, error) {
	return nil, nil
}
func (b *groupScreenBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	return viewmodel.SetupPreview{Key: state.Key{Source: request.Ref.PackID, Package: request.Ref.CapabilityID, Target: request.Ref.Name}}, nil
}
func (b *groupScreenBackend) UIInstall(context.Context, viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	return viewmodel.OperationResult{}, nil
}
func (b *groupScreenBackend) UISaveInputs(_ context.Context, request viewmodel.SetupInstallRequest) error {
	b.saved = &request
	return nil
}

func TestProfilesMenuAndTwoPanePreview(t *testing.T) {
	ref := config.InstanceRef{PackID: "pack", CapabilityID: "grafana", Name: "prod"}
	b := &groupScreenBackend{snapshot: viewmodel.GroupProfileSnapshot{Profiles: []viewmodel.GroupProfile{{PackID: "pack", Name: "Operations", Origin: "pack", Editable: true, Members: []viewmodel.GroupProfileMember{{Ref: ref, Status: "needs input"}}}}}}
	m := NewContext(context.Background(), b)
	m.view = "Profiles"
	m.groupProfileSnapshot = &b.snapshot
	m.width = 100
	m.height = 24
	text := m.View().Content
	if !strings.Contains(text, "Operations") || !strings.Contains(text, "grafana / prod") || !strings.Contains(text, "needs input") {
		t.Fatalf("profile preview missing content:\n%s", text)
	}
	m.management.Focus = ProfilesPane
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.workspace == nil || m.workspace.InstanceRef != ref {
		t.Fatalf("member Enter did not open exact instance workspace: cmd=%v workspace=%+v output=%q view=%s snap=%+v focus=%d", cmd != nil, m.workspace, m.output, m.view, m.groupProfileSnapshot, m.management.Focus)
	}
}

func TestNeedsInputAuthPromptContainsOnlyDeclaredMissingCookieFieldsAndSaves(t *testing.T) {
	ref := config.InstanceRef{PackID: "pack", CapabilityID: "grafana", Name: "prod"}
	inputs := []viewmodel.SetupInput{
		{Definition: catalog.Input{Name: "auth_mode", Type: "choice"}, Value: "session_cookie", HasValue: true, Editable: true},
		{Definition: catalog.Input{Name: "grafana_session", Label: "Grafana session", Type: "secret", AuthRequired: true}, Editable: true},
		{Definition: catalog.Input{Name: "session_expiry", Label: "Session expiry", Type: "secret", AuthPrompt: true}, Editable: true},
		{Definition: catalog.Input{Name: "oauth_refresh", Label: "OAuth refresh", Type: "secret"}, Editable: true},
	}
	preview := viewmodel.SetupPreview{Sections: []catalog.Section{{ID: "authentication", Fields: []string{"auth_mode", "grafana_session", "session_expiry", "oauth_refresh"}}}, Inputs: inputs}
	b := &groupScreenBackend{}
	m := NewContext(context.Background(), b)
	m.width = 100
	m.height = 24
	m.openMinimalGroupProfileAuthForm(preview, ref)
	view := m.form.View().Content
	if !strings.Contains(view, "Grafana session") || !strings.Contains(view, "Session expiry") {
		t.Fatalf("cookie fields missing from prompt:\n%s", view)
	}
	if strings.Contains(view, "OAuth refresh") {
		t.Fatalf("unrequested optional OAuth field included:\n%s", view)
	}
	cmd := m.saveGroupProfileAuth(map[string]any{"grafana_session": "cookie", "session_expiry": "2026-11-12T00:00:00Z"})
	msg := cmd().(groupProfileAuthSavedMsg)
	if msg.err != nil || b.saved == nil || b.saved.Inputs["grafana_session"] != "cookie" || b.saved.Inputs["session_expiry"] == nil {
		t.Fatalf("auth inputs were not saved: msg=%+v request=%+v", msg, b.saved)
	}
}

func TestGroupProfileEditorAllowsEmptyOrSelectedMembership(t *testing.T) {
	ref := config.InstanceRef{PackID: "pack", CapabilityID: "grafana", Name: "prod"}
	b := &groupScreenBackend{snapshot: viewmodel.GroupProfileSnapshot{Candidates: []viewmodel.GroupProfileMember{{Ref: ref, Status: "ready"}}}}
	m := NewContext(context.Background(), b)
	m.groupProfileSnapshot = &b.snapshot
	m.width = 100
	m.height = 24
	m.openGroupProfileEditor(nil)
	if m.form == nil || len(m.form.Values()) == 0 {
		t.Fatal("Profile editor did not open")
	}
	if got := m.groupProfileRefOptions[encodeGroupMember(ref)]; got != ref {
		t.Fatalf("membership choice lost instance identity: %+v", got)
	}
}
