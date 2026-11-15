package browsercontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type commandCall struct{ args []string }
type fakeCommands struct {
	calls           []commandCall
	steps           []commandStep
	stdinCalls      []stdinCommandCall
	volumeUses      string
	volumeUseStates map[string]string
	volumeMounts    map[string]string
}
type stdinCommandCall struct {
	args     []string
	stdin    []byte
	deadline time.Time
}
type commandStep struct {
	contains string
	out      string
	err      error
}

type noInputFakeCommands struct{ inner *fakeCommands }

func (f noInputFakeCommands) Run(ctx context.Context, args []string) ([]byte, error) {
	return f.inner.Run(ctx, args)
}

func (f *fakeCommands) Run(_ context.Context, args []string) ([]byte, error) {
	f.calls = append(f.calls, commandCall{args: append([]string(nil), args...)})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--format {{json .Mounts}}") {
		name := args[len(args)-1]
		if value, ok := f.volumeMounts[name]; ok {
			return []byte(value), nil
		}
		return []byte(`[]`), nil
	}
	if strings.Contains(joined, "--filter volume=") {
		var names []string
		for _, name := range strings.Fields(f.volumeUses) {
			state := f.volumeUseStates[name]
			if strings.Contains(joined, "--all") || state == "" || state == "running" || state == "paused" {
				names = append(names, name)
			}
		}
		return []byte(strings.Join(names, "\n")), nil
	}
	if len(f.steps) == 0 {
		return nil, nil
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, step.contains) {
		return nil, errUnexpectedCommand{got: joined, want: step.contains}
	}
	return []byte(step.out), step.err
}

func TestDockerBrowserRefusesProfileVolumeMountedByAnotherRunningContainer(t *testing.T) {
	for _, state := range []string{"running", "paused"} {
		t.Run(state, func(t *testing.T) {
			competitor := "browser-authentication-helper-profile-owner"
			commands := &fakeCommands{
				steps:           []commandStep{{contains: "container ls", out: ""}},
				volumeUses:      competitor + "\n",
				volumeUseStates: map[string]string{competitor: state},
				volumeMounts:    map[string]string{competitor: `[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}]`},
			}
			browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", StartupURL: "https://app.example"}
			if _, err := browser.EnsureStarted(context.Background()); err == nil ||
				!strings.Contains(err.Error(), "mounted by another container") ||
				!strings.Contains(err.Error(), "if it is the previous AACT browser") ||
				!strings.Contains(err.Error(), "without removing the profile volume") {
				t.Fatalf("EnsureStarted() error = %v; want profile-volume conflict", err)
			}
			if len(commands.calls) != 3 {
				t.Fatalf("Docker command count = %d; want name lookup, volume scan, and mount inspection: %#v", len(commands.calls), commands.calls)
			}
			for _, call := range commands.calls {
				if strings.Contains(strings.Join(call.args, " "), "container run") {
					t.Fatalf("Docker created a second container for a busy profile volume: %#v", call.args)
				}
			}
		})
	}
}

func TestDockerBrowserRefusesStoppedContainerThatStillReferencesProfileVolume(t *testing.T) {
	commands := &fakeCommands{
		steps: []commandStep{
			{contains: "container ls", out: ""},
			{contains: "container run", out: "must-not-start"},
		},
		volumeUses:      "browser-profile-recovery",
		volumeUseStates: map[string]string{"browser-profile-recovery": "exited"},
		volumeMounts:    map[string]string{"browser-profile-recovery": `[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}]`},
	}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", StartupURL: "https://app.example"}
	if _, err := browser.EnsureStarted(context.Background()); err == nil || !strings.Contains(err.Error(), "another container") {
		t.Fatalf("EnsureStarted() error = %v; want stopped recovery-container conflict", err)
	}
	for _, call := range commands.calls {
		if strings.Contains(strings.Join(call.args, " "), "container run") {
			t.Fatal("Docker created a browser while a stopped recovery container still references the profile volume")
		}
	}
}

