//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/browsercontroller"
)

func TestStoppedLegacyBrowserUpgradesWithoutDeletingProfile(t *testing.T) {
	if os.Getenv("AACT_RUN_BROWSER_DOCKER_TEST") != "1" {
		t.Skip("set AACT_RUN_BROWSER_DOCKER_TEST=1")
	}
	old := "aact/browser-authentication-helper:1.18.0"
	if err := exec.Command("docker", "image", "inspect", old).Run(); err != nil {
		t.Skip("legacy browser image unavailable")
	}
	name := fmt.Sprintf("aact-test-upgrade-%d", time.Now().UnixNano())
	volume := name + "-profile"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = exec.Command("docker", "volume", "rm", volume).Run()
	})
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s failed: %v %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	docker("volume", "create", volume)
	docker("create", "--name", name, "--label", "aact.browser-controller=1", "--label", "aact.browser-controller.profile-volume="+volume, "--label", "aact.browser-controller.access-config-sha256=none", "--label", "aact.browser-controller.startup-url-sha256=none", "--mount", "source="+volume+",target=/home/kasm-user", "--publish", "127.0.0.1::6901", old)
	browser := browsercontroller.NewDockerBrowser(browsercontroller.ServiceConfig{ProfileVolume: volume, ContainerName: name})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	viewer, err := browser.EnsureStarted(ctx)
	if err != nil {
		t.Fatalf("upgrade browser: %v", err)
	}
	if !strings.HasPrefix(viewer, "http://localhost:") {
		t.Fatalf("invalid viewer: %s", viewer)
	}
	got := docker("inspect", "--format", "{{.Config.Image}}", name)
	expected := "aact/browser-authentication-helper:1.18.0-form1"
	if got != expected {
		t.Fatalf("browser still uses %s, expected %s", got, expected)
	}
	docker("volume", "inspect", volume)
}
