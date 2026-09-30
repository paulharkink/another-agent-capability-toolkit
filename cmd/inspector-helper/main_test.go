package main

import (
	"bytes"
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"strings"
	"testing"
)

func TestStrictActionJSONProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantErr     bool
	}{
		{"single object", `{"protocol_version":1,"action":"prepare","target":{"name":"fixture"},"package_dir":"/fixture","state_dir":"/state","inputs":{},"interactive":false}`, false},
		{"trailing object", `{"protocol_version":1} {}`, true},
		{"unknown field", `{"protocol_version":1,"surprise":true}`, true},
		{"null", `null`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			called := false
			handler := func(_ context.Context, id string, q mcp.ActionRequest) (mcp.ActionResult, error) {
				called = true
				if id != "cluster-inspector" || q.Action != "prepare" {
					t.Fatal(q)
				}
				return mcp.ActionResult{AuthRequired: true}, nil
			}
			err := execute(context.Background(), []string{"cluster-inspector", "prepare"}, strings.NewReader(tc.input), &out, &diagnostics, handler)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v output=%s", err, out.String())
			}
			if tc.wantErr && called {
				t.Fatal("invalid protocol reached handler")
			}
			if !tc.wantErr && out.String() != "{\"auth_required\":true}\n" {
				t.Fatal(out.String())
			}
		})
	}
}
