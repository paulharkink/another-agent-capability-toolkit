package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestManifestSectionsPreserveDeclaredOrderAndDoNotInferFieldGroups(t *testing.T) {
	preview := viewmodel.SetupPreview{
		Key: state.Key{Package: "arbitrary-name"}, HasManifestUI: true,
		Sections: []catalog.Section{{ID: "connection", Title: "Remote Control", Fields: []string{"endpoint"}}, {ID: "options", Title: "Extra Options", Fields: []string{"credential"}}},
	}
	defs := []catalog.Input{{Name: "credential", Label: "Token", Type: "secret"}, {Name: "endpoint", Label: "Endpoint", Type: "string"}, {Name: "__agents", Type: "multichoice"}}
	got := workspaceFormSections(preview, defs, "__agents")
	if len(got) != 3 || got[0].Title != "Inputs" || got[1].Title != "Agent access" || got[2].Title != "Information" {
		t.Fatalf("staged workspace sections did not group manifest settings under Inputs: %#v", got)
	}
	if len(got[0].Fields) != 2 || got[0].Fields[0] != "credential" || got[0].Fields[1] != "endpoint" {
		t.Fatalf("Inputs did not preserve declared input order: %#v", got)
	}
}

type zeroInputSkillSetupBackend struct {
	fixtureBackend
	installRequest *viewmodel.SetupInstallRequest
	installed      bool
	mcp            bool
}

type skillOnlyInstanceBackend struct{ *zeroInputSkillSetupBackend }

func (skillOnlyInstanceBackend) UICapabilityInstances(context.Context) (map[string]viewmodel.CapabilityInstanceSnapshot, error) {
	return map[string]viewmodel.CapabilityInstanceSnapshot{}, nil
}
func (skillOnlyInstanceBackend) UICreateInstance(context.Context, config.InstanceRef) error {
	return nil
}
func (b *zeroInputSkillSetupBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	return viewmodel.SetupPreview{Key: setupProfileKey(request), MCP: b.mcp, PackageName: request.Ref.CapabilityID, Destinations: []viewmodel.SetupDestination{{ID: "codex", Name: "Codex", Selected: b.installed, Detected: true}}}, nil
}
func (b *zeroInputSkillSetupBackend) UIInstall(_ context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.installRequest = &request
	b.installed = len(request.DestinationIDs) > 0
	return viewmodel.OperationResult{Saved: true}, nil
}

func TestManifestlessSectionsUseInputOrderAndOnlyMCPAddsRuntimeSections(t *testing.T) {
	preview := viewmodel.SetupPreview{Key: state.Key{Package: "arbitrary-name"}}
	defs := []catalog.Input{{Name: "token", Label: "Access token", Type: "secret"}, {Name: "host", Label: "Host", Type: "string"}}
	got := workspaceFormSections(preview, defs, "__agents")
	if len(got) != 3 || got[0].Title != "Inputs" || got[0].Fields[0] != "token" || got[0].Fields[1] != "host" {
		t.Fatalf("legacy inputs were not kept in declared order: %#v", got)
	}
	preview.MCP = true
	got = workspaceFormSections(preview, defs, "__agents")
	if len(got) != 5 || got[2].Title != "Runtime" || got[3].Title != "Logs" {
		t.Fatalf("MCP runtime sections missing: %#v", got)
	}
	for _, section := range workspaceFormSections(preview, nil, "__agents") {
		if section.ID == sectionInputsID {
			t.Fatal("zero-input MCP capability received an empty Inputs menu")
		}
	}
}