func TestDockerBrowserAllowsVolumeFilterFalsePositiveWithoutExactVolumeMount(t *testing.T) {
	inspect := `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","aact.browser-controller.startup-url-sha256":"` + startupURLDigest("https://app.example") + `","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`
	commands := &fakeCommands{
		steps: []commandStep{
			{contains: "container ls", out: ""},
			{contains: "container run", out: "container-id"},
			{contains: "container inspect", out: inspect},
		},
		volumeUses:   "unrelated-container",
		volumeMounts: map[string]string{"unrelated-container": `[{"Type":"bind","Name":"","Destination":"/home/kasm-user"}]`},
	}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", StartupURL: "https://app.example"}
	if _, err := browser.EnsureStarted(context.Background()); err != nil {
		t.Fatalf("EnsureStarted() rejected a volume-filter false positive: %v", err)
	}
}

type concurrentVolumeRunner struct {
	started  chan struct{}
	release  chan struct{}
	startOne sync.Once
}

func (r *concurrentVolumeRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "container ls --all --filter name="):
		entered := false
		r.startOne.Do(func() { close(r.started); entered = true })
		if entered {
			select {
			case <-r.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, nil
	case strings.Contains(joined, "--filter volume="):
		return nil, nil
	case strings.Contains(joined, "container run"):
		return []byte("synthetic-container-id"), nil
	case strings.Contains(joined, "container inspect"):
		return []byte(`{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-concurrent","aact.browser-controller.access-config-sha256":"none","aact.browser-controller.startup-url-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-concurrent","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`), nil
	default:
		return nil, errors.New("unexpected concurrent synthetic Docker command")
	}
}

func TestDockerBrowserConcurrentStartsForOneProfileHaveSingleWinner(t *testing.T) {
	runner := &concurrentVolumeRunner{started: make(chan struct{}), release: make(chan struct{})}
	first := DockerBrowser{Commands: runner, ProfileVolume: "profile-concurrent", ContainerName: "browser-one"}
	second := DockerBrowser{Commands: runner, ProfileVolume: "profile-concurrent", ContainerName: "browser-two"}
	firstResult := make(chan error, 1)
	go func() { _, err := first.EnsureStarted(context.Background()); firstResult <- err }()
	<-runner.started
	_, secondErr := second.EnsureStarted(context.Background())
	close(runner.release)
	firstErr := <-firstResult
	if firstErr != nil || secondErr == nil {
		t.Fatalf("concurrent EnsureStarted results: first=%v second=%v; want one winner and one lock rejection", firstErr, secondErr)
	}
}

func (f *fakeCommands) RunInput(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	deadline, _ := ctx.Deadline()
	f.stdinCalls = append(f.stdinCalls, stdinCommandCall{args: append([]string(nil), args...), stdin: append([]byte(nil), stdin...), deadline: deadline})
	if len(f.steps) == 0 {
		return nil, nil
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, step.contains) {
		return nil, errUnexpectedCommand{got: joined, want: step.contains}
	}
	return []byte(step.out), step.err
}

type errUnexpectedCommand struct{ got, want string }

func (e errUnexpectedCommand) Error() string {
	return "unexpected command: " + e.got + " (wanted " + e.want + ")"
}

func TestDockerBrowserCreatesOnlyFixedLoopbackContainer(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: ""},
		{contains: "container run", out: "container-id"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","aact.browser-controller.startup-url-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
	}}
	accessConfig := `{"allowed_urls":["https://example.test:8443/private"]}`
	startupURL := "https://example.test:8443/private/start"
	commands.steps[2].out = strings.Replace(commands.steps[2].out,
		`"aact.browser-controller.access-config-sha256":"none"`,
		`"aact.browser-controller.access-config-sha256":"`+accessConfigDigest(accessConfig)+`"`, 1)
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", AccessConfigJSON: accessConfig, StartupURL: startupURL}
	commands.steps[2].out = strings.Replace(commands.steps[2].out,
		`"aact.browser-controller.startup-url-sha256":"none"`,
		`"aact.browser-controller.startup-url-sha256":"`+startupURLDigest(startupURL)+`"`, 1)
	url, err := browser.EnsureStarted(context.Background())
	if err != nil {
		t.Fatalf("EnsureStarted() error = %v", err)
	}
	if url != "http://localhost:49153" {
		t.Fatalf("viewer URL = %q", url)
	}
	if got := strings.Join(commands.calls[2].args, " "); !strings.Contains(got, "--publish 127.0.0.1::6901") || !strings.Contains(got, "--shm-size=512m") || !strings.Contains(got, "--mount source=profile-test,target=/home/kasm-user") || !strings.Contains(got, "aact/browser-authentication-helper:1.18.0") {
		t.Fatalf("unsafe or incomplete fixed docker run args: %q", got)
	}
	if got := strings.Join(commands.calls[2].args, " "); !strings.Contains(got, "--env AACT_BROWSER_ACCESS_CONFIG_JSON="+accessConfig) {
		t.Fatalf("browser access configuration was not passed to the image: %q", got)
	}
	if got := strings.Join(commands.calls[2].args, " "); !strings.Contains(got, "--env KASM_URL="+startupURL) || !strings.Contains(got, "aact.browser-controller.startup-url-sha256="+startupURLDigest(startupURL)) {
		t.Fatalf("validated startup URL or its digest was not passed to Docker: %q", got)
	}
}

