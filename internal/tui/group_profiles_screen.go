package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func (m *Model) loadGroupProfiles() tea.Cmd {
	backend, ok := m.backend.(groupProfilesBackend)
	if !ok {
		m.output = "Profiles are unavailable in this backend"
		return nil
	}
	m.busy, m.action = true, "load Profiles"
	return func() tea.Msg {
		snapshot, err := backend.UIGroupProfileSnapshot(m.ctx)
		return groupProfilesLoadedMsg{snapshot: snapshot, err: err}
	}
}

func (m *Model) groupProfilesKey(key string) tea.Cmd {
	if key == "f9" || key == "m" {
		m.management.Modal, m.management.ModalSelected = "main", 0
		return nil
	}
	if key == "esc" {
		m.navigate("Catalog")
		return nil
	}
	if key == "f5" || key == "R" {
		return m.loadGroupProfiles()
	}
	if m.groupProfileSnapshot == nil {
		return nil
	}
	if (m.setupOperationPending || m.groupProfileConcurrentRetries > 0) && (key == "l" || key == "r" || key == "c" || key == "e" || key == "delete") {
		m.output = "Profile launch or targeted retry is still running; wait for it to finish before changing membership or starting another operation."
		return nil
	}
	profiles := m.groupProfileSnapshot.Profiles
	if len(profiles) == 0 {
		m.selected = 0
	}
	switch key {
	case "left", "shift+tab":
		m.management.Focus = CapabilitiesPane
	case "right", "tab":
		m.management.Focus = ProfilesPane
	case "up", "k":
		if m.management.Focus == CapabilitiesPane {
			m.selected = max(0, m.selected-1)
		} else {
			m.groupProfileMemberIndex = max(0, m.groupProfileMemberIndex-1)
		}
	case "down", "j":
		if m.management.Focus == CapabilitiesPane {
			m.selected = min(max(0, len(profiles)-1), m.selected+1)
		} else if p, ok := m.selectedGroupProfile(); ok {
			m.groupProfileMemberIndex = min(max(0, len(p.Members)-1), m.groupProfileMemberIndex+1)
		}
	case "c":
		m.openGroupProfileEditor(nil)
	case "e":
		if p, ok := m.selectedGroupProfile(); ok {
			m.openGroupProfileEditor(&p)
		}
	case "l":
		if p, ok := m.selectedGroupProfile(); ok {
			m.groupProfileLaunchName = p.Name
			m.groupProfileAuthQueue = nil
			m.groupProfileAuthPrompted = map[string]bool{}
			m.groupProfileResultVersion = map[string]int{}
			m.groupProfileResults = nil
			return m.runGroupProfile(p.Name, nil)
		}
	case "r":
		if p, ok := m.selectedGroupProfile(); ok && m.management.Focus == ProfilesPane && m.groupProfileMemberIndex < len(p.Members) {
			ref := p.Members[m.groupProfileMemberIndex].Ref
			m.groupProfileLaunchName = p.Name
			return m.retryGroupProfileMember(ref)
		}
	case "delete":
		if p, ok := m.selectedGroupProfile(); ok {
			return m.deleteGroupProfile(p.Name)
		}
	case "enter":
		if p, ok := m.selectedGroupProfile(); ok {
			if m.management.Focus == ProfilesPane && m.groupProfileMemberIndex < len(p.Members) {
				member := p.Members[m.groupProfileMemberIndex]
				return m.openTargetWorkspace(viewmodel.SetupRequest{Ref: member.Ref}, "Inputs")
			}
			m.management.Focus = ProfilesPane
			m.groupProfileMemberIndex = 0
		}
	}
	return nil
}

func (m *Model) selectedGroupProfile() (viewmodel.GroupProfile, bool) {
	if m.groupProfileSnapshot == nil || m.selected < 0 || m.selected >= len(m.groupProfileSnapshot.Profiles) {
		return viewmodel.GroupProfile{}, false
	}
	return m.groupProfileSnapshot.Profiles[m.selected], true
}