func TestDestinationPresentationUsesSkillPathsAndRetainsMCPConfigPaths(t *testing.T) {
	destination := viewmodel.SetupDestination{ID: "codex", Path: "/home/test/.codex/skills", SkillsPath: "/home/test/.codex/skills", ConfigPath: "/home/test/.codex/config.toml"}
	skillPreview := viewmodel.SetupPreview{Destinations: []viewmodel.SetupDestination{
		{ID: "all", Path: "/home/test/.agents/skills", SkillsPath: "/home/test/.agents/skills", Selected: true},
		{ID: "generic", Path: "/home/test/.agents/skills", SkillsPath: "/home/test/.agents/skills"},
		destination,
	}}
	if got := destinationDisplayPath(skillPreview, destination); got != destination.Path {
		t.Fatalf("skill-only destination displayed MCP config path %q; want skill directory %q", got, destination.Path)
	}
	if got := len(workspaceDestinations(skillPreview)); got != 3 {
		t.Fatalf("destination identities were silently merged: retained %d of %d", got, len(skillPreview.Destinations))
	}
	lines := strings.Join(workspaceInformationLines(skillPreview, 120), "\n")
	if !strings.Contains(lines, destination.Path) || strings.Contains(lines, destination.ConfigPath) {
		t.Fatalf("skill-only Information shows the wrong destination path:\n%s", lines)
	}

	mcpPreview := skillPreview
	mcpPreview.MCP = true
	if got := destinationDisplayPath(mcpPreview, destination); !strings.Contains(got, destination.Path) || !strings.Contains(got, destination.ConfigPath) {
		t.Fatalf("MCP destination did not preserve both skill and config paths: %q", got)
	}
}

func TestPlainSkillAgentActionsUseDefaultInstanceWorkspace(t *testing.T) {
	for _, scenario := range []struct {
		label     string
		installed bool
		mcp       bool
	}{
		{label: "plain skill keyboard install"},
		{label: "plain skill mouse uninstall", installed: true},
		{label: "MCP-backed keyboard install", mcp: true},
		{label: "MCP-backed mouse uninstall", installed: true, mcp: true},
	} {
		t.Run(scenario.label, func(t *testing.T) {
			m, _ := homeFixture()
			m.Update(tea.WindowSizeMsg{Width: 120, Height: 42})
			backend := &zeroInputSkillSetupBackend{installed: scenario.installed, mcp: scenario.mcp}
			m.backend = skillOnlyInstanceBackend{backend}
			var request viewmodel.SetupRequest
			if scenario.mcp {
				request = viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: "one", CapabilityID: "cluster", Name: "default"}}
				m.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{}
				cmd := m.openTargetWorkspace(request, "Agents")
				if cmd == nil {
					t.Fatal("opening the MCP-backed instance did not open setup workspace")
				}
				m.Update(cmd())
			} else {
				selectPlainSkill(t, m)
				m.home.Focus = ProfilesPane
				m.home.Context.Index = 0
				cmd := m.openContextRow()
				if cmd == nil {
					t.Fatal("opening the default instance did not open setup workspace")
				}
				m.Update(cmd())
				request = viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: "one", CapabilityID: "plain", Name: "default"}}
			}
			if m.workspace == nil || m.workspace.Key.Target != "default" || m.pendingSetup == nil || m.form.SectionID() != sectionAgentsID {
				t.Fatalf("Agent Access did not bind the default Capability Instance: workspace=%+v pending=%+v section=%q", m.workspace, m.pendingSetup, m.form.SectionID())
			}
			if m.pendingSetup.MCP != scenario.mcp {
				t.Fatalf("fixture did not exercise expected capability kind: MCP=%t want %t", m.pendingSetup.MCP, scenario.mcp)
			}
			var actionCmd tea.Cmd
			if scenario.installed {
				// Click the visible button using screen coordinates through the
				// same overlay translation used by the running TUI.
				plain := ansi.Strip(m.form.View().Content)
				button := "[ Uninstall selected ]"
				x, y, _, _, ok := m.setupOverlayBounds()
				if !ok {
					t.Fatal("Agent Access overlay has no click bounds")
				}
				for lineNo, line := range strings.Split(plain, "\n") {
					if at := strings.Index(line, button); at >= 0 {
						_, actionCmd = m.Update(tea.MouseClickMsg{X: x + at, Y: y + lineNo, Button: tea.MouseLeft})
						break
					}
				}
				if actionCmd == nil {
					t.Fatalf("mouse could not activate %q:\n%s", button, plain)
				}
			} else {
				for _, key := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeySpace}} {
					_, _ = m.Update(key)
				}
				if got, _ := m.form.Values()[m.pendingSetupField].([]string); len(got) != 1 || got[0] != "codex" {
					t.Fatalf("keyboard could not select destination through focused Agent Access form: field=%q area=%d values=%+v view:\n%s", m.pendingSetupField, m.form.FocusArea(), m.form.Values(), ansi.Strip(m.View().Content))
				}
				for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyEnter}} {
					_, actionCmd = m.Update(key)
					if key.Code != tea.KeyEnter && actionCmd != nil {
						m.Update(actionCmd)
					}
				}
				if actionCmd == nil {
					t.Fatalf("keyboard could not reach Install selected:\n%s", ansi.Strip(m.form.View().Content))
				}
			}
			_, operationCmd := m.Update(actionCmd())
			if operationCmd == nil {
				t.Fatalf("Agent Access action was not dispatched to install: %q", m.output)
			}
			m.Update(runTeaCmd(t, m, operationCmd))
			wantCapability := "plain"
			if scenario.mcp {
				wantCapability = "cluster"
			}
			if backend.installRequest == nil || backend.installRequest.Ref.PackID != "one" || backend.installRequest.Ref.CapabilityID != wantCapability || backend.installRequest.Ref.Name != "default" {
				t.Fatalf("agent action bypassed the ordinary Pack-owned default instance: request=%+v output=%q pending=%+v form=%v", backend.installRequest, m.output, m.pendingSetup, m.form != nil)
			}
			if scenario.installed && len(backend.installRequest.DestinationIDs) != 0 {
				t.Fatalf("uninstall left selected agents installed: %+v", backend.installRequest)
			}
			if !scenario.installed && (len(backend.installRequest.DestinationIDs) != 1 || backend.installRequest.DestinationIDs[0] != "codex") {
				t.Fatalf("install did not submit selected agent: %+v", backend.installRequest)
			}
		})
	}
}