func TestDockerBrowserStopsOnlyItsOwnedContainer(t *testing.T) {
	name := "aact-browser-test"
	inspect := `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","aact.browser-controller.startup-url-sha256":"` + startupURLDigest("https://app.example") + `","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls --all --filter name=^/" + name, out: name + "\n"},
		{contains: "container inspect", out: inspect},
		{contains: "container stop --time 5 " + name, out: name},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", StartupURL: "https://app.example", ContainerName: name}
	stopper, ok := any(browser).(interface{ StopBrowser(context.Context) error })
	if !ok {
		t.Fatal("DockerBrowser does not implement safe managed browser stop")
	}
	if err := stopper.StopBrowser(context.Background()); err != nil {
		t.Fatalf("StopBrowser() error = %v", err)
	}
	if len(commands.calls) != 3 || !strings.Contains(strings.Join(commands.calls[2].args, " "), "container stop --time 5 "+name) {
		t.Fatalf("stop commands = %#v; want exact stop after ownership check", commands.calls)
	}
}

func TestDockerBrowserRefusesToStopContainerWithDifferentOwnership(t *testing.T) {
	name := "aact-browser-test"
	inspect := `{"Config":{"Image":"unrelated:image","Labels":{}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{}}}`
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls --all --filter name=^/" + name, out: name + "\n"},
		{contains: "container inspect", out: inspect},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", ContainerName: name}
	stopper, ok := any(browser).(interface{ StopBrowser(context.Context) error })
	if !ok {
		t.Fatal("DockerBrowser does not implement safe managed browser stop")
	}
	if err := stopper.StopBrowser(context.Background()); err == nil {
		t.Fatal("StopBrowser() accepted a container with unrelated ownership")
	}
	for _, call := range commands.calls {
		if strings.Contains(strings.Join(call.args, " "), "container stop") {
			t.Fatal("StopBrowser() stopped a container with unrelated ownership")
		}
	}
}

func TestDockerBrowserRequiresExplicitRecreateForStaleStartupURL(t *testing.T) {
	for _, test := range []struct {
		name       string
		configured string
		stored     string
		hasLabel   bool
	}{
		{name: "changed startup URL", configured: "https://app.example", stored: "old-startup-url", hasLabel: true},
		{name: "removed startup URL", configured: "", stored: startupURLDigest("https://app.example"), hasLabel: true},
		{name: "configured URL missing legacy digest", configured: "https://app.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := "browser-authentication-helper-controller"
			startupLabel := ""
			if test.hasLabel {
				startupLabel = `,"aact.browser-controller.startup-url-sha256":"` + test.stored + `"`
			}
			inspect := `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none"` + startupLabel + `,"org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`
			commands := &fakeCommands{steps: []commandStep{
				{contains: "container ls", out: name + "\n"},
				{contains: "container inspect", out: inspect},
			}}
			browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", StartupURL: test.configured}
			if _, err := browser.EnsureStarted(context.Background()); err == nil || !strings.Contains(err.Error(), "different startup site settings") {
				t.Fatalf("EnsureStarted() error = %v; want explicit-recreate diagnostic", err)
			}
			if len(commands.calls) != 3 {
				t.Fatalf("stale browser received additional Docker commands: %#v", commands.calls)
			}
		})
	}
}

func TestDockerBrowserFailsClosedForStaleContainerPolicyOrHelper(t *testing.T) {
	for _, labels := range []string{
		`"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"`,
		`"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none"`,
	} {
		commands := &fakeCommands{steps: []commandStep{
			{contains: "container ls", out: "browser-authentication-helper-controller\n"},
			{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{` + labels + `}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
		}}
		browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", AccessConfigJSON: `{"allowed_urls":["https://example.test:8443/private"]}`}
		if _, err := browser.EnsureStarted(context.Background()); err == nil {
			t.Fatal("EnsureStarted() accepted stale policy or browser helper metadata")
		}
		if len(commands.calls) != 3 {
			t.Fatalf("Docker changed a stale browser container: %#v", commands.calls)
		}
	}
}

func TestDockerBrowserRejectsInvalidAccessPolicyBeforeRunningCommands(t *testing.T) {
	commands := &fakeCommands{}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", AccessConfigJSON: `{"unknown":true}`}
	if _, err := browser.EnsureStarted(context.Background()); err == nil {
		t.Fatal("EnsureStarted() accepted invalid access policy")
	}
	if len(commands.calls) != 0 {
		t.Fatalf("Docker commands ran before policy validation: %#v", commands.calls)
	}
}

func TestDockerBrowserReloadBrowserTabUsesExactOriginAndBoundedDeadline(t *testing.T) {
	origin := "https://app.example"
	commands := &fakeCommands{steps: []commandStep{
		{contains: "browser-authentication-helper reload", out: `{"status":"reloaded","tabs":1}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", ContainerName: "aact-browser-test"}
	status, tabs, err := browser.ReloadBrowserTab(context.Background(), origin)
	if err != nil || status != "reloaded" || tabs != 1 {
		t.Fatalf("ReloadBrowserTab() = %q, %d, %v; want reloaded, one tab", status, tabs, err)
	}
	if len(commands.stdinCalls) != 1 {
		t.Fatalf("helper calls = %d; want only reload", len(commands.stdinCalls))
	}
	if got := strings.Join(commands.stdinCalls[0].args, " "); !strings.Contains(got, "browser-authentication-helper reload") {
		t.Fatalf("helper call = %q; want origin-scoped reload", got)
	}
	if remaining := time.Until(commands.stdinCalls[0].deadline); remaining <= 30*time.Second || remaining > 40*time.Second {
		t.Fatalf("reload helper deadline has %v remaining; want >30s and <=40s", remaining)
	}
	var reloadRequest map[string]string
	if err := json.Unmarshal(commands.stdinCalls[0].stdin, &reloadRequest); err != nil || len(reloadRequest) != 1 || reloadRequest["origin"] != origin {
		t.Fatalf("reload input = %#v, %v; want the exact requested origin", reloadRequest, err)
	}
}

