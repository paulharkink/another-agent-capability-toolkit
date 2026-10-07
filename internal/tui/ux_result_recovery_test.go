package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/picker"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type streamingSetupBackend struct {
	*setupBackendFixture
	started chan struct{}
	release chan struct{}
}

func (b *streamingSetupBackend) UIInstall(ctx context.Context, request viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	observer := viewmodel.OperationProgressObserver(ctx)
	if observer == nil {
		return viewmodel.OperationResult{}, errors.New("missing progress observer")
	}
	observer(viewmodel.OperationProgress{Step: "start", Output: "builder: applying configured settings\n"})
	close(b.started)
	<-b.release
	return viewmodel.OperationResult{Saved: true, Step: "start", Target: "plain / dev / default"}, errors.New("runtime start failed: fixture daemon refused the build")
}

func TestUXResultShowsExactLongChildErrorWrappedWithoutBlackRows(t *testing.T) {
	m, _ := homeFixture()
	m.width, m.height = 112, 28
	m.action = "install"
	diagnostic := "command repo-map failed: exit status 1: stderr: " + strings.Repeat("permission denied while opening the configured source directory; ", 4)
	m.Update(operationMsg{origin: "Catalog", output: diagnostic, err: errors.New(diagnostic)})
	view := m.View().Content
	plain := ansi.Strip(view)
	for _, part := range []string{"command repo-map failed", "configured source directory"} {
		if !strings.Contains(plain, part) {
			t.Fatalf("exact child output lost %q:\n%s", part, plain)
		}
	}
	for _, row := range strings.Split(view, "\n") {
		if ansi.StringWidth(row) > m.width {
			t.Fatalf("result row exceeds viewport: width=%d viewport=%d row=%q", ansi.StringWidth(row), m.width, row)
		}
	}
	if !strings.Contains(plain, "↓") || !strings.Contains(plain, "↑") {
		t.Fatalf("long result lacks visible scroll cues:\n%s", plain)
	}
}

func TestUXResultReportsSavedAndActualPerAgentAchievements(t *testing.T) {
	result := resultStateFromOperation("install", "", nil, viewmodel.OperationResult{
		Target: "dev", Step: "register", Saved: true, SavedApplicable: true,
		Changes: []state.Installation{{AgentID: "codex", Component: "mcp", Destination: "/tmp/codex.json"}},
		Errors:  []string{"claude: endpoint rejected registration"},
	})
	joined := strings.Join(result.Rows, "\n")
	for _, want := range []string{"Saved: yes", "codex", "/tmp/codex.json", "Failed", "claude: endpoint rejected registration"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("structured operation result missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "claude registered") {
		t.Fatalf("failed agent was reported as registered:\n%s", joined)
	}
}

func TestUXProgressFormatterDoesNotInventUnknownSteps(t *testing.T) {
	rows := operationProgressRows("Install", "dev", "", 24)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "Preparing") || strings.Contains(joined, "Starting") || strings.Contains(joined, "Registering") {
		t.Fatalf("progress invented an unknown step:\n%s", joined)
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > 24 {
			t.Fatalf("progress line exceeds resized width: %q", row)
		}
	}
	rows = operationProgressRows("Install", "dev", "authenticate", 54)
	if !strings.Contains(strings.Join(rows, "\n"), "authenticate") {
		t.Fatalf("real operation step is not visible:\n%v", rows)
	}
}

func TestUXProgressVisibleResizesAndDoesNotClaimUnknownSteps(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.action = "install"
	m.busy = true
	m.pendingSetup = &viewmodel.SetupPreview{Key: state.Key{Package: "plain", Target: "dev"}}
	m.Update(tea.WindowSizeMsg{Width: 46, Height: 12})
	plain := ansi.Strip(m.View().Content)
	if !strings.Contains(plain, "install") || !strings.Contains(plain, "plain") {
		t.Fatalf("foreground progress omitted operation identity or target:\n%s", plain)
	}
	if strings.Contains(plain, "Preparing") || strings.Contains(plain, "Starting") || strings.Contains(plain, "Registering") {
		t.Fatalf("foreground progress invented a stage:\n%s", plain)
	}
	for _, row := range strings.Split(m.View().Content, "\n") {
		if ansi.StringWidth(row) > m.width {
			t.Fatalf("progress row wider than resized viewport %d: %q", m.width, row)
		}
	}
}

