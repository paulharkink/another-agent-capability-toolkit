package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type profileBackend struct {
	snapshotErr error
	fixtureBackend
	snapshot     viewmodel.ProfileSnapshot
	request      *viewmodel.RegistrationRequest
	legacyCalled bool
	result       viewmodel.OperationResult
	err          error
	checkedURL   string
	checkedType  string
}

func (b *profileBackend) UICatalog(context.Context) ([]catalog.Package, error) {
	return []catalog.Package{{ID: "plain", Name: "Plain", Dir: "/catalog/plain", MCP: &catalog.MCP{Transport: "streamable-http"}, Skill: &catalog.Skill{Name: "companion"}}}, nil
}
func (b *profileBackend) UIMCPs(context.Context) ([]mcp.Instance, error) {
	b.legacyCalled = true
	return nil, errors.New("legacy inventory must not be used")
}
func (b *profileBackend) UIProfileSnapshot(context.Context) (viewmodel.ProfileSnapshot, error) {
	return b.snapshot, b.snapshotErr
}
func (b *profileBackend) UIConfigureRegistrations(_ context.Context, q viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	b.request = &q
	return b.result, b.err
}
func (b *profileBackend) CheckConnection(_ context.Context, url, transport string) viewmodel.ConnectionObservation {
	b.checkedURL, b.checkedType = url, transport
	return viewmodel.ConnectionObservation{URL: url, Reachable: true, CheckedAt: time.Now().UTC()}
}

