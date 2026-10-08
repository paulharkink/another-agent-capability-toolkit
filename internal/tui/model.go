package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"sort"
	"strings"
	"time"
)

// Backend uses existing domain types so app.Service can implement it without importing tui.
// UISourceLabels maps package directories to source IDs and may map source IDs to display labels.
// UIRun's target argument is the root path for the set-environment-root action.
type Backend interface {
	UICatalog(context.Context) ([]catalog.Package, error)
	UIInventory(context.Context) ([]state.Installation, error)
	UIMCPs(context.Context) ([]mcp.Instance, error)
	UIAgents(context.Context) ([]string, error)
	UISettings(context.Context) (map[string]string, error)
	UISourceLabels(context.Context) (map[string]string, error)
	UIRun(ctx context.Context, action, sourceID, packageID, profile, agentID, environment, target string) (string, error)
}

type agentManagementBackend interface {
	UIAgentManagement(context.Context) ([]viewmodel.AgentManagementRow, error)
	UIAgentConfig(context.Context, string, string) (string, error)
}
type profileRunBackend interface {
	UIProfileRun(context.Context, string, state.Key) (string, error)
}
type Model struct {
	pendingSetupItemsField     string
	capabilityProfiles         map[string]viewmodel.CapabilityProfileSnapshot
	creatingProfile            *config.ProfileRef
	home                       homeState
	backend                    Backend
	ctx                        context.Context
	view                       string
	selected, width, height    int
	catalog                    []catalog.Package
	inventory                  []state.Installation
	mcps                       []mcp.Instance
	profileSnapshot            *viewmodel.ProfileSnapshot
	profileError               error
	inventoryError             error
	environmentSnapshot        *viewmodel.EnvironmentSnapshot
	environmentError           error
	pendingRegistration        *viewmodel.RegistrationRequest
	pendingRegistrationRemoval bool
	registration               *registrationState
	pendingSetup               *viewmodel.SetupPreview
	pendingSetupField          string
	workspace                  *workspaceState
	setupRetry                 *setupRetryDraft
	setupInformation           bool
	setupInfoOffset            int
	setupOperationPending      bool
	setupOperationID           uint64
	progressStep               string
	progressOutput             string
	progressStarted            time.Time
	progressFrame              int
	progressOffset             int
	progressLastOutput         time.Time
	setupProgressEvents        <-chan viewmodel.OperationProgress
	setupProgressDone          <-chan struct{}
	logSession                 uint64
	logCancel                  context.CancelFunc
	logProfile                 state.Key
	logLabel                   string
	pendingDefaultAgents       bool
	agents                     []string
	agentManagement            []viewmodel.AgentManagementRow
	settings, sourceLabels     map[string]string
	busy                       bool
	output, action             string
	result                     *resultState
	retryOperation             func() tea.Cmd
	form                       *forms.FormModel
	unsavedExit                *unsavedExitState
	unsavedExitApplying        bool
	unsavedExitIntent          *unsavedExitState
	unsavedExitFailure         bool
	pending                    operation
	management                 managementState
}
type operation struct{ action, source, packageID, agent, environment, target, mcp, profile string }
type loadedMsg struct {
	capabilityProfiles  map[string]viewmodel.CapabilityProfileSnapshot
	catalog             []catalog.Package
	inventory           []state.Installation
	inventoryError      error
	mcps                []mcp.Instance
	profileSnapshot     *viewmodel.ProfileSnapshot
	profileError        error
	environmentSnapshot *viewmodel.EnvironmentSnapshot
	environmentError    error
	agents              []string
	agentManagement     []viewmodel.AgentManagementRow
	settings, labels    map[string]string
	err                 error
}
type agentConfigMsg struct {
	path, content string
	err           error
}
type operationMsg struct {
	origin, output string
	err            error
	failed         bool
	step, target   string
	setupID        uint64
	result         *viewmodel.OperationResult
}
type setupProgressMsg struct {
	setupID  uint64
	progress viewmodel.OperationProgress
}
type setupProgressClosedMsg struct{ setupID uint64 }
type setupProgressTickMsg struct{ setupID uint64 }
type settingsSavedMsg struct{ err error }

