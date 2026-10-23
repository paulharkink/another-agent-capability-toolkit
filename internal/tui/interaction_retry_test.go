package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
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
	// This retry journey needs a configurable capability with an Inputs stage;
	// the shared fixture catalog's plain skill now correctly goes straight to
	// Agent Access as its single default Capability Instance.
	m.catalog[0].Inputs = []catalog.Input{{Name: "repo", Label: "Repository", Type: "string"}}
	startHomeSetup(m)
	if m.form == nil {
		t.Fatal("setup form did not open")
	}
	m.form.SelectSectionID(sectionInputsID)
	m.form.FocusSection()
	if !m.form.ReconcileDraftField("repo", "/repos/edited") {
		t.Fatal("could not edit the saved profile input draft")
	}
	_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("Inputs Save did not submit")
	}
	m.Update(save())
	if backend.savedInputRequest == nil || backend.installRequest != nil {
		t.Fatal("Inputs Save did not persist answers separately from installation")
	}
	m.form.SelectSectionID(sectionAgentsID)
	_, install := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if install == nil {
		t.Fatal("Install selected did not start installation")
	}
	m.Update(runTeaCmd(t, m, install))
	if backend.installRequest == nil || backend.installRequest.Inputs["repo"] != "/repos/edited" {
		t.Fatalf("edited draft was not submitted: %+v", backend.installRequest)
	}
	if m.result == nil || !strings.Contains(m.View().Content, "Edit answers") {
		t.Fatal("failed setup result does not offer Edit answers")
	}
	m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if m.result != nil || m.form == nil || m.form.Values()["repo"] != "/repos/edited" {
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
	m.catalog[0].Inputs = []catalog.Input{{Name: "repo", Label: "Repository", Type: "string"}}
	startHomeSetup(m)
	_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("Inputs Save did not submit")
	}
	m.Update(save())
	if backend.installRequest != nil {
		t.Fatal("Inputs Save unexpectedly installed the capability")
	}
	m.form.SelectSectionID(sectionAgentsID)
	_, install := m.Update(forms.ActionMsg{SectionID: sectionAgentsID, ID: "install-selected"})
	if install == nil {
		t.Fatal("Install selected did not start installation")
	}
	m.Update(runTeaCmd(t, m, install))
	if m.result == nil || !m.result.Failed || !strings.Contains(m.View().Content, "Edit answers") {
		t.Fatal("partial installation failure was displayed as success")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // Close the result and return to the editable workspace.
	if m.result != nil || m.form == nil || m.workspace.Active || m.workspace.Draft == nil {
		t.Fatal("closing the failed result did not preserve the draft and return to the parent")
	}
}