func TestCheckConnectionActionObservesSelectedEndpoint(t *testing.T) {
	m, b := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	cmd := openProfileAction(t, m, "Check connection", false)
	if cmd == nil || !m.busy {
		t.Fatal("Check connection did not run")
	}
	m.Update(cmd())
	if b.checkedURL != "http://127.0.0.1:8765/mcp" || b.checkedType != "streamable-http" || b.request != nil {
		t.Fatalf("wrong connection observation: url=%q transport=%q registration=%+v", b.checkedURL, b.checkedType, b.request)
	}
	if !strings.Contains(m.output, "reachable") || !strings.Contains(m.output, b.checkedURL) {
		t.Fatalf("connection result is missing: %s", m.output)
	}
}
func typedProfileFixture() (*Model, *profileBackend) {
	b := &profileBackend{snapshot: viewmodel.ProfileSnapshot{Profiles: []viewmodel.Profile{
		{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "saved"}, Name: "saved", RuntimeStatus: "never-started", Ownership: "local", CanStart: true},
		{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}, Name: "foreign", RuntimeStatus: "running", Ownership: "other-aact", URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http", RegisteredAgents: []string{"claude"}, CanConfigureRegistrations: true, StartDisabledReason: "Owned by another installation", StopDisabledReason: "Owned by another installation"},
		{Key: state.Key{Source: "another-source", Package: "plain", Target: "unrelated"}, RuntimeStatus: "running"},
	}}}
	m := NewContext(context.Background(), b)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.Update(m.Init()())
	return m, b
}
func openProfileAction(t *testing.T, m *Model, label string, mouse bool) tea.Cmd {
	t.Helper()
	press(m, tea.KeyEnter, "")
	index := -1
	for i, entry := range m.menuEntries() {
		if strings.HasPrefix(entry, label) {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("profile action %q absent: %v", label, m.menuEntries())
	}
	if mouse {
		m.View()
		for _, hit := range m.home.Hits {
			if hit.Control == "menu" && hit.Index == index {
				_, cmd := m.Update(tea.MouseClickMsg{X: hit.X + 1, Y: hit.Y, Button: tea.MouseLeft})
				return cmd
			}
		}
		t.Fatal("menu hit region absent")
	}
	m.home.Modal.Selected = index
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}
func TestTypedProfilesIncludeSavedAndForeignRows(t *testing.T) {
	m, b := typedProfileFixture()
	if b.legacyCalled {
		t.Error("typed backend also queried legacy MCP inventory")
	}
	rows := m.profiles()
	if len(rows) != 2 {
		t.Fatalf("expected only related saved and foreign profiles, got %#v", rows)
	}
	view := m.View().Content
	for _, word := range []string{"never-started", "other-aact"} {
		if !strings.Contains(view, word) {
			t.Errorf("missing %s: %s", word, view)
		}
	}
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	id := m.home.Profiles.ID
	b.snapshot.Profiles[1].Name = "renamed runtime"
	b.snapshot.ObservationStale = true
	b.snapshot.DockerError = "daemon unavailable"
	b.snapshot.ObservedAt = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	m.Update(m.load()())
	if m.home.Profiles.ID != id {
		t.Error("display name changed stable profile identity")
	}
	view = m.View().Content
	if !strings.Contains(view, "daemon unavailable") || !strings.Contains(view, "2026-10-02") {
		t.Errorf("missing stale observation evidence: %s", view)
	}
}
func TestConfigureRegistrationsUsesTypedRequest(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyboard", true: "mouse"}[mouse], func(t *testing.T) {
			m, b := typedProfileFixture()
			m.focusPane(ProfilesPane)
			m.selectPane(ProfilesPane, 1)
			openProfileAction(t, m, "Configure agent registrations", mouse)
			if m.form == nil {
				t.Fatal("configure registrations did not open form")
			}
			view := m.View().Content
			if !strings.Contains(view, "[claude]") || strings.Contains(view, "All") {
				t.Fatalf("named agents not prefilled correctly: %s", view)
			}
			// codex is the first choice. Add it to the already registered claude destination.
			press(m, tea.KeySpace, " ")
			b.result = viewmodel.OperationResult{Message: "Registrations configured", Changes: []state.Installation{{AgentID: "codex", Component: "mcp"}}, Errors: []string{"claude: endpoint rejected"}}
			_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if cmd == nil || !m.busy {
				t.Fatal("save did not schedule registration operation")
			}
			if !strings.Contains(m.View().Content, "Running configure registrations") {
				t.Error("foreground operation progress missing")
			}
			m.Update(cmd())
			if b.request == nil {
				t.Fatal("typed registration backend not called")
			}
			want := b.snapshot.Profiles[1]
			if b.request.Key != want.Key || b.request.URL != want.URL || b.request.Transport != want.Transport || !reflect.DeepEqual(b.request.AgentIDs, []string{"claude", "codex"}) {
				t.Fatalf("wrong typed request: %#v", b.request)
			}
			if !strings.Contains(m.output, "codex") || !strings.Contains(m.output, "claude: endpoint rejected") {
				t.Fatalf("missing per-agent results: %s", m.output)
			}
			if m.home.Focus != ProfilesPane {
				t.Error("operation lost profile pane focus")
			}
		})
	}
}
func TestTypedProfileActionsRespectDisabledReasons(t *testing.T) {
	for _, action := range []string{"Restart", "Stop"} {
		m, _ := typedProfileFixture()
		m.focusPane(ProfilesPane)
		m.selectPane(ProfilesPane, 1)
		cmd := openProfileAction(t, m, action, false)
		if cmd != nil || m.busy || !strings.Contains(m.output, "Owned by another installation") {
			t.Fatalf("foreign action enabled or reason lost: %s", m.output)
		}
	}
	m, b := typedProfileFixture()
	b.snapshot.Profiles[1].Transport = ""
	m.Update(m.load()())
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openProfileAction(t, m, "Configure agent registrations", false)
	if m.form == nil || !strings.Contains(m.View().Content, "Transport") {
		t.Fatal("unknown foreign transport must be selected in the registration form")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("selected foreign transport did not submit")
	}
	m.Update(cmd())
	if b.request == nil || b.request.Transport != "streamable-http" {
		t.Fatalf("foreign transport choice was lost: %+v", b.request)
	}
}
func TestHomeLegacyUninstallShortcutIsDisabled(t *testing.T) {
	m := fixtureModel(t)
	focusFixtureProfile(m)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if cmd != nil || m.form != nil || m.pending.action == "uninstall" {
		t.Fatal("hidden profile shortcut opened full uninstall")
	}
}