func TestDockerBrowserReportsReloadOutcomesWithoutExtraAdapterAttempts(t *testing.T) {
	for _, test := range []struct {
		name       string
		reloadOut  string
		reloadErr  error
		wantStatus string
		wantTabs   int
		wantErr    bool
	}{
		{name: "no matching tab", reloadOut: `{"status":"not_found","tabs":0}`, wantStatus: "not_found"},
		{name: "multiple matching tabs", reloadOut: `{"status":"ambiguous","tabs":2}`, wantStatus: "ambiguous", wantTabs: 2},
		{name: "origin not allowed", reloadOut: `{"status":"not_configured","tabs":0}`, wantStatus: "not_configured"},
		{name: "reload helper error", reloadErr: errors.New("synthetic docker failure"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands := &fakeCommands{steps: []commandStep{{contains: "browser-authentication-helper reload", out: test.reloadOut, err: test.reloadErr}}}
			browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", ContainerName: "aact-browser-test"}
			status, tabs, err := browser.ReloadBrowserTab(context.Background(), "https://app.example")
			if test.wantErr {
				if err == nil || strings.Contains(err.Error(), "synthetic docker failure") {
					t.Fatalf("ReloadBrowserTab() error = %v; want sanitized reload error", err)
				}
			} else if err != nil || status != test.wantStatus || tabs != test.wantTabs {
				t.Fatalf("ReloadBrowserTab() = %q, %d, %v; want %q, %d", status, tabs, err, test.wantStatus, test.wantTabs)
			}
			if len(commands.stdinCalls) != 1 {
				t.Fatalf("helper calls = %d; want only reload", len(commands.stdinCalls))
			}
		})
	}
}

