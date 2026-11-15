package browsercontroller

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOfflineAdapterUsesReadOnlyProfileWithoutStartingBrowser(t *testing.T) {
	runner := &fakeCommands{steps: []commandStep{{contains: "volume inspect", out: "fixture-profile"}, {contains: "run", out: `{"status":"refreshed"}`}}}
	browser := DockerBrowser{Commands: runner, ProfileVolume: "fixture-profile"}
	request := BrowserAuthRequest{Origin: "https://example.test", Selector: BrowserAuthSelector{Kind: "cookie", Match: "exact", Names: []string{"session"}}, Adapter: BrowserAuthAdapter{Source: "def run(credentials, config): return {}"}}
	result, err := browser.ReadOffline(context.Background(), request)
	if err != nil || !strings.Contains(string(result), "refreshed") {
		t.Fatalf("offline result %s: %v", result, err)
	}
	if len(runner.stdinCalls) != 1 {
		t.Fatalf("private input calls %d", len(runner.stdinCalls))
	}
	args := strings.Join(runner.stdinCalls[0].args, " ")
	if !strings.Contains(args, "run-adapter-offline") || !strings.Contains(args, "source=fixture-profile,target=/home/kasm-user,readonly") {
		t.Fatalf("unsafe offline invocation: %s", args)
	}
	if strings.Contains(args, "session") {
		t.Fatal("selected credential was exposed in arguments")
	}
}

func TestOfflineAdapterRefusesProfileUsedByRunningContainer(t *testing.T) {
	runner := &fakeCommands{volumeUses: "other", volumeUseStates: map[string]string{"other": "running"}}
	browser := DockerBrowser{Commands: runner, ProfileVolume: "fixture-profile"}
	request := BrowserAuthRequest{Origin: "https://example.test", Selector: BrowserAuthSelector{Kind: "cookie", Match: "exact", Names: []string{"session"}}, Adapter: BrowserAuthAdapter{Source: "def run(credentials, config): return {}"}}
	if _, err := browser.ReadOffline(context.Background(), request); err == nil {
		t.Fatal("offline read accepted a running profile")
	}
	if len(runner.stdinCalls) != 0 {
		t.Fatal("launched offline helper for running profile")
	}
}

func TestOfflineAdapterDoesNotCreateMissingVolume(t *testing.T) {
	runner := &fakeCommands{steps: []commandStep{{contains: "volume inspect", err: errors.New("missing volume")}}}
	browser := DockerBrowser{Commands: runner, ProfileVolume: "missing-profile"}
	request := BrowserAuthRequest{Origin: "https://example.test", Selector: BrowserAuthSelector{Kind: "cookie", Match: "exact", Names: []string{"session"}}, Adapter: BrowserAuthAdapter{Source: "def run(credentials, config): return {}"}}
	if _, err := browser.ReadOffline(context.Background(), request); err == nil {
		t.Fatal("missing volume accepted")
	}
	if len(runner.stdinCalls) != 0 {
		t.Fatal("offline helper would create missing volume")
	}
}

func TestGenericRequestSupportsLocalStorage(t *testing.T) {
	request := BrowserAuthRequest{Origin: "https://example.test", Selector: BrowserAuthSelector{Kind: "localStorage", Match: "prefix", Name: "auth."}, Adapter: BrowserAuthAdapter{Source: "def run(credentials, config): return {}"}}
	if err := validateGenericBrowserAuthRequest(request); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveBrowserStopsAndRemovesOwnedContainerWithoutRemovingVolume(t *testing.T) {
	name := "fixture-browser"
	inspect := `{"Config":{"Image":"aact/browser-authentication-helper:1.18.0","Labels":{"aact.browser-controller":"1","aact.browser-controller.profile-volume":"fixture-profile","aact.browser-controller.access-config-sha256":"none","aact.browser-controller.startup-url-sha256":"none","org.aact.browser-authentication-helper":"1","org.aact.browser-access-controller":"1"}},"State":{"Running":true},"Mounts":[{"Type":"volume","Name":"fixture-profile","Destination":"/home/kasm-user"}]}`
	runner := &fakeCommands{steps: []commandStep{{contains: "container ls", out: name}, {contains: "container inspect", out: inspect}, {contains: "container stop"}, {contains: "container rm"}}}
	b := DockerBrowser{Commands: runner, ProfileVolume: "fixture-profile", ContainerName: name}
	if err := b.RemoveBrowser(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " "), "volume rm") || strings.Contains(strings.Join(call.args, " "), " --volumes") {
			t.Fatal("browser cleanup removed its profile volume")
		}
	}
}
