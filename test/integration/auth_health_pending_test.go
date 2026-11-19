package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/main/internal/viewmodel"
)

func TestAuthenticationSnapshotDoesNotReportTimeoutAsFailure(t *testing.T) {
	var phase atomic.Int32
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch phase.Load() {
		case 0:
			time.Sleep(2500 * time.Millisecond)
			fmt.Fprint(w, `{"authenticated":true}`)
		case 1:
			fmt.Fprint(w, `{"authenticated":false}`)
		default:
			fmt.Fprint(w, `{"authenticated":true}`)
		}
	}))
	defer health.Close()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	p := catalog.Package{ID: "grafana", Name: "Grafana", Dir: root, MCP: &catalog.MCP{Name: "grafana", Image: "placeholder", Transport: "streamable-http", HealthPath: "/health"}}
	key := state.Key{Source: "fixture", Package: "grafana", Target: "ot"}
	runtime := &integratedRuntime{store: store, instances: []mcp.Instance{{Key: key, ID: "fixture", Status: "running", Ownership: "local", URL: health.URL + "/mcp"}}}
	svc := app.New(config.Pack{ID: "fixture", Root: root, ProfileRoot: filepath.Join(root, "environments"), Catalog: []catalog.Package{p}}.LegacySource(), store, app.Options{Runtime: runtime})
	if err := svc.CreateProfile(context.Background(), config.ProfileRef{PackID: "fixture", CapabilityID: "grafana", Name: "ot"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.InstanceSnapshot(context.Background(), "grafana")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Instances) != 1 || len(snapshot.Instances[0].MCPs) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if got := snapshot.Instances[0].MCPs[0].AuthenticationStatus; got != viewmodel.AuthenticationNotConfirmed {
		t.Fatalf("slow authentication health status = %q, want not confirmed", got)
	}
	for _, check := range []struct {
		phase int32
		want  string
	}{{1, viewmodel.AuthenticationFailed}, {2, viewmodel.AuthenticationComplete}} {
		phase.Store(check.phase)
		snapshot, err = svc.InstanceSnapshot(context.Background(), "grafana")
		if err != nil {
			t.Fatal(err)
		}
		if got := snapshot.Instances[0].MCPs[0].AuthenticationStatus; got != check.want {
			t.Fatalf("authentication health status = %q, want %q", got, check.want)
		}
	}
}