func New(backend Backend) tea.Model { return NewContext(context.Background(), backend) }
func NewContext(ctx context.Context, backend Backend) *Model {
	return &Model{backend: backend, ctx: ctx, view: "Catalog", width: 80, height: 24, settings: map[string]string{}, sourceLabels: map[string]string{}}
}
func (m *Model) Init() tea.Cmd { return m.load() }
func (m *Model) load() tea.Cmd {
	backend, ctx := m.backend, m.ctx
	if !m.busy && m.result == nil {
		m.action = "load"
	}
	if m.retryOperation == nil {
		m.retryOperation = func() tea.Cmd {
			m.busy = true
			m.action = "load"
			return m.load()
		}
	}
	return func() tea.Msg {
		var msg loadedMsg
		errs := []error{}
		var e error
		msg.catalog, e = backend.UICatalog(ctx)
		if e != nil {
			errs = append(errs, e)
		}
		msg.inventory, e = backend.UIInventory(ctx)
		msg.inventoryError = e
		if e != nil {
			errs = append(errs, e)
		}
		if profiles, ok := backend.(capabilityProfilesBackend); ok {
			msg.capabilityProfiles, e = profiles.UICapabilityProfiles(ctx)
			if legacy, ok := backend.(profileSnapshotBackend); ok {
				snapshot, err := legacy.UIProfileSnapshot(ctx)
				if err == nil {
					msg.profileSnapshot = &snapshot
				}
			}
		} else if profileBackend, ok := backend.(profileSnapshotBackend); ok {
			snapshot, err := profileBackend.UIProfileSnapshot(ctx)
			e = err
			msg.profileError = err
			if err == nil {
				msg.profileSnapshot = &snapshot
			}
		} else {
			msg.mcps, e = backend.UIMCPs(ctx)
		}
		if e != nil {
			errs = append(errs, e)
		}
		msg.agents, e = backend.UIAgents(ctx)
		if e != nil {
			errs = append(errs, e)
		}
		if managementBackend, ok := backend.(packAgentManagementBackend); ok {
			msg.agentManagement, e = managementBackend.UIPackAgentManagement(ctx)
			if e != nil {
				errs = append(errs, e)
			}
		} else if managementBackend, ok := backend.(agentManagementBackend); ok {
			msg.agentManagement, e = managementBackend.UIAgentManagement(ctx)
			if e != nil {
				errs = append(errs, e)
			}
		}
		if environmentBackend, ok := backend.(environmentBrowserBackend); ok && !m.profileMode() {
			snapshot, err := environmentBackend.UIEnvironmentSnapshot(ctx)
			msg.environmentError = err
			if err == nil {
				msg.environmentSnapshot = &snapshot
			} else {
				errs = append(errs, err)
			}
		}
		if pack, ok := backend.(packSettingsBackend); ok {
			msg.settings, e = pack.PackSettings(ctx)
			if msg.settings == nil {
				msg.settings = map[string]string{}
			}
			msg.settings["source"] = msg.settings["capability-pack"]
			msg.settings["checkout"] = msg.settings["pack-directory"]
			msg.settings["environment-root"] = msg.settings["profile-directory"]
			msg.settings["state-dir"] = msg.settings["state-directory"]
		} else {
			msg.settings, e = backend.UISettings(ctx)
		}
		if e != nil {
			errs = append(errs, e)
		}
		msg.labels, e = backend.UISourceLabels(ctx)
		if e != nil {
			errs = append(errs, e)
		}
		msg.err = errors.Join(errs...)
		return msg
	}
}
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch progress := msg.(type) {
	case setupProgressMsg:
		if progress.setupID != m.setupOperationID || !m.setupOperationPending {
			return m, nil
		}
		if progress.progress.Step != "" {
			m.progressStep = progress.progress.Step
		}
		m.progressOutput += m.cleanOutput(progress.progress.Output)
		if progress.progress.Output != "" {
			m.progressLastOutput = time.Now()
			if m.progressOffset > 0 {
				m.progressOffset += strings.Count(progress.progress.Output, "\n")
			}
		}
		if len(m.progressOutput) > 16*1024 {
			m.progressOutput = m.progressOutput[len(m.progressOutput)-16*1024:]
		}
		return m, waitSetupProgress(progress.setupID, m.setupProgressEvents, m.setupProgressDone, m.ctx)
	case setupProgressClosedMsg:
		return m, nil
	case setupProgressTickMsg:
		if progress.setupID != m.setupOperationID || !m.setupOperationPending {
			return m, nil
		}
		m.progressFrame++
		return m, setupProgressTick(progress.setupID)
	case operationMsg:
		if progress.setupID != 0 && progress.setupID != m.setupOperationID {
			return m, nil
		}
		return m, m.handleOperationResult(progress)
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		m.reconcileHome()
	}
	// Inventory refreshes are lifecycle events. A retained workspace form must
	// not consume them as ordinary form input after an operation completes.
	if loaded, ok := msg.(loadedMsg); ok {
		m.applyLoaded(loaded)
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		stroke := key.String()
		quitKey := stroke == "f10"
		if stroke == "q" {
			quitKey = m.form == nil || !m.form.IsEditingInput()
		}
		if quitKey {
			if m.registration != nil {
				if m.registration.HasUnsavedChanges() {
					m.openRegistrationExitPrompt(m.registration, true)
					return m, nil
				}
				if m.form != nil && m.form.HasUnsavedChanges() {
					m.unsavedExit = &unsavedExitState{reason: "quit", selected: 2}
					return m, nil
				}
				return m, tea.Quit
			}
			if m.form != nil && m.form.HasUnsavedChanges() {
				m.unsavedExit = &unsavedExitState{reason: "quit", selected: 2}
				return m, nil
			}
			return m, tea.Quit
		}
	}
	// A below-minimum resize replaces an active form with a recovery screen.
	// Keep the underlying draft, but don't let ordinary input reach controls
	// that are no longer visible. Only the recovery screen's quit keys remain.
	if m.form != nil && m.unsavedExit == nil && (m.width < 80 || m.height < 16) {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			switch event.String() {
			case "ctrl+c":
				m.discardFormAndContinue("cancel")
				return m, tea.Quit
			case "f10", "q":
				if m.form.HasUnsavedChanges() {
					m.unsavedExit = &unsavedExitState{reason: "quit", selected: 2}
					return m, nil
				}
				return m, tea.Quit
			}
			return m, nil
		case tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg, tea.MouseMsg:
			return m, nil
		}
	}
	if request, ok := msg.(forms.ExitRequestMsg); ok && m.form != nil {
		m.unsavedExit = &unsavedExitState{reason: request.Reason, selected: 2}
		return m, nil
	}
	if m.unsavedExit != nil {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			if event.String() == "ctrl+c" {
				m.discardFormAndContinue("cancel")
				return m, tea.Quit
			}
			return m, m.unsavedExitKey(event.String())
		case tea.MouseClickMsg:
			return m, m.unsavedExitMouse(event)
		case tea.WindowSizeMsg:
			return m, nil
		case operationMsg:
			return m, m.handleOperationResult(event)
		case loadedMsg:
			m.applyLoaded(event)
			return m, nil
		case settingsSavedMsg:
			m.busy = false
			return m, nil
		default:
			return m, nil
		}
	}
	// Foreground overlays own human input before any hidden form or modal gets
	// a chance to consume it. Lifecycle messages still pass through below.
	if m.result != nil {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			return m, m.resultKey(event.String())
		case tea.MouseMsg:
			return m, m.resultMouse(event)
		}
	}
	if m.home.Modal != nil && m.home.Modal.Kind == "logs" {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			return m, m.logKey(event.String())
		case tea.MouseMsg:
			return m, m.logMouse(event)
		}
	}
	if m.busy {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			if event.String() == "ctrl+c" {
				return m, tea.Quit
			}
			if m.setupOperationPending {
				maxOffset := int(^uint(0) >> 1)
				switch event.String() {
				case "up", "k":
					if m.progressOffset < maxOffset {
						m.progressOffset++
					}
				case "down", "j":
					m.progressOffset = max(0, m.progressOffset-1)
				case "pgup":
					page := max(1, m.height/2)
					m.progressOffset = min(maxOffset-page, m.progressOffset) + page
				case "pgdown":
					m.progressOffset = max(0, m.progressOffset-max(1, m.height/2))
				case "home":
					m.progressOffset = int(^uint(0) >> 1)
				case "end":
					m.progressOffset = 0
				}
			}
			return m, nil
		case tea.MouseMsg:
			return m, nil
		}
	}
	if action, ok := msg.(forms.ActionMsg); ok && action.SectionID == sectionComponentsID && action.ID == "select-all-skills" {
		m.selectAllSkills()
		return m, nil
	}
	if action, ok := msg.(forms.ActionMsg); ok && m.workspace != nil && m.workspace.Active && (action.SectionID == sectionRuntimeID || action.SectionID == sectionEndpointID) {
		if action.ID == "check-connection" && action.SectionID == sectionEndpointID && m.workspace.Profile != nil {
			profile := m.workspace.Profile
			return m, m.checkProfileConnection(ProfileRow{Key: profile.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Profile: profile})
		}
		parts := strings.SplitN(action.ID, ":", 2)
		if len(parts) == 2 {
			if parts[0] == "check-connection" {
				if profile := m.profileForMCPName(parts[1]); profile != nil {
					return m, m.checkProfileConnection(ProfileRow{Key: profile.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Profile: profile})
				}
				return m, nil
			}
			if parts[0] == "logs" {
				if profile := m.profileForMCPName(parts[1]); profile != nil {
					return m, m.openProfileLogs(ProfileRow{Key: profile.Key, URL: profile.URL, Name: profile.Name, Status: profile.RuntimeStatus, Profile: profile})
				}
				return m, nil
			}
			if parts[0] == "start" || parts[0] == "stop" {
				profile := m.profileForMCPName(parts[1])
				if profile == nil && m.workspace.Profile != nil {
					parent := *m.workspace.Profile
					profile = &parent
				}
				row := ProfileRow{}
				if profile != nil {
					row = ProfileRow{Key: profile.Key, Profile: profile, URL: profile.URL, Status: profile.RuntimeStatus}
				}
				guardAction := "s"
				if parts[0] == "stop" {
					guardAction = "x"
				}
				if reason := m.profileActionReason(row, guardAction); reason != "" {
					m.output = reason
					return m, nil
				}
				return m, m.run(operation{action: parts[0], source: m.workspace.Key.Source, packageID: m.workspace.Key.Package, environment: m.workspace.Key.Environment, target: m.workspace.Key.Target, mcp: parts[1]})
			}
		}
		return m, nil
	}
	if action, ok := msg.(forms.ActionMsg); ok && m.workspace != nil && m.workspace.Active && ((action.SectionID == sectionOverviewID || action.SectionID == sectionAgentsID) || (action.SectionID == "" && action.Section == "Overview")) {
		switch action.ID {
		case "apply":
			if m.form != nil && m.pendingSetup != nil {
				return m, m.applySetup(m.form.Values())
			}
		case "stop":
			if handled, cmd := m.workspaceOverviewAction("x"); handled {
				return m, cmd
			}
		case "agents":
			if m.form != nil {
				m.workspace.Section = "Agents"
				m.workspace.SectionID = sectionAgentsID
				m.form.SelectSectionID(sectionAgentsID)
				m.form.FocusSection()
			}
			return m, nil
		case "locate-source":
			m.locateSelectedSource()
			return m, nil
		}
		return m, nil
	}
	if m.setupInformation {
		if key, ok := msg.(tea.KeyPressMsg); ok {
			switch key.String() {
			case "esc", "f3":
				m.setupInformation = false
			case "up", "k":
				m.setupInfoOffset = max(0, m.setupInfoOffset-1)
			case "down", "j":
				m.setupInfoOffset++
			case "pgup":
				m.setupInfoOffset = max(0, m.setupInfoOffset-max(1, m.height-8))
			case "pgdown":
				m.setupInfoOffset += max(1, m.height-8)
			case "home":
				m.setupInfoOffset = 0
			}
		}
		return m, nil
	}
	if m.registration != nil {
		switch event := msg.(type) {
		case tea.KeyPressMsg:
			return m, m.registrationKey(event.String())
		case registrationCheckMsg:
			m.registration.Check = event.observation
			return m, nil
		case tea.MouseMsg:
			return m, m.registrationMouse(event)
		}
	}
	if m.form != nil {
		m.form.SetUnsavedExitGuard(true)
		if key, ok := msg.(tea.KeyPressMsg); ok {
			switch key.String() {
			case "ctrl+c":
				m.discardFormAndContinue("cancel")
				return m, tea.Quit
			case "f10":
				if m.form.HasUnsavedChanges() {
					m.unsavedExit = &unsavedExitState{reason: "quit", selected: 2}
					return m, nil
				}
				return m, tea.Quit
			}
		}
		switch event := msg.(type) {
		case logsMsg:
			return m, m.showLogs(event)
		case logPollMsg:
			if event.session == m.logSession && m.home.Modal != nil && m.home.Modal.Kind == "logs" && m.home.Modal.Follow {
				return m, m.fetchLogs(event.session)
			}
		}
		if result, ok := msg.(operationMsg); ok {
			return m, m.handleOperationResult(result)
		}
		if saved, ok := msg.(settingsSavedMsg); ok && m.unsavedExitApplying {
			m.busy = false
			m.unsavedExitApplying = false
			intent := m.unsavedExitIntent
			m.unsavedExitIntent = nil
			if saved.err != nil {
				m.form.PrepareRetry(saved.err.Error())
				m.output = saved.err.Error()
				m.showOperationResult(operationMsg{origin: m.view, err: saved.err, step: "save settings"})
				m.result.CanReturn = false
				m.result.CanRetry = false
				m.unsavedExitFailure = true
				return m, nil
			}
			m.pendingDefaultAgents = false
			m.form.MarkClean()
			return m, m.finishUnsavedExit(intent)
		}
		if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "f3" && m.pendingSetup != nil && !m.form.PickerActive() {
			if m.workspace != nil && m.workspace.Active {
				m.workspace.Section = "Information"
				m.workspace.SectionID = sectionInformationID
				m.form.SelectSectionID(sectionInformationID)
				m.form.FocusSection()
				return m, nil
			}
			m.setupInformation = true
			m.setupInfoOffset = 0
			return m, nil
		}
		if back, ok := msg.(forms.BackMsg); ok && m.workspace != nil && m.workspace.Active {
			m.workspace.cacheDraft(back.Draft)
			m.workspace.Active = false
			m.form = nil
			m.pendingSetup = nil
			m.pendingSetupField = ""
			m.output = "Back · draft kept for this target"
			return m, nil
		}
		if key, ok := msg.(tea.KeyPressMsg); ok && m.workspace != nil && m.workspace.Active && m.workspace.ObservedOnly {
			switch key.String() {
			case "g":
				m.workspace.Section = "Agents"
				m.workspace.SectionID = sectionAgentsID
				m.form.SelectSectionID(sectionAgentsID)
				m.form.FocusSection()
				return m, nil
			case "enter":
				if m.form.FocusArea() == 1 && m.form.SectionID() == sectionAgentsID {
					m.locateSelectedSource()
					return m, nil
				}
			case "s", "x":
				m.output = "Runtime lifecycle actions are unavailable from this observed profile workspace."
				return m, nil
			}
		}
		if key, ok := msg.(tea.KeyPressMsg); ok && m.workspace != nil && m.workspace.Active && m.form.SectionID() == sectionOverviewID && !m.form.PickerActive() {
			if handled, cmd := m.workspaceOverviewAction(key.String()); handled {
				return m, cmd
			}
		}
		if x, y, width, height, ok := m.setupOverlayBounds(); ok {
			switch event := msg.(type) {
			case tea.WindowSizeMsg:
				msg = tea.WindowSizeMsg{Width: width, Height: height}
			case tea.MouseClickMsg:
				if event.X < x || event.X >= x+width || event.Y < y || event.Y >= y+height {
					return m, nil
				}
				event.X -= x
				event.Y -= y
				msg = event
			case tea.MouseWheelMsg:
				if event.X < x || event.X >= x+width || event.Y < y || event.Y >= y+height {
					return m, nil
				}
				event.X -= x
				event.Y -= y
				msg = event
			}
		}
		next, cmd := m.form.Update(msg)
		m.form = next.(*forms.FormModel)
		m.refreshComponentAvailability()
		values, e := m.form.Result()
		if errors.Is(e, forms.ErrNotSubmitted) {
			if m.unsavedExitApplying {
				m.unsavedExitApplying = false
				m.unsavedExitIntent = nil
			}
			return m, cmd
		}
		if m.unsavedExitApplying && e == nil && m.pendingSetup != nil {
			cmd := m.applySetup(values)
			if cmd == nil && m.unsavedExitApplying {
				m.unsavedExitApplying = false
				m.unsavedExitIntent = nil
				m.form.PrepareRetry(m.output)
			}
			return m, cmd
		}
		if !m.unsavedExitApplying {
			if m.pendingSetup != nil {
				m.pendingSetup.ActiveInputGroups = m.form.ActiveInputGroups()
			}
			m.form = nil
			m.management.FormOverlay = false
		}
		if errors.Is(e, picker.ErrCancelled) {
			m.pendingRegistration = nil
			m.pendingRegistrationRemoval = false
			m.pendingSetup = nil
			m.pendingSetupField = ""
			m.pendingDefaultAgents = false
			m.creatingProfile = nil
			m.workspace = nil
			m.output = "Cancelled"
			return m, nil
		}
		if e != nil {
			m.unsavedExitApplying = false
			m.unsavedExitIntent = nil
			m.output = e.Error()
			return m, nil
		}
		if m.creatingProfile != nil {
			ref := *m.creatingProfile
			ref.Name, _ = values["name"].(string)
			m.creatingProfile = nil
			m.action = "create profile"
			return m, func() tea.Msg {
				return profileCreatedMsg{ref: ref, err: m.backend.(capabilityProfilesBackend).UICreateProfile(m.ctx, ref)}
			}
		}
		if m.pendingRegistration != nil {
			request := *m.pendingRegistration
			m.pendingRegistration = nil
			selected, _ := values["agent"].([]string)
			removing := m.pendingRegistrationRemoval
			m.pendingRegistrationRemoval = false
			if removing {
				if len(selected) == 0 {
					m.output = "No registrations selected for removal"
					m.home.Modal = nil
					return m, nil
				}
				removed := make(map[string]bool, len(selected))
				for _, agent := range selected {
					removed[agent] = true
				}
				remaining := make([]string, 0, len(request.AgentIDs))
				for _, agent := range request.AgentIDs {
					if !removed[agent] {
						remaining = append(remaining, agent)
					}
				}
				request.AgentIDs = remaining
			} else {
				request.AgentIDs = selected
			}
			if request.Transport == "" {
				request.Transport, _ = values["transport"].(string)
			}
			return m, m.configureRegistrations(request, removing)
		}
		if m.pendingSetup != nil {
			return m, m.applySetup(values)
		}
		if m.pendingDefaultAgents {
			selected, _ := values["agents"].([]string)
			backend := m.backend.(defaultAgentsBackend)
			ctx := m.ctx
			selected = append([]string(nil), selected...)
			m.action = "save default agents"
			m.busy = true
			m.retryOperation = func() tea.Cmd {
				m.busy = true
				return func() tea.Msg {
					return settingsSavedMsg{err: backend.UISetDefaultAgents(ctx, append([]string(nil), selected...))}
				}
			}
			return m, func() tea.Msg {
				return settingsSavedMsg{err: backend.UISetDefaultAgents(ctx, append([]string(nil), selected...))}
			}
		}
		op := m.pending
		if op.action == "set-environment-root" {
			op.target, _ = values["root"].(string)
		} else if op.action == "locate-source" {
			op.target, _ = values["root"].(string)
		} else {
			if agents, ok := values["agent"].([]string); ok {
				op.agent = strings.Join(agents, ",")
			}
			op.environment, _ = values["environment"].(string)
			op.target, _ = values["target"].(string)
		}
		return m, m.run(op)
	}
	switch msg := msg.(type) {
	case logsMsg:
		return m, m.showLogs(msg)
	case logPollMsg:
		if msg.session == m.logSession && m.home.Modal != nil && m.home.Modal.Kind == "logs" && m.home.Modal.Follow {
			return m, m.fetchLogs(msg.session)
		}
	case profileCreatedMsg:
		if msg.err != nil {
			m.showOperationResult(operationMsg{origin: m.view, err: msg.err, step: "create profile"})
			return m, nil
		}
		return m, m.openTargetWorkspace(viewmodel.SetupRequest{Ref: msg.ref}, "Overview")
	case setupPreviewMsg:
		m.busy = false
		if msg.err != nil {
			key := state.Key{}
			if m.workspace != nil {
				key = m.workspace.Key
			}
			m.showOperationResult(operationMsg{origin: m.view, err: msg.err, step: "preview", target: setupTargetLabel(key)})
			return m, nil
		}
		m.retryOperation = nil
		if m.workspace != nil && m.workspace.Reference.CapabilityID != "" {
			m.workspace.Key = msg.preview.Key
		}
		m.openSetupForm(msg.preview)
		name := msg.preview.PackageName
		if name == "" {
			name = msg.preview.Key.Package
		}
		title := "New setup · " + name
		if msg.preview.Configured || (m.workspace != nil && m.workspace.Key == msg.preview.Key && m.workspace.Existing) {
			title = "Configure · " + name
		}
		if m.profileMode() {
			title += " · Profile " + msg.preview.Key.Target
		} else if msg.preview.MCP && strings.TrimSpace(msg.preview.Key.Environment) != "" && strings.TrimSpace(msg.preview.Key.Target) != "" && msg.preview.Key.Target != "default" {
			title += " · " + msg.preview.Key.Environment + " / " + msg.preview.Key.Target
		}
		m.form.SetTitle(title + " · F3 Information")
	case loadedMsg:
		m.applyLoaded(msg)
	case agentConfigMsg:
		if msg.err != nil {
			m.action = "load agent configuration"
			m.showOperationResult(operationMsg{origin: m.view, err: msg.err, step: "load config", target: msg.path})
			return m, nil
		}
		m.management.ViewerPath = msg.path
		m.management.ViewerContent = msg.content
		m.management.ViewerOffset = 0
		m.management.ViewerHorizontal = 0
		m.management.Modal = "viewer"
	case environmentTargetMsg:
		if msg.err != nil {
			m.action = "load target configuration"
			m.showOperationResult(operationMsg{origin: m.view, err: msg.err, step: "load config", target: msg.path})
			return m, nil
		}
		m.management.ViewerPath = msg.path
		m.management.ViewerContent = msg.content
		m.management.ViewerOffset = 0
		m.management.ViewerHorizontal = 0
		m.management.Modal = "viewer"
	case operationMsg:
		return m, m.handleOperationResult(msg)
	case settingsSavedMsg:
		m.busy = false
		if msg.err != nil {
			m.showOperationResult(operationMsg{origin: m.view, err: msg.err, step: "save settings"})
			return m, nil
		}
		m.pendingDefaultAgents = false
		m.navigate("Catalog")
		m.output = "Default named agents saved for future MCP installs"
		return m, m.load()
	case tea.MouseMsg:
		if m.result != nil {
			return m, m.resultMouse(msg)
		}
		if m.view == "Catalog" && !m.busy {
			return m, m.homeMouse(msg)
		}
		if isManagementView(m.view) && !m.busy {
			return m, m.managementMouse(msg)
		}
	case tea.KeyPressMsg:
		stroke := msg.String()
		if m.result != nil {
			return m, m.resultKey(stroke)
		}
		if m.busy {
			if stroke == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.view == "Catalog" {
			return m, m.homeKey(stroke)
		}
		if isManagementView(m.view) {
			return m, m.managementKey(stroke)
		}
		switch stroke {
		case "esc":
			m.navigate("Catalog")
		case "f10", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.selected = max(0, m.selected-1)
		case "down", "j":
			m.selected = min(max(0, len(m.rows())-1), m.selected+1)
		case "f9", "m":
			m.navigate("Catalog")
			m.home.Modal = &modalState{Kind: "main"}
		case "f5", "R":
			return m, m.load()
		default:
			return m, m.handleAction(stroke)
		}
	}
	return m, nil
}

