package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestFailedSetupResultCanEditSubmittedAnswers(t *testing.T) {
	backend := &setupBackendFixture{
		installResult: &viewmodel.OperationResult{Saved: true},
		installErr:    errors.New("bind 127.0.0.1:9999: address already in use"),
	}
	m := NewContext(context.Background(), backend)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m.Update(m.Init()())
	m.Update(m.homeOperation("parameters")())
	if m.form == nil {
		t.Fatal("setup form did not open")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "/repos/edited"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("setup Save did not submit")
	}
	m.Update(cmd())
	if backend.installRequest == nil || backend.installRequest.Inputs["repo"] != "/repos/edited" {
		t.Fatalf("edited draft was not submitted: %+v", backend.installRequest)
	}
	if m.result == nil || !strings.Contains(m.View().Content, "Edit answers") {
		t.Fatal("failed setup result does not offer Edit answers")
	}
	m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if m.result != nil || m.form == nil || !strings.Contains(m.View().Content, "/repos/edited") {
		t.Fatal("Edit answers did not restore the submitted draft")
	}
}

func TestPartialSetupFailureOffersEditAnswers(t *testing.T) {
	backend := &setupBackendFixture{installResult: &viewmodel.OperationResult{
		Saved:  true,
		Errors: []string{"codex: permission denied"},
	}}
	m := NewContext(context.Background(), backend)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m.Update(m.Init()())
	m.Update(m.homeOperation("parameters")())
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m.Update(cmd())
	if m.result == nil || !m.result.Failed || !strings.Contains(m.View().Content, "Edit answers") {
		t.Fatal("partial installation failure was displayed as success")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.result != nil || m.form != nil {
		t.Fatal("Back action in failed result did not return to the previous screen")
	}
}