func TestRegistrationCancelRestoresActions(t *testing.T) {
	m, b := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openProfileAction(t, m, "Configure agent registrations", false)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil || m.form != nil || m.home.Modal != nil || m.home.Focus != ProfilesPane || b.request != nil {
		t.Fatal("cancel did not restore originating profile selection")
	}
}
func TestRegistrationDeselectionAppliesEmptyDesiredSet(t *testing.T) {
	m, b := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	m.agents = append(m.agents, "All", "all")
	openProfileAction(t, m, "Configure agent registrations", false)
	for i := 0; i < 4; i++ {
		press(m, tea.KeyRight, "")
		if strings.Contains(m.View().Content, "[All]") || strings.Contains(m.View().Content, "[all]") {
			t.Fatal("All destination exposed for MCP registration")
		}
	}
	press(m, tea.KeyRight, "")
	press(m, tea.KeySpace, " ")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || m.form != nil || !m.busy {
		t.Fatal("empty desired selection must submit registration-only removal")
	}
	m.Update(cmd())
	if b.request == nil || len(b.request.AgentIDs) != 0 {
		t.Fatalf("wrong desired agents: %#v", b.request)
	}
}

func TestRemoveRegistrationsStartsUnselectedAndRemovesOnlySelectedAgents(t *testing.T) {
	m, b := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openProfileAction(t, m, "Remove agent registrations", false)
	if m.form == nil {
		t.Fatalf("Remove registrations did not open a form: %s", m.output)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil || b.request != nil || !strings.Contains(m.output, "No registrations selected") {
		t.Fatalf("unselected removal should be a no-op: %+v", b.request)
	}
	openProfileAction(t, m, "Remove agent registrations", false)
	press(m, tea.KeySpace, " ")
	_, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("selected removal was not submitted")
	}
	m.Update(cmd())
	if b.request == nil || len(b.request.AgentIDs) != 0 {
		t.Fatalf("remove selection did not produce empty desired registration state: %+v", b.request)
	}
}
func TestRegistrationPreservesUnavailableRegisteredAgent(t *testing.T) {
	m, b := typedProfileFixture()
	b.snapshot.Profiles[1].RegisteredAgents = append(b.snapshot.Profiles[1].RegisteredAgents, "generic:old")
	m.Update(m.load()())
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openProfileAction(t, m, "Configure agent registrations", false)
	if !strings.Contains(m.View().Content, "generic:old") {
		t.Fatal("unavailable existing registration was silently removed from selection")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("prefilled registration selection could not submit")
	}
	m.Update(cmd())
	if !reflect.DeepEqual(b.request.AgentIDs, []string{"claude", "generic:old"}) {
		t.Fatalf("existing registration lost: %#v", b.request)
	}
}
func TestRegistrationConnectionObservationIsVisible(t *testing.T) {
	for _, reachable := range []bool{true, false} {
		m, b := typedProfileFixture()
		m.focusPane(ProfilesPane)
		m.selectPane(ProfilesPane, 1)
		b.result.Connection = viewmodel.ConnectionObservation{URL: b.snapshot.Profiles[1].URL, Reachable: reachable, Error: "connection refused"}
		if reachable {
			b.result.Connection.Error = ""
		}
		openProfileAction(t, m, "Configure agent registrations", false)
		_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		if cmd == nil {
			t.Fatal("no save command")
		}
		m.Update(cmd())
		want := "connection refused"
		if reachable {
			want = "reachable"
		}
		if !strings.Contains(m.View().Content, want) {
			t.Fatalf("connection result missing: %s", m.View().Content)
		}
	}
}
func TestProfileDetailsSeparateLiveStatusFromLocalLastAction(t *testing.T) {
	m, b := typedProfileFixture()
	p := &b.snapshot.Profiles[1]
	p.RuntimeStatus = "conflict"
	p.LocalLastAction = "stop"
	p.LocalLastActionAt = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	p.RegistrationDisabledReason = "Multiple runtime endpoints"
	p.CanConfigureRegistrations = false
	m.Update(m.load()())
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	press(m, tea.KeyF3, "")
	for _, word := range []string{"conflict", "Local last action: stop", "2026-10-02", "Multiple runtime endpoints"} {
		if !strings.Contains(m.View().Content, word) {
			t.Errorf("missing detail %s: %s", word, m.View().Content)
		}
	}
}
func TestSnapshotFailurePreservesRowsAndDisablesRuntime(t *testing.T) {
	m, b := typedProfileFixture()
	b.snapshotErr = errors.New("profile store unavailable")
	m.Update(m.load()())
	if len(m.profiles()) != 2 {
		t.Fatal("failed refresh erased known profiles")
	}
	m.focusPane(ProfilesPane)
	cmd := openProfileAction(t, m, "Start", false)
	if cmd != nil || m.busy || !strings.Contains(m.output, "profile store unavailable") {
		t.Fatalf("failed refresh left runtime actions enabled: %s", m.output)
	}
}