func (m *Model) applyLoaded(msg loadedMsg) {
	if m.action == "load" {
		m.busy = false
	}
	m.capabilityProfiles = msg.capabilityProfiles
	m.catalog = msg.catalog
	m.inventory = msg.inventory
	m.inventoryError = msg.inventoryError
	m.mcps = msg.mcps
	m.profileError = msg.profileError
	m.environmentError = msg.environmentError
	m.environmentSnapshot = msg.environmentSnapshot
	if msg.profileSnapshot != nil {
		m.profileSnapshot = msg.profileSnapshot
		if m.workspace != nil {
			m.workspace.ProfileSnapshot = copyProfileSnapshot(m.profileSnapshot)
			m.workspace.Profile = m.profileForWorkspace(m.workspace.Key)
			if m.workspace.ObservedOnly {
				m.refreshObservedWorkspaceFacts()
			} else if m.workspace.Preview != nil && m.form != nil {
				m.configureWorkspaceForm(*m.workspace.Preview)
			}
		}
	}
	m.agents = msg.agents
	m.agentManagement = msg.agentManagement
	m.settings = msg.settings
	m.sourceLabels = msg.labels
	if msg.err != nil {
		m.showOperationResult(operationMsg{origin: m.view, err: msg.err, step: "load"})
		m.result.CanRetry = true
	}
	m.reconcileHome()
}