func TestUXReturnToConfigurationPreservesSubmittedDraftAndOrigin(t *testing.T) {
	backend := &setupBackendFixture{installResult: &viewmodel.OperationResult{Saved: true, Step: "install", Target: "plain/dev"}, installErr: errors.New("repository missing")}
	m := NewContext(context.Background(), backend)
	m.view = "Environments"
	m.width, m.height = 100, 28
	m.pendingSetup = &viewmodel.SetupPreview{PackageName: "Plain", Key: state.Key{Source: "team", Package: "plain", Environment: "dev", Target: "default"}, Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string"}, Editable: true}}}
	m.pendingSetupField = "__aact_destinations"
	m.workspace = &workspaceState{Key: m.pendingSetup.Key, Active: true, Section: "Connection", InvokingView: "Environments"}
	cmd := m.applySetup(map[string]any{"repo": "/repos/submitted", "__aact_destinations": []string{"codex"}})
	m.Update(runTeaCmd(t, m, cmd))
	if m.result == nil {
		t.Fatal("failed operation did not open foreground result")
	}
	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{"Operation: install", "Target: plain/dev", "Failed step: install", "Saved: yes", "repository missing", "Return to configuration", "Retry", "Close"} {
		if !strings.Contains(plain, want) {
			t.Errorf("foreground result omitted %q:\n%s", want, plain)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.result != nil || m.form == nil || m.view != "Environments" {
		t.Fatalf("Return to configuration did not restore form and origin: result=%+v form=%v view=%s", m.result, m.form != nil, m.view)
	}
	if got := m.form.Values()["repo"]; got != "/repos/submitted" {
		t.Fatalf("submitted draft lost on return: repo=%v", got)
	}
}

func TestUXSetupWorkerReturnsFailureMetadataForEventLoop(t *testing.T) {
	backend := &setupBackendFixture{
		installResult: &viewmodel.OperationResult{Saved: true, Step: "registration", Target: "plain/dev"},
		installErr:    errors.New("endpoint rejected registration"),
	}
	m := NewContext(context.Background(), backend)
	m.view = "Catalog"
	m.pendingSetup = &viewmodel.SetupPreview{Key: state.Key{Package: "plain", Environment: "dev"}}
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex"}})
	if cmd == nil {
		t.Fatal("setup operation was not submitted")
	}
	draft := m.setupRetry
	message := cmd()
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("setup command returned %T, want operation batch", message)
	}
	msg, ok := batch[0]().(operationMsg)
	if !ok {
		t.Fatalf("setup operation returned %T, want operationMsg", batch[0]())
	}
	if draft.step != "" || draft.failure != "" {
		t.Fatalf("worker mutated model-owned retry draft before Update: step=%q failure=%q", draft.step, draft.failure)
	}
	if msg.step != "registration" || msg.err == nil || msg.err.Error() != "endpoint rejected registration" {
		t.Fatalf("failure metadata missing from completion message: %+v", msg)
	}
	m.Update(msg)
	if draft.step != "registration" || draft.failure != "endpoint rejected registration" {
		t.Fatalf("Update did not retain failure metadata: step=%q failure=%q", draft.step, draft.failure)
	}
}

func TestUXStaleSetupCompletionDoesNotRewriteNewRetryDraft(t *testing.T) {
	backend := &setupBackendFixture{
		installResult: &viewmodel.OperationResult{Step: "registration"},
		installErr:    errors.New("old request failed"),
	}
	m := NewContext(context.Background(), backend)
	m.pendingSetup = &viewmodel.SetupPreview{Key: state.Key{Package: "plain", Environment: "dev"}}
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex"}})
	staleID := m.setupOperationID
	newDraft := &setupRetryDraft{preview: viewmodel.SetupPreview{Key: state.Key{Package: "plain", Environment: "dev"}}, values: map[string]any{"repo": "/new"}}
	m.setupOperationID++
	m.setupRetry = newDraft
	m.Update(runTeaCmd(t, m, cmd))
	if m.setupOperationID == staleID {
		t.Fatal("test did not advance the active operation identity")
	}
	if newDraft.step != "" || newDraft.failure != "" {
		t.Fatalf("stale completion rewrote the newer retry draft: step=%q failure=%q", newDraft.step, newDraft.failure)
	}
}

type gatedSetupBackend struct {
	*setupBackendFixture
	started chan struct{}
	release chan struct{}
}

func (b *gatedSetupBackend) UIInstall(_ context.Context, _ viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	close(b.started)
	<-b.release
	return viewmodel.OperationResult{Step: "registration"}, errors.New("endpoint rejected registration")
}

func TestUXSetupWorkerCanRunWhileModelIsViewed(t *testing.T) {
	backend := &gatedSetupBackend{setupBackendFixture: &setupBackendFixture{}, started: make(chan struct{}), release: make(chan struct{})}
	m := NewContext(context.Background(), backend)
	m.width, m.height = 100, 24
	m.pendingSetup = &viewmodel.SetupPreview{Key: state.Key{Package: "plain", Environment: "dev"}}
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex"}})
	draft := m.setupRetry
	completed := make(chan tea.Msg, 1)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("setup worker returned no operation batch")
	}
	go func() { completed <- batch[0]() }()
	select {
	case <-backend.started:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked install did not start")
	}
	close(backend.release)
	finished := make(chan struct{})
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		for {
			_ = draft.step
			_ = draft.failure
			_ = m.View().Content
			select {
			case <-finished:
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	msg := <-completed
	close(finished)
	<-observerDone
	if draft.step != "" || draft.failure != "" {
		t.Fatalf("worker changed retry metadata before the event loop handled completion: step=%q failure=%q", draft.step, draft.failure)
	}
	m.Update(msg)
	if draft.step != "registration" || draft.failure != "endpoint rejected registration" {
		t.Fatalf("event loop did not apply returned metadata: step=%q failure=%q", draft.step, draft.failure)
	}
}

type retrySetupBackend struct {
	*setupBackendFixture
	calls    int
	requests []viewmodel.SetupInstallRequest
}

func (b *retrySetupBackend) UIInstall(_ context.Context, q viewmodel.SetupInstallRequest) (viewmodel.OperationResult, error) {
	b.calls++
	copyRequest := q
	copyRequest.Inputs = cloneSetupValues(q.Inputs)
	b.requests = append(b.requests, copyRequest)
	if b.calls == 1 {
		return viewmodel.OperationResult{Saved: true, Step: "install", Target: "plain/default"}, errors.New("first attempt failed")
	}
	return viewmodel.OperationResult{Saved: true, Changes: []state.Installation{{AgentID: "codex", Component: "skill", Destination: "/tmp/codex"}}}, nil
}

func TestUXRetryUsesSameInputsAndRunsOnce(t *testing.T) {
	backend := &retrySetupBackend{setupBackendFixture: &setupBackendFixture{}}
	m := NewContext(context.Background(), backend)
	m.view = "Catalog"
	m.width, m.height = 100, 28
	m.pendingSetup = &viewmodel.SetupPreview{Key: state.Key{Source: "team", Package: "plain", Target: "default"}, Inputs: []viewmodel.SetupInput{{Definition: catalog.Input{Name: "repo", Label: "Repository", Type: "string"}, Editable: true}}}
	m.pendingSetupField = "__aact_destinations"
	initial := map[string]any{"repo": "/repos/unchanged", "__aact_destinations": []string{"codex"}}
	cmd := m.applySetup(initial)
	m.Update(runTeaCmd(t, m, cmd))
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	_, retry := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if retry == nil {
		t.Fatal("Retry did not start an operation")
	}
	m.Update(runTeaCmd(t, m, retry))
	if backend.calls != 2 || len(backend.requests) != 2 {
		t.Fatalf("retry count=%d requests=%d; want exactly one retry", backend.calls, len(backend.requests))
	}
	if !reflect.DeepEqual(backend.requests[0].Inputs, backend.requests[1].Inputs) || !reflect.DeepEqual(backend.requests[0].DestinationIDs, backend.requests[1].DestinationIDs) {
		t.Fatalf("retry changed submitted values: first=%+v second=%+v", backend.requests[0], backend.requests[1])
	}
	if m.result != nil && m.result.Failed {
		t.Fatalf("successful retry left a failure result open: %+v", m.result)
	}
}

func TestUXCancelReturnsWithoutFailureDialog(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.action = "install"
	m.Update(operationMsg{origin: "Catalog", err: picker.ErrCancelled})
	if m.result != nil || m.busy {
		t.Fatalf("ordinary cancellation opened failure result: result=%+v busy=%t", m.result, m.busy)
	}
}

func TestUXPreviewOrConnectionErrorCannotHideInFooter(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.view = "Environments"
	m.width, m.height = 100, 28
	m.Update(setupPreviewMsg{err: errors.New("/tmp/env/target.toml: invalid format")})
	if m.result == nil || !strings.Contains(ansi.Strip(m.View().Content), "/tmp/env/target.toml: invalid format") {
		t.Fatalf("preview error was not foregrounded: output=%q result=%+v", m.output, m.result)
	}
	m.result = nil
	m.Update(operationMsg{origin: "Catalog", output: "https://mcp.example.test: unreachable · checked now\nconnection refused"})
	plain := ansi.Strip(m.View().Content)
	if m.result == nil || !strings.Contains(plain, "https://mcp.example.test") || !strings.Contains(plain, "connection refused") {
		t.Fatalf("connection detail was hidden in footer: output=%q result=%+v", m.output, m.result)
	}
}

func TestUXSmallResultKeepsExactFailureAndActionKeysVisible(t *testing.T) {
	m, _ := homeFixture()
	m.action = "load"
	m.Update(operationMsg{origin: "Catalog", err: errors.New("/tmp/config/target.toml: malformed TOML")})
	m.Update(tea.WindowSizeMsg{Width: 34, Height: 7})
	plain := ansi.Strip(m.View().Content)
	if !strings.Contains(plain, "malformed TOML") || !strings.Contains(plain, "Esc") {
		t.Fatalf("small terminal hid failure or dismissal control:\n%s", plain)
	}
	if len(strings.Split(plain, "\n")) > 7 {
		t.Fatalf("small result exceeded terminal height:\n%s", plain)
	}
}

func TestUXRegistrationResultListsOnlyStructuredAchievedEffectsAndKeepsMixedFailureVisible(t *testing.T) {
	m, backend := typedProfileFixture()
	m.focusPane(ProfilesPane)
	m.selectPane(ProfilesPane, 1)
	openRegistrationWorkspaceAction(t, m, false)
	backend.result = viewmodel.OperationResult{
		Saved: true, Step: "registration", Target: "plain / dev / foreign",
		Changes: []state.Installation{{AgentID: "codex", Component: "mcp", RegistrationName: "plain-dev", URL: "http://127.0.0.1:8765/mcp", Transport: "streamable-http"}},
		Errors:  []string{"claude: endpoint rejected registration"},
	}
	backend.err = errors.Join(errors.New("registration command returned a partial failure"), picker.ErrCancelled)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("registration update was not scheduled")
	}
	m.Update(cmd())
	if m.result == nil || !m.result.Failed {
		t.Fatalf("mixed failure/cancellation was hidden: result=%+v output=%q", m.result, m.output)
	}
	joined := strings.Join(m.result.Rows, "\n")
	for _, want := range []string{"Applied effects: 1 reported", "codex", "plain-dev", "http://127.0.0.1:8765/mcp", "Failed · claude: endpoint rejected registration", "partial failure"} {
		if !strings.Contains(joined, want) {
			t.Errorf("registration result omitted %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "Saved:") {
		t.Fatalf("registration-only operation claimed package inputs were saved:\n%s", joined)
	}
	if strings.Contains(joined, "claude registered") || strings.Contains(joined, "claude · mcp ·") {
		t.Fatalf("failed agent was claimed as applied:\n%s", joined)
	}
}

type retryRegistrationBackend struct {
	*registrationWorkspaceBackend
	calls    int
	requests []viewmodel.RegistrationRequest
	result   viewmodel.OperationResult
	err      error
}

func (b *retryRegistrationBackend) UIConfigureRegistrations(_ context.Context, request viewmodel.RegistrationRequest) (viewmodel.OperationResult, error) {
	b.calls++
	copyRequest := request
	copyRequest.AgentIDs = append([]string(nil), request.AgentIDs...)
	b.requests = append(b.requests, copyRequest)
	return b.result, b.err
}

func TestUXRegistrationRetrySurvivesCompletionRefreshAndReusesRequest(t *testing.T) {
	m, profile := typedProfileFixture()
	base := &registrationWorkspaceBackend{Backend: profile, profileBackend: profile, setupBackendFixture: &setupBackendFixture{}}
	backend := &retryRegistrationBackend{registrationWorkspaceBackend: base, err: errors.New("registration failed")}
	m.backend = backend
	request := viewmodel.RegistrationRequest{Key: state.Key{Source: "team-source", Package: "plain", Environment: "dev", Target: "foreign"}, URL: "http://127.0.0.1:8765/mcp", AgentIDs: []string{"claude"}, Transport: "streamable-http"}
	completed := m.configureRegistrations(request, false)()
	_, refresh := m.Update(completed)
	if refresh == nil {
		t.Fatal("failed registration did not schedule data refresh")
	}
	m.Update(refresh())
	if m.result == nil || !m.result.CanRetry {
		t.Fatalf("operation-specific retry was lost during refresh: result=%+v", m.result)
	}
	backend.err = nil
	backend.result = viewmodel.OperationResult{Saved: true, Target: "plain / dev / foreign", Changes: []state.Installation{{AgentID: "claude", Component: "mcp", RegistrationName: "plain-dev"}}}
	_, retry := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if retry == nil {
		t.Fatal("Retry did not start the original registration operation")
	}
	completedRetry := retry()
	_, refresh = m.Update(completedRetry)
	if refresh != nil {
		m.Update(refresh())
	}
	if backend.calls != 2 || len(backend.requests) != 2 {
		t.Fatalf("registration request count=%d, want initial attempt plus exactly one retry", backend.calls)
	}
	if !reflect.DeepEqual(backend.requests[0], request) || !reflect.DeepEqual(backend.requests[1], request) {
		t.Fatalf("retry changed original registration request: first=%+v retry=%+v want=%+v", backend.requests[0], backend.requests[1], request)
	}
	if m.result != nil && m.result.Failed {
		t.Fatalf("successful retry remained failed: %+v", m.result)
	}
}

func TestUXProfileActionsUseObservedOwnerAndRuntimeStatus(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	cases := []struct {
		name, owner, status, action, want string
	}{
		{"foreign running start", "other-aact", "running", "s", "another installation"},
		{"unknown running logs", "unknown", "running", "l", "locally owned"},
		{"foreign running logs", "other-aact", "running", "l", "locally owned"},
		{"local running logs", "local", "running", "l", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := ProfileRow{Profile: &viewmodel.Profile{Ownership: tc.owner, RuntimeStatus: tc.status}}
			if got := m.profileActionReason(row, tc.action); !strings.Contains(got, tc.want) {
				t.Fatalf("reason=%q, want it to contain %q", got, tc.want)
			}
		})
	}
}

type recordingUIRunBackend struct {
	fixtureBackend
	action string
}

func (b *recordingUIRunBackend) UIRun(_ context.Context, action, sourceID, packageID, agentID, environment, target string) (string, error) {
	b.action = action
	return "ok", nil
}

func TestUXWorkspaceShortStartStopActionsUseNamedServiceActions(t *testing.T) {
	for short, want := range map[string]string{"s": "start", "x": "stop"} {
		t.Run(want, func(t *testing.T) {
			backend := &recordingUIRunBackend{}
			m := NewContext(context.Background(), backend)
			cmd := m.run(operation{action: short, packageID: "plain", target: "dev"})
			msg := cmd().(operationMsg)
			m.Update(msg)
			if backend.action != want {
				t.Fatalf("service action=%q, want %q", backend.action, want)
			}
		})
	}
}

type unreachableConnectionBackend struct {
	*registrationWorkspaceBackend
	checks int
}

func (b *unreachableConnectionBackend) CheckConnection(_ context.Context, url, _ string) viewmodel.ConnectionObservation {
	b.checks++
	return viewmodel.ConnectionObservation{URL: url, Error: "connection refused"}
}

func TestUXWorkspaceConnectionFailureActionsStayInForegroundOverActiveForm(t *testing.T) {
	m, profile := typedProfileFixture()
	base := &registrationWorkspaceBackend{Backend: profile, profileBackend: profile, setupBackendFixture: &setupBackendFixture{}}
	backend := &unreachableConnectionBackend{registrationWorkspaceBackend: base}
	m.backend = backend
	key := profile.snapshot.Profiles[1].Key
	cmd := m.openTargetWorkspace(viewmodel.SetupRequest{SourceID: key.Source, PackageID: key.Package, Environment: key.Environment, Target: key.Target}, "Connection")
	m.Update(cmd())
	if m.form == nil {
		t.Fatal("workspace form did not open")
	}
	form := m.form
	row := ProfileRow{Profile: &profile.snapshot.Profiles[1], Key: key, URL: profile.snapshot.Profiles[1].URL}
	m.Update(m.checkProfileConnection(row)())
	if m.result == nil || !m.result.Failed || !strings.Contains(ansi.Strip(m.View().Content), "connection refused") {
		t.Fatalf("connection error did not become foreground result: result=%+v output=%q", m.result, m.output)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.result.ActionIndex != 1 || m.form != form {
		t.Fatalf("Tab reached hidden form instead of foreground action: selection=%d formSame=%t", m.result.ActionIndex, m.form == form)
	}
	m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if m.result != nil || m.form != form {
		t.Fatalf("Return to configuration did not restore the active workspace: result=%+v formSame=%t", m.result, m.form == form)
	}
}

func TestUXRunningInstallShowsLiveOutputOverWorkspaceContext(t *testing.T) {
	m, _ := homeFixture()
	m.width, m.height = 100, 30
	m.busy = true
	m.action = "install"
	m.progressStep = "generate"
	m.progressOutput = "generator: writing configured skill files"
	m.pendingSetup = &viewmodel.SetupPreview{PackageName: "Plain", Key: state.Key{Source: "team", Package: "plain", Environment: "dev", Target: "default"}}
	m.workspace = &workspaceState{Key: m.pendingSetup.Key, Active: true}

	plain := ansi.Strip(m.View().Content)
	if !strings.Contains(plain, "generator: writing configured skill files") {
		t.Fatalf("live child output is hidden while the install is running:\n%s", plain)
	}
	if !strings.Contains(plain, "Inspector") || !strings.Contains(plain, "AACT") {
		t.Fatalf("progress replaced the current workspace context:\n%s", plain)
	}
}

func TestUXRunningInstallOutputHasTruthfulScrollCues(t *testing.T) {
	m := NewContext(context.Background(), &setupBackendFixture{})
	m.width, m.height = 80, 16
	m.busy = true
	m.setupOperationPending = true
	m.setupOperationID = 3
	m.action = "install"
	m.progressStarted = time.Now()
	lines := make([]string, 24)
	for i := range lines {
		lines[i] = fmt.Sprintf("child output line %02d", i)
	}
	m.progressOutput = strings.Join(lines, "\n")
	newest := ansi.Strip(m.View().Content)
	if !strings.Contains(newest, "↑ older output") || strings.Contains(newest, "↓ newer output") {
		t.Fatalf("bottom-follow view shows incorrect scroll cues:\n%s", newest)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	older := ansi.Strip(m.View().Content)
	if !strings.Contains(older, "↓ newer output") {
		t.Fatalf("scrolling older did not show a truthful newer-output cue:\n%s", older)
	}
	m.Update(setupProgressMsg{setupID: 3, progress: viewmodel.OperationProgress{Output: "\nnew trailing child line"}})
	if m.progressOffset == 0 {
		t.Fatal("a new chunk forced the user's older scroll position to latest output")
	}
}

func TestUXSaveApplyStreamsBeforeCompletionAndKeepsFailureRecovery(t *testing.T) {
	backend := &streamingSetupBackend{setupBackendFixture: &setupBackendFixture{}, started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(backend.release) })
	m := NewContext(context.Background(), backend)
	m.width, m.height = 120, 36
	m.pendingSetup = &viewmodel.SetupPreview{PackageName: "Plain", Key: state.Key{Source: "team", Package: "plain", Environment: "dev", Target: "default"}}
	m.pendingSetupField = "__aact_destinations"
	m.workspace = &workspaceState{Key: m.pendingSetup.Key, Active: true, Section: "Connection"}
	m.openSetupFormWithValues(*m.pendingSetup, map[string]any{})
	cmd := m.applySetup(map[string]any{"__aact_destinations": []string{"codex"}})
	message := cmd()
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("SaveApply did not start operation, stream and heartbeat commands: %T %#v", message, batch)
	}
	operationDone := make(chan tea.Msg, 1)
	go func() { operationDone <- batch[0]() }()
	select {
	case <-backend.started:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked SaveApply did not start")
	}
	progress := batch[1]()
	m.Update(progress)
	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{"Operation in progress", "Current step: start", "builder: applying configured settings", "Target: team / plain", "Install · Plain", "Authentication", "Databases", "Elapsed", "Quiet"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("blocked operation overlay omitted %q:\n%s", want, plain)
		}
	}
	select {
	case <-operationDone:
		t.Fatal("operation completed before the fixture was released")
	default:
	}
	writeUXCapture(t, "SaveApply install in progress", m.View().Content)
	releaseOnce.Do(func() { close(backend.release) })
	var completed tea.Msg
	select {
	case completed = <-operationDone:
	case <-time.After(3 * time.Second):
		t.Fatal("SaveApply did not finish after release")
	}
	m.Update(completed)
	if m.result == nil || !m.result.Failed {
		t.Fatalf("child failure did not produce a recoverable result: %+v", m.result)
	}
	result := ansi.Strip(m.View().Content)
	for _, want := range []string{"builder: applying configured settings", "fixture daemon refused the build", "Return to configuration", "Retry"} {
		if !strings.Contains(result, want) {
			t.Errorf("foreground failure omitted %q:\n%s", want, result)
		}
	}

	oldID := m.setupOperationID
	m.setupOperationID++
	m.setupOperationPending = true
	m.busy = true
	m.result = nil
	m.progressStep = "next action"
	m.progressOutput = "new operation output"
	m.Update(setupProgressMsg{setupID: oldID, progress: viewmodel.OperationProgress{Step: "stale", Output: "stale output"}})
	m.Update(operationMsg{setupID: oldID, origin: "Catalog", output: "stale completion", err: errors.New("stale failure")})
	if !m.busy || m.progressStep != "next action" || m.progressOutput != "new operation output" || m.result != nil {
		t.Fatalf("stale operation output changed the next operation: busy=%t step=%q output=%q result=%+v", m.busy, m.progressStep, m.progressOutput, m.result)
	}
	// A subsequent unrelated operation must not inherit setup progress or timing.
	m.busy = false
	m.progressStarted = time.Now().Add(-time.Minute)
	m.progressLastOutput = m.progressStarted
	m.progressStep = "authenticate"
	m.progressOutput = "old setup child output"
	m.run(operation{action: "stop"})
	plain = ansi.Strip(m.View().Content)
	if strings.Contains(plain, "authenticate") || strings.Contains(plain, "old setup child output") || strings.Contains(plain, "Elapsed:") || strings.Contains(plain, "Quiet:") {
		t.Fatalf("unrelated operation inherited stale setup progress:\n%s", plain)
	}
}