func selectPlainSkill(t *testing.T, m *Model) {
	t.Helper()
	m.agents = []string{"codex", "opencode"}
	m.capabilityInstances = map[string]viewmodel.CapabilityInstanceSnapshot{"plain": {Instances: []viewmodel.CapabilityInstance{{
		Ref: config.InstanceRef{PackID: "one", CapabilityID: "plain", Name: "default"},
		Key: state.Key{Source: "one", Package: "plain", Target: "default"}, ConfigStatus: "configured",
		Components: []viewmodel.ComponentStatus{{AgentID: "codex", Kind: "skill", Name: "plain", Status: "absent"}},
	}}}}
	for index, capability := range m.capabilities() {
		if capability.Package == "plain" {
			m.home.Capabilities.ID = capability.ID
			m.home.Capabilities.Index = index
			m.home.Focus = CapabilitiesPane
			m.reconcileHome()
			return
		}
	}
	t.Fatal("plain skill capability missing")
}

func firstSelectableAgent(t *testing.T, m *Model) string {
	t.Helper()
	for _, agent := range m.agents {
		if agent != "" && agent != "all" {
			return agent
		}
	}
	t.Fatal("no selectable agent destination")
	return ""
}

func assertAgentTargetSelector(t *testing.T, m *Model) {
	t.Helper()
	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "Target agents") {
		t.Fatalf("agent target selector label missing from rendered view:\n%s", content)
	}
	if strings.Contains(content, "All agents") {
		t.Fatalf("selector unexpectedly offers a broadcast destination:\n%s", content)
	}
}

func TestFixedManifestControllerExposesRequiredEditableInputWithoutSubmittingController(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	preview := viewmodel.SetupPreview{
		Key: state.Key{Source: "one", Package: "fixture"}, HasManifestUI: true,
		Sections: []catalog.Section{{ID: "credentials", Title: "Credential Settings", Fields: []string{"credential"}}},
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "mode", Type: "choice", Options: []catalog.Choice{{Value: "one"}, {Value: "two"}}}, Value: "one", HasValue: true, Editable: false},
			{Definition: catalog.Input{Name: "credential", Label: "Credential", Type: "secret", Required: true, VisibleWhen: map[string]any{"mode": "one"}}, Editable: true},
		},
	}
	m.openSetupForm(preview)
	if m.form == nil || !m.form.HasSection("Inputs") || !strings.Contains(ansi.Strip(m.form.View().Content), "Credential") {
		t.Fatal("fixed controller hid its required editable field from the Inputs stage")
	}
	if _, submitted := m.form.Values()["mode"]; submitted {
		t.Fatal("fixed controller leaked into editable answers")
	}
	_, _ = m.form.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if _, err := m.form.Result(); err == nil || !strings.Contains(ansi.Strip(m.form.View().Content), "is required") {
		t.Fatalf("empty visible required input did not block save: err=%v view=%s", err, ansi.Strip(m.form.View().Content))
	}
}