func TestForeignSourceCapabilityRemainsSelectableAndRegistrable(t *testing.T) {
	m, b := typedProfileFixture()
	foreign := &b.snapshot.Profiles[2]
	foreign.Ownership = "other-aact"
	foreign.Name = "Remote inspector"
	foreign.URL = "http://127.0.0.1:8080/mcp"
	foreign.Transport = "streamable-http"
	foreign.CanConfigureRegistrations = true
	m.Update(m.load()())
	rows := m.capabilities()
	if len(rows) != 2 {
		t.Fatalf("foreign capability missing: %#v", rows)
	}
	m.selectPane(CapabilitiesPane, 1)
	if rows[1].Source != "another-source" || rows[1].CatalogIndex >= 0 {
		t.Fatalf("wrong synthetic identity: %#v", rows[1])
	}
	if !strings.Contains(m.View().Content, "another-source") {
		t.Fatal("foreign source label is missing")
	}
	id := m.home.Capabilities.ID
	for _, action := range []string{"parameters", "i", "a", "s"} {
		cmd := m.homeOperation(action)
		if cmd != nil || m.form != nil || m.busy || !strings.Contains(m.output, "local catalog") {
			t.Fatalf("foreign capability %s attempted local package operation", action)
		}
	}
	m.focusPane(ProfilesPane)
	openProfileAction(t, m, "Configure agent registrations", false)
	if m.form == nil {
		t.Fatal("foreign profile registration inaccessible")
	}
	press(m, tea.KeyEscape, "")
	b.snapshot.Profiles[2].Name = "Remote renamed"
	m.Update(m.load()())
	if m.home.Capabilities.ID != id {
		t.Fatal("synthetic capability identity changed on refresh")
	}
}

func TestSkillOnlyInstallDefaultsToGlobalAll(t *testing.T) {
	m := fixtureModel(t)
	m.agents = []string{"codex", "all", "claude"}
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnter, "")
	if m.form == nil || !strings.Contains(m.View().Content, "All — ~/.agents/skills") {
		t.Fatal("global skills destination lacks explicit label")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || m.pending.agent != "all" {
		t.Fatalf("skill-only install did not default to All: %#v", m.pending)
	}
}
func TestMCPInstallNeverOffersGlobalAll(t *testing.T) {
	m := fixtureModel(t)
	m.catalog[0].MCP = &catalog.MCP{}
	m.agents = []string{"all", "codex", "claude"}
	m.settings["default_agents"] = "all"
	press(m, tea.KeyEnter, "")
	press(m, tea.KeyEnter, "")
	for i := 0; i < 4; i++ {
		if strings.Contains(m.View().Content, "All —") || strings.Contains(m.View().Content, "[all]") {
			t.Fatal("MCP installation offers global All")
		}
		press(m, tea.KeyRight, "")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || m.pending.agent != "codex" {
		t.Fatalf("MCP install lacks named default: %#v", m.pending)
	}
}
func TestLegacyMCPViewDoesNotAdvertiseUninstall(t *testing.T) {
	m := fixtureModel(t)
	m.view = "MCPs"
	if strings.Contains(m.View().Content, "u uninstall") || strings.Contains(m.View().Content, "u unregister") {
		t.Fatal("legacy MCP view advertises destructive shortcut")
	}
}