func (m *Model) groupProfilesView() tea.View {
	w, h := m.width, m.height
	lines := make([]string, h)
	for i := range lines {
		lines[i] = "║" + fit("", w-2) + "║"
	}
	lines[0] = "╔" + fit(" AACT · Profiles", w-2) + "╗"
	lines[1] = "║" + fit(" F9 Main menu: Profiles · Pack-supplied and local named groups", w-2) + "║"
	lines[2] = "╠" + strings.Repeat("═", w-2) + "╣"
	left, right := managementPaneWidths(w)
	lines[3] = "║" + fit("Profiles", left) + "│" + fit("Capability Instances", right) + "║"
	for row := 4; row < h-5; row++ {
		lines[row] = "║" + fit("", left) + "│" + fit("", right) + "║"
	}
	if m.groupProfileSnapshot != nil {
		for i, p := range m.groupProfileSnapshot.Profiles {
			y := 4 + i
			if y >= h-5 {
				break
			}
			label := p.Name + " · " + p.Origin
			if i == m.selected && m.management.Focus == CapabilitiesPane {
				label = managementSelected.Render(label)
			}
			lines[y] = "║" + fit(label, left) + "│" + fit("", right) + "║"
		}
		if p, ok := m.selectedGroupProfile(); ok {
			lines[3] = "║" + fit("Profiles", left) + "│" + fit(p.Name+" · "+p.Description, right) + "║"
			for i, member := range p.Members {
				y := 4 + i
				if y >= h-5 {
					break
				}
				label := member.Ref.CapabilityID + " / " + member.Ref.Name + " · " + member.Status
				if member.Error != "" {
					label += " · " + member.Error
				}
				if i == m.groupProfileMemberIndex && m.management.Focus == ProfilesPane {
					label = managementSelected.Render(label)
				}
				lines[y] = "║" + fit("", left) + "│" + fit(m.cleanOutput(label), right) + "║"
			}
			base := 4 + len(p.Members)
			for i, result := range m.groupProfileResults {
				y := base + i
				if y >= h-5 {
					break
				}
				label := "Launch · " + result.Name + " · " + result.Status
				if result.Error != "" {
					label += " · " + result.Error
				}
				lines[y] = "║" + fit("", left) + "│" + fit(m.cleanOutput(label), right) + "║"
			}
		}
	}
	lines[h-5] = "╠" + strings.Repeat("═", w-2) + "╣"
	lines[h-4] = "║" + fit(" "+m.output, w-2) + "║"
	lines[h-3] = "║" + fit(" c Create · e Edit memberships · l Launch all · r Retry member · Delete Remove/reset", w-2) + "║"
	lines[h-2] = "║" + fit(" Tab/←→ Focus · ↑↓ Select · Enter Preview/open instance · F5 Refresh · Esc Back", w-2) + "║"
	lines[h-1] = "╚" + strings.Repeat("═", w-2) + "╝"
	return tea.NewView(navyCanvas(strings.Join(lines, "\n")))
}

func (m *Model) openGroupProfileEditor(existing *viewmodel.GroupProfile) {
	backend, ok := m.backend.(groupProfilesBackend)
	if !ok {
		return
	}
	if m.groupProfileSnapshot == nil {
		m.output = "Refresh Profiles before editing"
		return
	}
	name := ""
	selected := []string{}
	if existing != nil {
		name = existing.Name
		for _, member := range existing.Members {
			selected = append(selected, encodeGroupMember(member.Ref))
		}
	}
	choices := make([]catalog.Choice, 0, len(m.groupProfileSnapshot.Candidates))
	refs := map[string]config.InstanceRef{}
	for _, member := range m.groupProfileSnapshot.Candidates {
		id := encodeGroupMember(member.Ref)
		refs[id] = member.Ref
		choices = append(choices, catalog.Choice{Value: id, Label: member.Ref.CapabilityID + " / " + member.Ref.Name + " · " + member.Status})
	}
	m.groupProfileEditing = &groupProfileEdit{Name: name, Members: nil}
	_ = backend
	m.groupProfileRefOptions = refs
	m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "name", Label: "Profile name", Type: "string", Required: true}, {Name: "members", Label: "Capability Instances", Type: "multichoice", Options: choices}}, map[string]any{"name": name, "members": selected})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}
func encodeGroupMember(ref config.InstanceRef) string { return ref.CapabilityID + "|||" + ref.Name }