func TestDeclaredSectionIDCannotOverwriteBuiltinAgentsWithSameName(t *testing.T) {
	preview := viewmodel.SetupPreview{HasManifestUI: true, Sections: []catalog.Section{{ID: "tool:agents", Title: "Agents", Fields: []string{"endpoint"}}}}
	sections := workspaceFormSections(preview, []catalog.Input{{Name: "endpoint", Type: "string"}}, "")
	foundBuiltin, foundInput := false, false
	for _, section := range sections {
		if section.ID == sectionAgentsID {
			foundBuiltin = section.Title == "Agent access" && len(section.Fields) == 0
		}
		if section.ID == sectionInputsID {
			foundInput = section.Title == "Inputs" && len(section.Fields) == 1 && section.Fields[0] == "endpoint"
		}
	}
	if !foundBuiltin || !foundInput {
		t.Fatalf("declared ID/title collision overwrote a staged tool section: %#v", sections)
	}
}

func TestInformationShowsManifestDeclaredValuesAndRawTOML(t *testing.T) {
	preview := viewmodel.SetupPreview{
		Key:        state.Key{Source: "one", Package: "secure"},
		TargetTOML: "[auth]\ntoken = \"raw-secret\"\nendpoint = \"https://example.test\"\n",
		Inputs: []viewmodel.SetupInput{
			{Definition: catalog.Input{Name: "token", Label: "Access token", Type: "secret"}, Value: "resolved-secret", HasValue: true, Provenance: "target"},
			{Definition: catalog.Input{Name: "endpoint", Label: "Endpoint", Type: "string"}, Value: "https://example.test", HasValue: true, Provenance: "target"},
		},
	}
	lines := workspaceInformationLines(preview, 120)
	view := strings.Join(lines, "\n")
	for _, want := range []string{"token = \"raw-secret\"", "Access token: resolved-secret", "https://example.test"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Information omitted declared manifest configuration %q:\n%s", want, view)
		}
	}
}

type multiMCPRuntimeBackend struct {
	zeroInputSkillSetupBackend
	calledAction string
	calledKey    state.Key
	checkedURL   string
	checkedType  string
}

type multiMCPInstallBackend struct {
	multiMCPRuntimeBackend
	installRequest *viewmodel.SetupInstallRequest
}

func (b *multiMCPInstallBackend) UIInstall(_ context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.installRequest = &request
	return viewmodel.OperationResult{Saved: true}, nil
}

func (b *multiMCPRuntimeBackend) UISetupPreview(_ context.Context, request viewmodel.SetupRequest) (viewmodel.SetupPreview, error) {
	return viewmodel.SetupPreview{
		Key: setupProfileKey(request),
		MCP: true, MCPDefinitions: []catalog.MCP{{Name: "alpha"}, {Name: "beta"}},
	}, nil
}

func (b *multiMCPRuntimeBackend) UIProfileRun(_ context.Context, action string, key state.Key) (string, error) {
	b.calledAction, b.calledKey = action, key
	return "ok", nil
}

func (b *multiMCPRuntimeBackend) CheckConnection(_ context.Context, url, transport string) viewmodel.ConnectionObservation {
	b.checkedURL, b.checkedType = url, transport
	return viewmodel.ConnectionObservation{URL: url, Reachable: true}
}