func (m *Model) handleOperationResult(msg operationMsg) tea.Cmd {
	if msg.setupID != 0 && msg.setupID != m.setupOperationID {
		return nil
	}
	if msg.setupID != 0 && m.progressOutput != "" {
		if msg.output != "" {
			msg.output = m.progressOutput + "\n" + msg.output
		} else {
			msg.output = m.progressOutput
		}
	}
	m.busy = false
	if msg.setupID != 0 && msg.setupID == m.setupOperationID && m.setupRetry != nil {
		step := msg.step
		if msg.result != nil && msg.result.Step != "" {
			step = msg.result.Step
		}
		failure := ""
		if msg.err != nil {
			failure = msg.err.Error()
		} else if msg.result != nil && len(msg.result.Errors) > 0 {
			failure = strings.Join(msg.result.Errors, "\n")
		}
		m.setupRetry.step = step
		m.setupRetry.failure = failure
		if msg.err != nil || msg.failed || (msg.result != nil && len(msg.result.Errors) > 0) {
			m.setupRetry.returnValues = cloneSetupValues(m.setupRetry.values)
			if m.setupRetry.destinationField != "" {
				achieved := m.achievedDestinationIDs(m.setupRetry, msg.result)
				m.setupRetry.returnValues[m.setupRetry.destinationField] = achieved
				if m.unsavedExitIntent != nil && m.form != nil {
					m.form.ReconcileDraftField(m.setupRetry.destinationField, achieved)
				}
			}
		}
	}
	if intent := m.unsavedExitIntent; intent != nil && intent.onResult != nil {
		m.unsavedExitIntent = nil
		return intent.onResult(msg)
	}
	cancelOnly := onlyCancellation(msg.err) && !msg.failed && (msg.result == nil || len(msg.result.Errors) == 0)
	if cancelOnly {
		m.setupOperationPending = false
		m.setupProgressEvents = nil
		m.setupProgressDone = nil
		m.resetSetupProgress()
		m.setupRetry = nil
		m.output = strings.TrimSpace(msg.output)
		m.result = nil
		m.retryOperation = nil
		return m.load()
	}
	m.view = msg.origin
	succeeded := msg.err == nil && !msg.failed && (msg.result == nil || len(msg.result.Errors) == 0)
	if m.unsavedExitApplying {
		intent := m.unsavedExitIntent
		m.unsavedExitApplying = false
		m.unsavedExitIntent = nil
		m.setupOperationPending = false
		m.setupProgressEvents = nil
		m.setupProgressDone = nil
		m.resetSetupProgress()
		if succeeded {
			if m.form != nil {
				m.form.MarkClean()
			}
			m.setupRetry = nil
			m.retryOperation = nil
			return m.finishUnsavedExit(intent)
		}
		failure := strings.TrimSpace(msg.output)
		if msg.err != nil {
			failure = msg.err.Error()
		} else if msg.result != nil && len(msg.result.Errors) > 0 {
			failure = strings.Join(msg.result.Errors, "\n")
		}
		if failure == "" {
			failure = "Save and apply failed"
		}
		if m.form != nil {
			m.form.PrepareRetry(failure)
		}
		if draft := m.setupRetry; draft != nil {
			m.pendingSetup = &draft.preview
			m.pendingSetupField = draft.destinationField
		}
		m.output = failure
		m.showOperationResult(msg)
		m.result.CanReturn = false
		m.result.CanRetry = false
		m.unsavedExitFailure = true
		return nil
	}
	if succeeded {
		// Keep the retained workspace actionable after its result is dismissed.
		// applySetup consumes pendingSetup while the operation runs, so restore
		// the preview and destination field from the submitted draft on success.
		if draft := m.setupRetry; draft != nil && m.workspace != nil && m.workspace.Active && m.workspace.Key == draft.preview.Key {
			preview := draft.preview
			m.pendingSetup = &preview
			m.pendingSetupField = draft.destinationField
			if m.form != nil {
				m.form.MarkClean()
			}
		}
		m.setupRetry = nil
		m.retryOperation = nil
	}
	m.setupOperationPending = false
	m.setupProgressEvents = nil
	m.setupProgressDone = nil
	m.resetSetupProgress()
	if m.action == "check connection" && strings.Contains(strings.ToLower(msg.output), ": unreachable") {
		msg.failed = true
	}
	m.showOperationResult(msg)
	return m.load()
}