func (m *Model) saveGroupProfile(values map[string]any) tea.Cmd {
	backend, ok := m.backend.(groupProfilesBackend)
	if !ok {
		return nil
	}
	name, _ := values["name"].(string)
	selected, _ := values["members"].([]string)
	refs := make([]config.InstanceRef, 0, len(selected))
	for _, id := range selected {
		if ref, ok := m.groupProfileRefOptions[id]; ok {
			refs = append(refs, ref)
		}
	}
	packID := ""
	if len(refs) > 0 {
		packID = refs[0].PackID
	} else if m.groupProfileSnapshot != nil {
		packID = m.groupProfileSnapshot.PackID
		if p, ok := m.selectedGroupProfile(); ok {
			packID = p.PackID
		}
		if packID == "" && len(m.groupProfileSnapshot.Candidates) > 0 {
			packID = m.groupProfileSnapshot.Candidates[0].Ref.PackID
		}
	}
	if packID == "" {
		m.output = "Cannot save an empty Profile without an active Pack"
		return nil
	}
	m.groupProfileEditing = nil
	m.form = nil
	m.busy = true
	return func() tea.Msg {
		err := backend.UISetGroupProfile(m.ctx, viewmodel.GroupProfileUpdate{PackID: packID, Name: name, Members: refs})
		if err != nil {
			return groupProfilesLoadedMsg{err: err}
		}
		snapshot, e := backend.UIGroupProfileSnapshot(m.ctx)
		return groupProfilesLoadedMsg{snapshot: snapshot, err: e}
	}
}
func (m *Model) deleteGroupProfile(name string) tea.Cmd {
	backend, ok := m.backend.(groupProfilesBackend)
	if !ok {
		return nil
	}
	m.busy = true
	return func() tea.Msg {
		err := backend.UIDeleteGroupProfile(m.ctx, name)
		if err != nil {
			return groupProfilesLoadedMsg{err: err}
		}
		snap, e := backend.UIGroupProfileSnapshot(m.ctx)
		return groupProfilesLoadedMsg{snapshot: snap, err: e}
	}
}
func (m *Model) runGroupProfile(name string, ref *config.InstanceRef) tea.Cmd {
	backend, ok := m.backend.(groupProfilesBackend)
	if !ok {
		return nil
	}
	ctx := m.ctx
	attempt := 1
	if ref != nil {
		attempt = m.nextGroupProfileAttempt(*ref)
	}
	events := make(chan viewmodel.OperationProgress)
	done := make(chan struct{})
	m.setupOperationID++
	id := m.setupOperationID
	m.setupProgressEvents = events
	m.setupProgressDone = done
	m.setupOperationPending = true
	m.busy = true
	m.progressStep = "Launching Profile"
	m.progressStarted = time.Now()
	ctx = viewmodel.WithOperationProgress(ctx, func(p viewmodel.OperationProgress) {
		p.Attempt = attempt
		p.Acknowledged = make(chan struct{})
		select {
		case events <- p:
		case <-done:
			return
		case <-ctx.Done():
			return
		}
		select {
		case <-p.Acknowledged:
		case <-done:
		case <-ctx.Done():
		}
	})
	cmd := func() tea.Msg {
		defer close(done)
		var r []viewmodel.GroupProfileLaunchResult
		var e error
		if ref == nil {
			r, e = backend.UIGroupProfileLaunch(ctx, name)
		} else {
			r, e = backend.UIGroupProfileRetry(ctx, name, *ref)
		}
		return groupProfileLaunchMsg{results: r, err: e, attempt: attempt}
	}
	return tea.Batch(cmd, waitSetupProgress(id, events, done, ctx), setupProgressTick(id))
}

func (m *Model) groupProfileMemberRef(label string) (config.InstanceRef, bool) {
	if m.groupProfileSnapshot == nil {
		return config.InstanceRef{}, false
	}
	for _, profile := range m.groupProfileSnapshot.Profiles {
		if profile.Name != m.groupProfileLaunchName {
			continue
		}
		for _, member := range profile.Members {
			if member.Name == label {
				return member.Ref, true
			}
		}
	}
	return config.InstanceRef{}, false
}

func (m *Model) enqueueGroupAuthPrompt(ref config.InstanceRef) bool {
	if ref.PackID == "" {
		return false
	}
	if m.groupProfileAuthPrompted == nil {
		m.groupProfileAuthPrompted = map[string]bool{}
	}
	key := encodeGroupMember(ref)
	if m.groupProfileAuthPrompted[key] {
		return false
	}
	m.groupProfileAuthPrompted[key] = true
	m.groupProfileAuthQueue = append(m.groupProfileAuthQueue, ref)
	return true
}

func (m *Model) openNextGroupAuthPrompt() tea.Cmd {
	if m.groupProfileAuthPending || m.form != nil || len(m.groupProfileAuthQueue) == 0 {
		return nil
	}
	ref := m.groupProfileAuthQueue[0]
	m.groupProfileAuthQueue = m.groupProfileAuthQueue[1:]
	return m.openGroupProfileAuthPrompt(ref)
}

func (m *Model) retryGroupProfileMember(ref config.InstanceRef) tea.Cmd {
	if m.groupProfileAuthPrompted != nil {
		delete(m.groupProfileAuthPrompted, encodeGroupMember(ref))
	}
	m.groupProfileRetryOperation = true
	return m.runGroupProfile(m.groupProfileLaunchName, &ref)
}

