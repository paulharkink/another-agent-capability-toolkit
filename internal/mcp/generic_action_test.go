package mcp

import (
	"context"
	"encoding/json"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenericActionProfileAndFileCredentials(t *testing.T) {
	f := &actionExec{output: `{}`}
	p := actionPackage()
	profile := config.Profile{Ref: config.ProfileRef{PackID: "company", CapabilityID: "neutral", Name: "production"}}
	_, err := (&ActionRunner{Executor: f}).Run(context.Background(), p, ActionRequest{Action: "authenticate", Profile: profile, Inputs: map[string]any{"credential_path": "/secret/file"}})
	if err != nil || f.calls != 1 || f.request.Profile.Ref != profile.Ref {
		t.Fatalf("request=%+v err=%v", f.request, err)
	}
	b, _ := json.Marshal(f.request)
	if strings.Contains(string(b), `"target"`) {
		t.Fatalf("legacy selection in envelope: %s", b)
	}
}
func TestGenericActionMissingHelperNeverFallsBack(t *testing.T) {
	p := actionPackage()
	p.ID = "grafana-inspector"
	p.Dir = t.TempDir()
	p.MCP.Actions["prepare"].Argv[0] = "bin/inspector-helper"
	_, err := RunAction(context.Background(), p, ActionRequest{Action: "prepare"})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(p.Dir, "bin", "inspector-helper")) {
		t.Fatalf("missing declared path: %v", err)
	}
}
func TestGenericActionAactDoesNotEmbedPackageHelpers(t *testing.T) {
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-deps", "./cmd/aact")
	cmd.Dir = "../.."
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if strings.Contains(string(b), "/internal/packagehelpers") {
		t.Fatal("AACT embeds capability implementation")
	}
}