func onlyCancellation(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyCancellation(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return onlyCancellation(wrapped.Unwrap())
	}
	return errors.Is(err, picker.ErrCancelled)
}
func (m *Model) navigate(view string) {
	if view == "Help" && m.view != "Help" {
		m.management.HelpOrigin = m.view
		m.management.HelpSelected = m.selected
		m.view = view
		m.selected = 0
		m.output = ""
		m.management.Modal = ""
		return
	}
	m.view = view
	m.selected = 0
	m.output = ""
	m.management.Modal = ""
	m.management.Focus = CapabilitiesPane
	m.management.EnvironmentIndex = 0
	m.management.TargetIndex = 0
	if view == "Settings" {
		m.initSettingsDraft()
	}
	if view == "Catalog" {
		m.reconcileHome()
	}
}
func (m *Model) label(source string) string {
	if label := m.sourceLabels[source]; label != "" {
		return label
	}
	if source == "" {
		return "current Capability Pack"
	}
	return source
}
func (m *Model) rows() []string {
	rows := []string{}
	switch m.view {
	case "Catalog":
		for _, p := range m.catalog {
			name := p.Name
			if name == "" {
				name = p.ID
			}
			components := []string{}
			if p.Skill != nil {
				components = append(components, "skill")
			}
			if p.HasMCP() {
				components = append(components, "MCP")
			}
			rows = append(rows, name+" ["+strings.Join(components, " + ")+"] · "+m.label(m.sourceLabels[p.Dir]))
		}
		for _, inst := range m.inventory {
			if inst.Component == "skill" {
				rows = append(rows, "Installed "+inst.Key.Package+" · "+inst.AgentID+" · "+m.label(inst.Key.Source)+" · "+inst.Key.Environment+"/"+inst.Key.Target)
			}
		}
	case "MCPs":
		for _, inst := range m.mcps {
			rows = append(rows, inst.Name+" · "+inst.Status+" · "+m.label(inst.Key.Source)+" · "+inst.Key.Environment+"/"+inst.Key.Target+"\n    "+inst.URL)
		}
	case "Agents":
		for _, agent := range m.agents {
			rows = append(rows, agent)
		}
	case "Environments":
		rows = append(rows, "Environment directory: "+m.environmentRoot(), "Target browsing awaits the environment service.")
	case "Help":
		rows = append(rows, "Tab / Left / Right: focus home panes", "Up / Down / PageUp / PageDown / Home / End: select", "Enter / F2: Actions; Actions includes Details and Parameters", "m / F9: Main menu", "Mouse click/wheel: select/scroll", "Esc: Back; F10: Quit")
	case "Settings":
		rows = append(rows, "Environment directory: "+m.environmentRoot())
		keys := []string{}
		for key := range m.settings {
			if key != "environment_root" && key != "environment-root" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, key+": "+m.settings[key])
		}
	}
	return rows
}
func (m *Model) handleAction(stroke string) tea.Cmd {
	switch m.view {
	case "Catalog":
		if m.selected < len(m.catalog) {
			if stroke == "a" || stroke == "s" {
				p := m.catalog[m.selected]
				if !p.HasMCP() {
					m.output = "This package has no MCP server to authenticate or start."
					return nil
				}
				action := "authenticate"
				if stroke == "s" {
					action = "start"
				}
				m.pending = operation{action: action, source: m.sourceLabels[p.Dir], packageID: p.ID}
				m.contextForm()
				return nil
			}
			if stroke == "enter" || stroke == "i" {
				p := m.catalog[m.selected]
				m.pending = operation{action: "install", source: m.sourceLabels[p.Dir], packageID: p.ID}
				m.contextForm()
				return nil
			}
		} else {
			installs := []state.Installation{}
			for _, inst := range m.inventory {
				if inst.Component == "skill" {
					installs = append(installs, inst)
				}
			}
			index := m.selected - len(m.catalog)
			if index >= 0 && index < len(installs) && (stroke == "u" || stroke == "delete") {
				inst := installs[index]
				return m.run(operation{action: "uninstall", source: inst.Key.Source, packageID: inst.Key.Package, agent: inst.AgentID, environment: inst.Key.Environment, target: inst.Key.Target})
			}
		}
	case "MCPs":
		if m.selected < len(m.mcps) {
			inst := m.mcps[m.selected]
			actions := map[string]string{"enter": "status", "s": "start", "x": "stop", "a": "authenticate", "l": "logs"}
			if action := actions[stroke]; action != "" {
				if inst.Status == "external" && action != "status" {
					m.output = "External registrations support status; use profile Actions to configure registrations."
					return nil
				}
				op := operation{action: action, source: inst.Key.Source, packageID: inst.Key.Package, environment: inst.Key.Environment, target: inst.Key.Target, mcp: inst.Key.MCP, profile: inst.Key.Profile}
				return m.run(op)
			}
		}
	case "Agents":
		if m.selected < len(m.agents) && stroke == "enter" {
			return m.run(operation{action: "agent-info", agent: m.agents[m.selected]})
		}
	case "Settings":
		if m.selected == 0 && stroke == "enter" {
			m.pending = operation{action: "set-environment-root"}
			m.form = forms.NewForm(m.ctx, []catalog.Input{{Name: "root", Label: "Environment directory", Type: "directory", Required: true}}, map[string]any{"root": m.environmentRoot()})
			m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		}
	}
	return nil
}
func (m *Model) contextForm() {
	allowAll := false
	for _, p := range m.catalog {
		source := m.sourceLabels[p.Dir]
		if source == "" {
			source = m.settings["source"]
		}
		if p.ID == m.pending.packageID && source == m.pending.source && p.Skill != nil && !p.HasMCP() {
			allowAll = true
			break
		}
	}
	choices := []catalog.Choice{}
	if allowAll {
		choices = append(choices, catalog.Choice{Value: "all", Label: "All — ~/.agents/skills"})
	}
	for _, agent := range m.agents {
		if strings.EqualFold(agent, "all") {
			continue
		}
		choices = append(choices, catalog.Choice{Value: agent, Label: agent})
	}
	selected := []string{}
	if allowAll {
		selected = []string{"all"}
	} else {
		for _, configured := range strings.Split(m.settings["default_agents"], ",") {
			candidate := strings.TrimSpace(configured)
			for _, choice := range choices {
				if candidate == choice.Value {
					present := false
					for _, old := range selected {
						if old == choice.Value {
							present = true
						}
					}
					if !present {
						selected = append(selected, choice.Value)
					}
					break
				}
			}
		}
	}
	if len(selected) == 0 && len(choices) > 0 {
		selected = append(selected, choices[0].Value)
	}

	target := m.pending.target
	if m.pending.environment == "" && target == "default" {
		target = ""
	}
	defs := []catalog.Input{{Name: "environment", Label: "Environment (optional)", Type: "string"}, {Name: "target", Label: "Target (optional)", Type: "string"}}
	prefill := map[string]any{"environment": m.pending.environment, "target": target}
	if m.pending.action == "install" || m.pending.action == "uninstall" {
		defs = append([]catalog.Input{{Name: "agent", Label: "Agents", Type: "multichoice", Options: choices, Required: true}}, defs...)
		prefill["agent"] = selected
	}
	m.form = forms.NewForm(m.ctx, defs, prefill)
	m.form.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
}
func (m *Model) run(op operation) tea.Cmd {
	if op.action == "s" {
		op.action = "start"
	} else if op.action == "x" {
		op.action = "stop"
	}
	m.pending = op
	m.busy = true
	m.action = op.action
	m.resetSetupProgress()
	origin := m.view
	backend, ctx := m.backend, m.ctx
	m.retryOperation = func() tea.Cmd { return m.run(op) }
	return func() tea.Msg {
		var output string
		var err error
		if profileBackend, ok := backend.(configurationProfileRunBackend); ok && m.profileMode() && (op.action == "start" || op.action == "stop" || op.action == "prepare" || op.action == "authenticate") {
			output, err = profileBackend.UIConfigurationProfileRun(ctx, op.action, config.ProfileRef{PackID: op.source, CapabilityID: op.packageID, Name: op.target}, firstNonempty(op.mcp, op.profile))
		} else if op.mcp != "" || op.profile != "" {
			profileBackend, ok := backend.(profileRunBackend)
			if !ok {
				return operationMsg{origin: origin, err: errors.New("backend does not support MCP-specific runtime actions"), target: op.target}
			}
			output, err = profileBackend.UIProfileRun(ctx, op.action, state.Key{Source: op.source, Package: op.packageID, Environment: op.environment, Target: op.target, MCP: op.mcp, Profile: op.profile})
		} else {
			output, err = backend.UIRun(ctx, op.action, op.source, op.packageID, op.profile, op.agent, op.environment, op.target)
		}
		return operationMsg{origin: origin, output: output, err: err, target: op.target}
	}
}