func TestDockerBrowserRejectsReloadWithoutInputRunner(t *testing.T) {
	commands := &fakeCommands{}
	browser := DockerBrowser{Commands: noInputFakeCommands{inner: commands}, ProfileVolume: "profile-test", ContainerName: "aact-browser-test"}
	_, _, err := browser.ReloadBrowserTab(context.Background(), "https://app.example")
	if err == nil {
		t.Fatal("ReloadBrowserTab() accepted a command runner without private input support")
	}
	if len(commands.calls) != 0 {
		t.Fatalf("commands ran despite missing input support: %#v", commands.calls)
	}
}

func TestDockerBrowserRefusesUnownedNameCollision(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"other/image:latest","Labels":{}},"State":{"Running":true},"Mounts":[],"NetworkSettings":{"Ports":{}}}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	_, err := browser.EnsureStarted(context.Background())
	if err == nil || len(commands.calls) != 3 || strings.Contains(strings.Join(commands.calls[2].args, " "), " start ") || strings.Contains(strings.Join(commands.calls[2].args, " "), " run ") {
		t.Fatalf("EnsureStarted() error=%v, commands=%d; want collision failure without mutation", err, len(commands.calls))
	}
}

func TestDockerBrowserUsesHostConfiguredContainerName(t *testing.T) {
	name := "aact-browser-controller-e2e"
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: ""},
		{contains: "--name " + name, out: "container-id"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test", ContainerName: name}
	if _, err := browser.EnsureStarted(context.Background()); err != nil {
		t.Fatalf("EnsureStarted() error = %v", err)
	}
	if strings.Contains(strings.Join(commands.calls[2].args, " "), "AACT_BROWSER_ACCESS_CONFIG_JSON") {
		t.Fatal("unconfigured access policy was injected; absent config must preserve unrestricted navigation")
	}
}

func TestDockerBrowserRejectsPausedOwnedContainer(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":true},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	if _, err := browser.EnsureStarted(context.Background()); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("EnsureStarted() error = %v; want paused-state diagnostic", err)
	}
	if !strings.Contains(strings.Join(commands.calls[2].args, " "), ".State.Paused") {
		t.Fatal("Docker inspect did not request the paused-state field")
	}
}