func (m *Model) retryGroupProfileMemberConcurrent(ref config.InstanceRef) tea.Cmd {
	backend, ok := m.backend.(groupProfilesBackend)
	if !ok {
		return nil
	}
	name := m.groupProfileLaunchName
	attempt := m.nextGroupProfileAttempt(ref)
	m.groupProfileConcurrentRetries++
	return func() tea.Msg {
		results, err := backend.UIGroupProfileRetry(m.ctx, name, ref)
		return groupProfileRetryMsg{results: results, err: err, attempt: attempt}
	}
}

func (m *Model) nextGroupProfileAttempt(ref config.InstanceRef) int {
	if m.groupProfileResultVersion == nil {
		m.groupProfileResultVersion = map[string]int{}
	}
	key := ref.CapabilityID + "/" + ref.Name
	m.groupProfileResultVersion[key]++
	return m.groupProfileResultVersion[key]
}

func (m *Model) recordGroupProfileResult(result viewmodel.GroupProfileLaunchResult, attempt int) {
	key := result.Name
	if attempt == 0 {
		attempt = 1
	}
	if m.groupProfileResultVersion == nil {
		m.groupProfileResultVersion = map[string]int{}
	}
	if attempt < m.groupProfileResultVersion[key] {
		return
	}
	m.groupProfileResultVersion[key] = attempt
	for i := range m.groupProfileResults {
		if m.groupProfileResults[i].Name == result.Name {
			m.groupProfileResults[i] = result
			return
		}
	}
	m.groupProfileResults = append(m.groupProfileResults, result)
}

func (m *Model) recordGroupProfileProgress(event viewmodel.OperationProgress) {
	result := viewmodel.GroupProfileLaunchResult{Name: event.MemberName, Status: event.MemberStatus, Error: event.MemberError}
	m.recordGroupProfileResult(result, event.Attempt)
}

func (m *Model) openGroupProfileAuthPrompt(ref config.InstanceRef) tea.Cmd {
	backend, ok := m.backend.(setupBackend)
	if !ok {
		m.output = "Authentication input preview is unavailable"
		return nil
	}
	m.groupProfileAuthRef = &ref
	m.groupProfileAuthPending = true
	m.busy = true
	return func() tea.Msg {
		preview, err := backend.UISetupPreview(m.ctx, viewmodel.SetupRequest{Ref: ref})
		return groupProfileAuthPreviewMsg{preview: preview, ref: ref, err: err}
	}
}

func (m *Model) openMinimalGroupProfileAuthForm(preview viewmodel.SetupPreview, ref config.InstanceRef) {
	values := map[string]any{}
	for _, input := range preview.Inputs {
		if input.HasValue {
			values[input.Definition.Name] = input.Value
		}
	}
	inputs := minimalGroupProfileAuthInputs(preview.Sections, preview.Inputs, values)
	if len(inputs) == 0 {
		m.groupProfileAuthPending = false
		m.groupProfileAuthRef = nil
		m.output = "No missing visible authentication inputs are declared for this instance."
		return
	}
	defs := make([]catalog.Input, 0, len(inputs))
	names := make([]string, 0, len(inputs))
	formValues := map[string]any{}
	for _, input := range inputs {
		defs = append(defs, input.Definition)
		names = append(names, input.Definition.Name)
		if input.HasValue {
			formValues[input.Definition.Name] = input.Value
		}
	}
	m.groupProfileAuthRef = &ref
	m.form = forms.NewForm(m.ctx, defs, formValues)
	m.form.SetTitle("Authentication · " + ref.CapabilityID + " / " + ref.Name)
	m.form.SetSections(forms.FormSection{ID: "authentication", Title: "Authentication", Fields: names})
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	for _, input := range inputs {
		if input.SecretSaved {
			m.form.SetHint(input.Definition.Name, "Credential saved · leave blank to keep")
		}
	}
}

func (m *Model) saveGroupProfileAuth(values map[string]any) tea.Cmd {
	backend, ok := m.backend.(setupInputSaveBackend)
	if !ok || m.groupProfileAuthRef == nil {
		m.groupProfileAuthPending = false
		m.output = "Saving authentication inputs is unavailable"
		return nil
	}
	ref := *m.groupProfileAuthRef
	m.busy = true
	m.form = nil
	inputs := map[string]any{}
	for k, v := range values {
		inputs[k] = v
	}
	request := viewmodel.SetupInstallRequest{SetupRequest: viewmodel.SetupRequest{Ref: ref}, Inputs: inputs}
	return func() tea.Msg { return groupProfileAuthSavedMsg{ref: ref, err: backend.UISaveInputs(m.ctx, request)} }
}