func (m *Model) resetSetupProgress() {
	m.progressStep = ""
	m.progressOutput = ""
	m.progressStarted = time.Time{}
	m.progressLastOutput = time.Time{}
	m.progressFrame = 0
	m.progressOffset = 0
	m.setupProgressEvents = nil
	m.setupProgressDone = nil
}
func (m *Model) cleanOutput(output string) string {
	for _, p := range m.catalog {
		for _, input := range p.Inputs {
			if input.Type == "secret" {
				if value, ok := input.Default.(string); ok && value != "" {
					output = strings.ReplaceAll(output, value, "[redacted]")
				}
			}
		}
	}
	return output
}

func (m *Model) setupOverlayBounds() (x, y, width, height int, ok bool) {
	if m.workspace != nil && m.workspace.Active && m.workspace.ObservedOnly {
		return managementFormOverlayBounds(m.width, m.height)
	}
	if m.management.FormOverlay {
		return managementFormOverlayBounds(m.width, m.height)
	}
	if (m.pendingSetup == nil && m.setupRetry == nil) || (m.view != "Catalog" && !isManagementView(m.view)) || m.width < 80 || m.height < 16 {
		return 0, 0, 0, 0, false
	}
	// At the supported minimum, reclaim the navigation margins so the form's
	// fixed actions remain inside the parent frame.
	if m.height-6 < 12 {
		width, height := m.width-8, m.height-4
		return (m.width - width) / 2, 4, width, height, true
	}
	return 4, 4, m.width - 8, m.height - 6, true
}