func TestRuntimeControlsAddressTheSelectedManifestMCPChild(t *testing.T) {
	backend := &multiMCPRuntimeBackend{}
	m := NewContext(t.Context(), backend)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
	m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{
		{Key: state.Key{Source: "one", Package: "bundle", Target: "staging", MCP: "alpha"}, Name: "Alpha", RuntimeStatus: "running", Ownership: "local", CanStart: true, CanStop: true},
		{Key: state.Key{Source: "one", Package: "bundle", Target: "staging", MCP: "beta"}, Name: "Beta", RuntimeStatus: "running", Ownership: "local", CanStop: true},
	}}
	open := m.openTargetWorkspace(viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: "one", CapabilityID: "bundle", Name: "staging"}}, "Overview")
	if open == nil {
		t.Fatal("workspace preview did not start")
	}
	m.Update(open())
	m.workspace.Preview.Configured = true
	m.workspace.Installed = true
	m.workspace.RegisteredMCPs = map[string]bool{"alpha": true, "beta": true}
	m.configureWorkspaceForm(*m.workspace.Preview)
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	if !strings.Contains(ansi.Strip(m.View().Content), "beta · status: running") {
		t.Fatalf("Runtime section omitted per-child observations: %s", ansi.Strip(m.View().Content))
	}
	_, run := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "stop:beta"})
	if run == nil {
		t.Fatal("selected child Stop action did not dispatch")
	}
	m.Update(run())
	if backend.calledAction != "stop" || backend.calledKey.MCP != "beta" || backend.calledKey.Target != "staging" {
		t.Fatalf("runtime action lost exact child identity: action=%q key=%+v", backend.calledAction, backend.calledKey)
	}
	_, start := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:alpha"})
	if start == nil {
		t.Fatalf("known-local child Start was blocked: %q", m.output)
	}
	m.Update(start())
	if backend.calledAction != "start" || backend.calledKey.MCP != "alpha" {
		t.Fatalf("known-local child Start lost its exact identity: action=%q key=%+v", backend.calledAction, backend.calledKey)
	}
}

func TestMissingNamedChildInheritsParentOwnershipForLifecycleGuards(t *testing.T) {
	for _, test := range []struct{ owner, want string }{{"other-aact", "another installation"}, {"unknown", "unknown"}} {
		t.Run(test.owner, func(t *testing.T) {
			backend := &multiMCPInstallBackend{}
			m := NewContext(t.Context(), backend)
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
			parentKey := state.Key{Source: "one", Package: "bundle", Target: "staging"}
			m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{{Key: parentKey, Name: "Bundle", RuntimeStatus: "running", Ownership: test.owner, URL: "https://parent.example/mcp"}}}
			open := m.openTargetWorkspace(viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: "one", CapabilityID: "bundle", Name: "staging"}}, "Overview")
			if open == nil {
				t.Fatal("workspace preview did not start")
			}
			m.Update(open())
			m.workspace.Preview.Configured = true
			m.workspace.Installed = true
			m.configureWorkspaceForm(*m.workspace.Preview)
			m.form.SelectSectionID(sectionRuntimeID)
			m.form.FocusSection()
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "Start alpha") || !strings.Contains(strings.ToLower(view), test.want) {
				t.Fatalf("unobserved child Start was enabled under parent owner %q: %s", test.owner, view)
			}
			_, run := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "start:alpha"})
			if run != nil || backend.calledAction != "" {
				t.Fatalf("forged child Start action reached runtime backend: cmd=%v action=%q", run != nil, backend.calledAction)
			}
			if !strings.Contains(strings.ToLower(m.output), test.want) {
				t.Fatalf("blocked child Start did not explain inherited ownership: %q", m.output)
			}
			handled, shortcut := m.workspaceOverviewAction("s")
			if !handled || shortcut != nil || backend.installRequest != nil || backend.calledAction != "" {
				t.Fatalf("overview Start shortcut performed a capability apply or runtime call: handled=%t cmd=%v request=%+v action=%q", handled, shortcut != nil, backend.installRequest, backend.calledAction)
			}
			if !strings.Contains(strings.ToLower(m.output), test.want) {
				t.Fatalf("overview Start shortcut did not explain inherited ownership: %q", m.output)
			}
		})
	}
}