func TestDockerBrowserRunsGenericAdapterForNamedCookiesAtConfiguredOrigin(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
		{contains: "browser-authentication-helper run-adapter", out: `{"status":"selected","count":2}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	result, err := browser.RunBrowserAuthAdapter(context.Background(), BrowserAuthRequest{
		Origin:   "https://portal.example.test",
		Selector: BrowserAuthSelector{Kind: "cookie", Names: []string{"session_id", "csrf_token"}, Match: "exact"},
		Adapter: BrowserAuthAdapter{
			Source: "def run(credentials, config): return {'status': config['expected']}",
			Config: map[string]any{"expected": "selected"},
		},
	})
	if err != nil {
		t.Fatalf("RunBrowserAuthAdapter() error = %v", err)
	}
	if string(result) != `{"count":2,"status":"selected"}` {
		t.Fatalf("RunBrowserAuthAdapter() result = %s", result)
	}
	if len(commands.stdinCalls) != 1 {
		t.Fatalf("helper stdin calls = %d; want one consistent snapshot call", len(commands.stdinCalls))
	}
	for _, call := range commands.stdinCalls {
		var request struct {
			Origin   string `json:"origin"`
			Selector struct {
				Kind  string   `json:"kind"`
				Names []string `json:"names"`
				Match string   `json:"match"`
			} `json:"selector"`
			Adapter struct {
				Source string         `json:"source"`
				Config map[string]any `json:"config"`
			} `json:"adapter"`
		}
		if err := json.Unmarshal(call.stdin, &request); err != nil {
			t.Fatalf("decode helper request: %v", err)
		}
		if request.Origin != "https://portal.example.test" || request.Selector.Kind != "cookie" || strings.Join(request.Selector.Names, ",") != "session_id,csrf_token" || request.Selector.Match != "exact" || request.Adapter.Source == "" || request.Adapter.Config["expected"] != "selected" {
			t.Fatalf("helper request = %#v", request)
		}
		if strings.Contains(strings.Join(call.args, " "), "session_id") || strings.Contains(strings.Join(call.args, " "), "csrf_token") {
			t.Fatalf("cookie name appeared in Docker arguments: %q", call.args)
		}
	}
}

func TestDockerBrowserGenericAdapterRejectsUnboundedCookieListsBeforeStarting(t *testing.T) {
	browser := DockerBrowser{}
	tooMany := make([]string, 129)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("cookie-%d", i)
	}
	if _, err := browser.RunBrowserAuthAdapter(context.Background(), BrowserAuthRequest{
		Origin: "https://portal.example.test", Selector: BrowserAuthSelector{Kind: "cookie", Names: tooMany},
	}); err == nil || browser.Commands != nil {
		t.Fatalf("RunBrowserAuthAdapter() error = %v; want request validation before browser access", err)
	}
}

func TestDockerBrowserGenericCookieReadRejectsMalformedOriginBeforeStarting(t *testing.T) {
	for _, origin := range []string{"https://bad host.example", "https://portal.example:70000", "https://user@portal.example"} {
		t.Run(origin, func(t *testing.T) {
			browser := DockerBrowser{}
			_, err := browser.ReadBrowserCookies(context.Background(), origin, []string{"session_id"})
			if err == nil || !strings.Contains(err.Error(), "origin is invalid") {
				t.Fatalf("ReadBrowserCookies() error = %v; want invalid-origin rejection before browser access", err)
			}
		})
	}
}

func TestDockerBrowserReadsNamedCookiesWithoutCallerAdapterCode(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
		{contains: "browser-authentication-helper run-adapter", out: `{"credentials":[{"key":"session_id","value":"synthetic-session"},{"key":"csrf_token","value":"synthetic-csrf"}]}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	cookies, err := browser.ReadBrowserCookies(context.Background(), "https://portal.example.test", []string{"session_id", "csrf_token"})
	if err != nil {
		t.Fatalf("ReadBrowserCookies() error = %v", err)
	}
	if len(cookies) != 2 || cookies[0] != (BrowserCookie{Name: "session_id", Value: "synthetic-session"}) || cookies[1] != (BrowserCookie{Name: "csrf_token", Value: "synthetic-csrf"}) {
		t.Fatalf("ReadBrowserCookies() = %#v", cookies)
	}
	if len(commands.stdinCalls) != 1 {
		t.Fatalf("helper stdin calls = %d; want one snapshot for all named cookies", len(commands.stdinCalls))
	}
	for _, call := range commands.stdinCalls {
		if strings.Contains(string(call.stdin), "synthetic-session") || strings.Contains(string(call.stdin), "synthetic-csrf") {
			t.Fatal("cookie values appeared in helper request input")
		}
		if strings.Contains(strings.Join(call.args, " "), "synthetic-") {
			t.Fatalf("cookie value appeared in Docker arguments: %q", call.args)
		}
	}
	if commands.stdinCalls[0].deadline.IsZero() {
		t.Fatal("generic cookie helper call has no operation deadline")
	}
}

func TestDockerBrowserCookieBatchFailsClosedWhenOneNameIsMissing(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
		{contains: "browser-authentication-helper run-adapter", err: errors.New("browser_auth_error=cookie selector did not match exactly one cookie")},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	if cookies, err := browser.ReadBrowserCookies(context.Background(), "https://portal.example.test", []string{"session_id", "missing"}); err == nil || cookies != nil || strings.Contains(err.Error(), "missing") {
		t.Fatalf("ReadBrowserCookies() = %#v, %v; want sanitized all-or-nothing failure", cookies, err)
	}
	if len(commands.stdinCalls) != 1 {
		t.Fatalf("helper calls = %d; want one batch call", len(commands.stdinCalls))
	}
}

func TestDockerBrowserRunsGenericSessionStorageSelector(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
		{contains: "browser-authentication-helper run-adapter", out: `{"credentials":[{"key":"auth.access","value":"synthetic-access"},{"key":"auth.refresh","value":"synthetic-refresh"}]}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	entries, err := browser.ReadBrowserSessionStorage(context.Background(), "https://portal.example.test", "auth.", "prefix", false)
	if err != nil {
		t.Fatalf("ReadBrowserSessionStorage() error = %v", err)
	}
	want := []BrowserStorageEntry{{Key: "auth.access", Value: "synthetic-access"}, {Key: "auth.refresh", Value: "synthetic-refresh"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("ReadBrowserSessionStorage() = %#v; want %#v", entries, want)
	}
	var request struct {
		Selector BrowserAuthSelector `json:"selector"`
	}
	if err := json.Unmarshal(commands.stdinCalls[0].stdin, &request); err != nil {
		t.Fatalf("decode helper request: %v", err)
	}
	if request.Selector.Kind != "sessionStorage" || request.Selector.Name != "auth." || request.Selector.Match != "prefix" {
		t.Fatalf("sessionStorage selector = %#v", request.Selector)
	}
}

func TestDockerBrowserAllowsEmptyAllSessionStorageSelection(t *testing.T) {
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls", out: "browser-authentication-helper-controller\n"},
		{contains: "container inspect", out: `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"profile-test","aact.browser-controller.access-config-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true,"Paused":false},"Mounts":[{"Type":"volume","Name":"profile-test","Destination":"/home/kasm-user"}],"NetworkSettings":{"Ports":{"6901/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}}}`},
		{contains: "browser-authentication-helper run-adapter", out: `{"credentials":[]}`},
	}}
	browser := DockerBrowser{Commands: commands, ProfileVolume: "profile-test"}
	entries, err := browser.ReadBrowserSessionStorage(context.Background(), "https://portal.example.test", "", "all", true)
	if err != nil || len(entries) != 0 {
		t.Fatalf("ReadBrowserSessionStorage() = %#v, %v; want empty success", entries, err)
	}
	var request struct {
		Selector BrowserAuthSelector `json:"selector"`
	}
	if err := json.Unmarshal(commands.stdinCalls[0].stdin, &request); err != nil {
		t.Fatalf("decode helper request: %v", err)
	}
	if request.Selector.Kind != "sessionStorage" || request.Selector.Match != "all" || !request.Selector.AllowEmpty {
		t.Fatalf("sessionStorage selector = %#v", request.Selector)
	}
}

func TestDockerBrowserReportsOwnedMCPRuntimeLivenessReadOnly(t *testing.T) {
	id := strings.Repeat("a", 64)
	commands := &fakeCommands{steps: []commandStep{
		{contains: "container ls --all --filter id=" + id, out: id[:12] + "\n"},
		{contains: "container inspect --format", out: `{"ID":"` + id + `","State":{"Running":false,"Restarting":true,"Status":"restarting"}}`},
	}}
	browser := DockerBrowser{Commands: commands, MCPContainerID: id}
	active, err := browser.MCPRuntimeRunning(context.Background())
	if err != nil || !active {
		t.Fatalf("MCPRuntimeRunning() = %v, %v; want restarting runtime considered active", active, err)
	}
	if len(commands.calls) != 2 || strings.Contains(strings.Join(commands.calls[0].args, " "), "start") || strings.Contains(strings.Join(commands.calls[1].args, " "), "stop") {
		t.Fatalf("liveness check changed runtime or used unexpected calls: %#v", commands.calls)
	}
}

func TestDockerBrowserReportsRemovedMCPRuntimeWithoutMutation(t *testing.T) {
	id := strings.Repeat("b", 64)
	commands := &fakeCommands{steps: []commandStep{{contains: "container ls --all --filter id=" + id, out: ""}}}
	browser := DockerBrowser{Commands: commands, MCPContainerID: id}
	active, err := browser.MCPRuntimeRunning(context.Background())
	if err != nil || active {
		t.Fatalf("MCPRuntimeRunning() = %v, %v; want removed runtime inactive", active, err)
	}
	if len(commands.calls) != 1 {
		t.Fatalf("removed runtime check made unexpected Docker calls: %#v", commands.calls)
	}
}