func (m *Model) setupOverlayView() tea.View {
	return m.setupOverlayViewContent(m.form.View().Content)
}

func (m *Model) setupOverlayViewContent(content string) tea.View {
	x, y, width, height, _ := m.setupOverlayBounds()
	baseView := m.homeView()
	if isManagementView(m.view) {
		baseView = m.managementView()
	}
	base := strings.Split(baseView.Content, "\n")
	overlay := strings.Split(content, "\n")
	for row := 0; row < height && row < len(overlay) && y+row < len(base); row++ {
		line := base[y+row]
		base[y+row] = ansi.Cut(line, 0, x) + fit(overlay[row], width) + ansi.Cut(line, x+width, m.width)
	}
	v := tea.NewView(navyCanvas(strings.Join(base, "\n")))
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) View() (v tea.View) {
	defer func() {
		// Bubble Tea owns entering and restoring terminal modes when requested
		// on the active view. Keep these properties consistent across overlays
		// so exit restores the caller's screen, cursor, and mouse state.
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
	}()
	if m.unsavedExit != nil && (m.form != nil || m.registration != nil) {
		return m.exitPopupView()
	}
	if m.form != nil && (m.width < 80 || m.height < 16) {
		if m.busy {
			return m.progressView()
		}
		// The resize recovery screen owns the terminal until the main-screen
		// minimum is restored. Keep any pending result/lifecycle state intact.
		return m.homeView()
	}
	if m.result != nil {
		return m.resultView()
	}
	if m.setupInformation {
		return m.setupInformationView()
	}
	if m.busy {
		return m.progressView()
	}
	if m.home.Modal != nil && m.home.Modal.Kind == "logs" {
		return m.homeView()
	}
	if m.registration != nil {
		parent := m.homeView()
		if isManagementView(m.view) {
			parent = m.managementView()
		}
		lines := m.registrationOverlay(strings.Split(parent.Content, "\n"))
		v := tea.NewView(navyCanvas(strings.Join(lines, "\n")))
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	if m.form == nil && (m.view == "Catalog" || m.width < 80 || m.height < 16) {
		return m.homeView()
	}
	if m.form != nil {
		if m.width < 80 || m.height < 16 {
			if isManagementView(m.view) {
				return m.managementView()
			}
			return m.homeView()
		}
		if _, _, _, _, ok := m.setupOverlayBounds(); ok {
			return m.setupOverlayView()
		}
		return m.form.View()
	}
	if isManagementView(m.view) {
		return m.managementView()
	}
	title := lipgloss.NewStyle().Bold(true).Render("AACT · " + m.view)
	nav := "Esc Back · m Main menu"
	rows := m.rows()
	if len(rows) == 0 {
		rows = []string{"No entries"}
	}
	maxRows := m.height - 10
	if maxRows < 1 {
		maxRows = 1
	}
	start := 0
	if m.selected >= maxRows {
		start = m.selected - maxRows + 1
	}
	end := start + maxRows
	if end > len(rows) {
		end = len(rows)
	}
	shown := []string{}
	for i := start; i < end; i++ {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		shown = append(shown, prefix+rows[i])
	}
	footer := "↑↓ Select · Esc Back · F5 Refresh · F10 Quit"
	switch m.view {
	case "Catalog":
		footer += "\nEnter install · a authenticate MCP · s start MCP · u uninstall installed skill"
	case "MCPs":
		if m.selected < len(m.mcps) && m.mcps[m.selected].Status == "external" {
			footer += "\nEnter status · registration changes available from home Actions"
		} else {
			footer += "\nEnter status · s start · x stop · a authenticate · l logs"
		}
	case "Agents":
		footer += "\nEnter agent details"
	case "Settings":
		footer += "\nEnter edit environment directory"
	}
	status := m.output
	if m.busy {
		status = "Running " + m.action + "…"
	}
	content := fmt.Sprintf("%s\n%s\n\n%s\n\n%s\n\n%s", title, nav, strings.Join(shown, "\n"), status, footer)
	width := m.width
	if width < 20 {
		width = 20
	}
	return tea.NewView(lipgloss.NewStyle().MaxWidth(width).Render(content))
}