func TestRuntimeConnectionCheckUsesExactManifestMCPChild(t *testing.T) {
	backend := &multiMCPRuntimeBackend{}
	m := NewContext(t.Context(), backend)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
	m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{
		{Key: state.Key{Source: "one", Package: "bundle", Target: "staging", MCP: "alpha"}, Name: "Alpha", RuntimeStatus: "running", Ownership: "foreign", URL: "https://alpha.example/mcp", Transport: "streamable-http"},
		{Key: state.Key{Source: "one", Package: "bundle", Target: "staging", MCP: "beta"}, Name: "Beta", RuntimeStatus: "running", Ownership: "foreign", URL: "https://beta.example/mcp", Transport: "sse"},
	}}
	open := m.openTargetWorkspace(viewmodel.SetupRequest{Ref: config.InstanceRef{PackID: "one", CapabilityID: "bundle", Name: "staging"}}, "Overview")
	if open == nil {
		t.Fatal("workspace preview did not start")
	}
	m.Update(open())
	m.form.SelectSectionID(sectionRuntimeID)
	m.form.FocusSection()
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Check beta connection") {
		t.Fatalf("Runtime does not expose child-specific diagnostics: %s", view)
	}
	_, check := m.Update(forms.ActionMsg{SectionID: sectionRuntimeID, ID: "check-connection:beta"})
	if check == nil {
		t.Fatal("selected child connection action did not dispatch")
	}
	m.Update(check())
	if backend.checkedURL != "https://beta.example/mcp" || backend.checkedType != "sse" {
		t.Fatalf("connection action did not preserve selected child endpoint/transport: %q %q", backend.checkedURL, backend.checkedType)
	}
}

func TestMultiMCPExternalAttachRequiresEveryForeignChildEndpoint(t *testing.T) {
	for _, test := range []struct {
		name        string
		betaOwner   string
		wantBlocked bool
	}{
		{name: "foreign sibling without endpoint", betaOwner: "other-aact", wantBlocked: true},
		{name: "local sibling starts normally", betaOwner: "local"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &multiMCPInstallBackend{}
			m := NewContext(t.Context(), backend)
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 42})
			key := state.Key{Source: "one", Package: "bundle", Target: "staging"}
			m.instanceSnapshot = &viewmodel.InstanceSnapshot{Profiles: []viewmodel.Instance{
				{Key: key, Name: "Bundle", Ownership: "local", URL: "http://127.0.0.1:7777/mcp"},
				{Key: state.Key{Source: key.Source, Package: key.Package, Environment: key.Environment, Target: key.Target, MCP: "alpha"}, Name: "Alpha", Ownership: "other-aact", URL: "https://alpha.example/mcp"},
				{Key: state.Key{Source: key.Source, Package: key.Package, Environment: key.Environment, Target: key.Target, MCP: "beta"}, Name: "Beta", Ownership: test.betaOwner},
			}}
			open := m.openTargetWorkspace(m.packInstanceRequest(key), "Overview")
			if open == nil {
				t.Fatal("workspace preview did not start")
			}
			m.Update(open())
			cmd := m.applySetup(m.form.Values())
			if test.wantBlocked {
				if cmd != nil || backend.installRequest != nil || !strings.Contains(m.output, "MCP \"beta\"") || !strings.Contains(m.output, "no observed endpoint URI") {
					t.Fatalf("incomplete foreign attachment was not rejected for the exact child: cmd=%t request=%+v output=%q", cmd != nil, backend.installRequest, m.output)
				}
				return
			}
			if cmd == nil {
				t.Fatalf("local companion was blocked: %q", m.output)
			}
			m.Update(runTeaCmd(t, m, cmd))
			if backend.installRequest == nil || backend.installRequest.ExternalURLs["alpha"] != "https://alpha.example/mcp" {
				t.Fatalf("external child endpoint was not keyed exactly: %+v", backend.installRequest)
			}
			if _, found := backend.installRequest.ExternalURLs["beta"]; found {
				t.Fatalf("local companion received a broadcast external URL: %+v", backend.installRequest.ExternalURLs)
			}
		})
	}
}

func TestPlainSkillActionsOfferOrdinaryAgentAccessOnly(t *testing.T) {
	m, _ := homeFixture()
	m.backend = skillOnlyInstanceBackend{&zeroInputSkillSetupBackend{}}
	selectPlainSkill(t, m)
	m.home.Focus = CapabilitiesPane
	m.home.Modal = &modalState{Kind: "actions"}
	actions := map[string]string{}
	for _, item := range m.homeMenuItems() {
		actions[item.Action] = item.Label
	}
	if label, exists := actions["agents"]; !exists || label != "Agent access…" {
		t.Errorf("plain-skill instance lacks ordinary Agent Access: %#v", actions)
	}
	for _, forbidden := range []string{"install-skill-only", "uninstall-skill-only", "create-profile"} {
		if _, exists := actions[forbidden]; exists {
			t.Errorf("plain-skill actions retain bypass %q: %#v", forbidden, actions)
		}
	}
}
