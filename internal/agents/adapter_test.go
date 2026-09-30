package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	args []string
	env  map[string]string
}
type captureRunner struct {
	calls  []call
	get    []byte
	getErr error
}

func (r *captureRunner) Run(_ context.Context, args []string, _ string, _ []byte, env map[string]string, _ func([]byte)) ([]byte, error) {
	r.calls = append(r.calls, call{args, env})
	for _, a := range args {
		if a == "get" {
			return r.get, r.getErr
		}
	}
	return nil, nil
}
func TestCodexArgsAndExplicitHome(t *testing.T) {
	r := &captureRunner{getErr: errors.New("MCP server 'local' not found")}
	a, err := For("codex", r)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := ResolveEnvironment("codex", "codex", t.TempDir())
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", Transport: "http"}
	if err = a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) < 2 {
		t.Fatalf("no commands %v", r.calls)
	}
	last := r.calls[len(r.calls)-1]
	if !reflect.DeepEqual(last.args, []string{"codex", "mcp", "add", "local", "--url", "http://localhost:1/mcp"}) || last.env["HOME"] != e.Home || last.env["CODEX_HOME"] != filepath.Join(e.Home, ".codex") {
		t.Fatalf("%#v", last)
	}
}
func TestCopilotCLIArgs(t *testing.T) {
	r := &captureRunner{getErr: errors.New("MCP server 'local' not found")}
	a, _ := For("copilot-cli", r)
	e, _ := ResolveEnvironment("copilot", "copilot-cli", t.TempDir())
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", Transport: "http", TimeoutMS: 30000}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) == 0 {
		t.Fatal("no commands")
	}
	last := r.calls[len(r.calls)-1]
	if !reflect.DeepEqual(last.args, []string{"copilot", "mcp", "add", "--transport", "http", "--timeout", "30000", "local", "http://localhost:1/mcp"}) {
		t.Fatalf("%#v", last)
	}
}
func configFixture(t *testing.T, kind, body string) (Adapter, Environment) {
	t.Helper()
	e, err := ResolveEnvironment("agent", kind, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if e.ConfigPath == "" {
		e.ConfigPath = filepath.Join(e.Home, "manual.json")
	}
	os.MkdirAll(filepath.Dir(e.ConfigPath), 0755)
	if body != "" {
		os.WriteFile(e.ConfigPath, []byte(body), 0644)
	}
	a, err := For(kind, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, e
}
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := standardJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestOpenCodeUpdatesExistingJSONAndJSONC(t *testing.T) {
	a, e := configFixture(t, "opencode", `{"theme":"dark","mcp":{}}`)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	os.WriteFile(jsonc, []byte("{ // comment\n \"model\":\"sample\",\n}"), 0644)
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.ConfigPath, jsonc} {
		v := readJSON(t, p)
		m := server(t, v, "mcp", "local")
		if m["url"] != reg.URL || m["type"] != "remote" || m["timeout"] != float64(30000) {
			t.Fatalf("%v", v)
		}
	}
}
func TestJSONCCommentsAndUnrelatedEntriesPreserved(t *testing.T) {
	body := "{\n // retain theme comment\n \"theme\": \"dark\",\n \"mcp\": {\n // retained server comment\n \"other\": {\"url\":\"http://other\"},\n },\n}\n"
	a, e := configFixture(t, "opencode", body)
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 1}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(e.ConfigPath)
	for _, snippet := range []string{"// retain theme comment", "\"theme\": \"dark\"", "// retained server comment", "\"other\": {\"url\":\"http://other\"}"} {
		if !strings.Contains(string(b), snippet) {
			t.Fatalf("lost %q in %s", snippet, b)
		}
	}
	e.Owned = map[string]Registration{"local": reg}
	if err := a.Unregister(context.Background(), e, "local"); err != nil {
		t.Fatal(err)
	}
	v := readJSON(t, e.ConfigPath)
	servers, ok := v["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("missing servers: %v", v)
	}
	if servers["local"] != nil || servers["other"] == nil {
		t.Fatal("wrong entry removed")
	}
}
func TestMalformedConfigRemainsUnchanged(t *testing.T) {
	for _, body := range []string{"{broken", `[]`, `{"mcp":true}`, `{"mcp":{},"mcp":{}}`} {
		a, e := configFixture(t, "opencode", body)
		if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1"}); err == nil {
			t.Fatalf("accepted %s", body)
		}
		b, _ := os.ReadFile(e.ConfigPath)
		if string(b) != body {
			t.Fatal("malformed file modified")
		}
	}
}
func TestIntellijUsesMcpServers(t *testing.T) {
	a, e := configFixture(t, "intellij-ai-assistant", `{}`)
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1"}); err != nil {
		t.Fatal(err)
	}
	if readJSON(t, e.ConfigPath)["mcpServers"] == nil {
		t.Fatal("wrong config shape")
	}
}
func TestCopilotIntellijUsesServers(t *testing.T) {
	a, e := configFixture(t, "copilot-intellij", `{}`)
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1"}); err != nil {
		t.Fatal(err)
	}
	if readJSON(t, e.ConfigPath)["servers"] == nil {
		t.Fatal("wrong config shape")
	}
	a, e = configFixture(t, "copilot-intellij", "")
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1"}); err == nil || !strings.Contains(err.Error(), "Add MCP Tools") {
		t.Fatalf("missing prerequisite: %v", err)
	}
}
func TestChangedOwnedRegistrationRefused(t *testing.T) {
	a, e := configFixture(t, "intellij-ai-assistant", `{"mcpServers":{"local":{"url":"http://changed"}}}`)
	reg := Registration{Name: "local", URL: "http://original"}
	e.Owned = map[string]Registration{"local": reg}
	if err := a.Unregister(context.Background(), e, "local"); err == nil {
		t.Fatal("edited registration removed")
	}
	if err := a.Register(context.Background(), e, reg); err == nil {
		t.Fatal("edited registration overwritten")
	}
}
func TestForeignRegistrationRefused(t *testing.T) {
	a, e := configFixture(t, "intellij-ai-assistant", `{"mcpServers":{"local":{"url":"http://foreign"}}}`)
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://foreign"}); err == nil {
		t.Fatal("unowned matching registration adopted")
	}
}
func TestGenericReturnsConfigurationArtifact(t *testing.T) {
	a, e := configFixture(t, "generic-mcp", "")
	reg := Registration{Name: "local", URL: "http://localhost:1"}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	v := readJSON(t, e.ConfigPath)
	if server(t, v, "servers", "local")["url"] != reg.URL || !IsManual(e.Kind) {
		t.Fatal("missing manual artifact")
	}
}

var _ = json.Marshal

func server(t *testing.T, v map[string]any, parent, name string) map[string]any {
	t.Helper()
	p, ok := v[parent].(map[string]any)
	if !ok {
		t.Fatalf("missing %s object: %#v", parent, v)
	}
	entry, ok := p[name].(map[string]any)
	if !ok {
		t.Fatalf("missing %s registration: %#v", name, p)
	}
	return entry
}

func TestCLIChangedOwnedRegistrationRefused(t *testing.T) {
	r := &captureRunner{get: []byte(`{"transport":{"type":"streamable_http","url":"http://changed"}}`)}
	a, _ := For("codex", r)
	e, _ := ResolveEnvironment("agent", "codex", t.TempDir())
	e.Owned = map[string]Registration{"local": {Name: "local", URL: "http://original"}}
	if err := a.Unregister(context.Background(), e, "local"); err == nil {
		t.Fatal("changed registration removed")
	}
	if len(r.calls) != 1 {
		t.Fatalf("mutated commands: %v", r.calls)
	}
}
func TestOpenCodePrevalidatesBothFiles(t *testing.T) {
	a, e := configFixture(t, "opencode", `{"theme":"dark"}`)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	os.WriteFile(jsonc, []byte("{broken"), 0600)
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1"}); err == nil {
		t.Fatal("broken sibling accepted")
	}
	b, _ := os.ReadFile(e.ConfigPath)
	if string(b) != `{"theme":"dark"}` {
		t.Fatal("valid sibling changed before validation")
	}
}

func TestCopilotHumanGetUsesConfigForOwnership(t *testing.T) {
	r := &captureRunner{get: []byte("Name: local\nURL: http://localhost:1\n")}
	a, _ := For("copilot-cli", r)
	e, _ := ResolveEnvironment("agent", "copilot-cli", t.TempDir())
	reg := Registration{Name: "local", URL: "http://localhost:1", TimeoutMS: 30000}
	e.Owned = map[string]Registration{"local": reg}
	os.MkdirAll(filepath.Dir(e.ConfigPath), 0755)
	os.WriteFile(e.ConfigPath, []byte(`{"mcpServers":{"local":{"type":"http","url":"http://localhost:1","timeout":30000}}}`), 0600)
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("matching owned registration mutated: %v", r.calls)
	}
}

func TestCLIQueryFailurePreservesOwnedRegistration(t *testing.T) {
	r := &captureRunner{getErr: errors.New("authentication service unavailable")}
	a, _ := For("codex", r)
	e, _ := ResolveEnvironment("agent", "codex", t.TempDir())
	reg := Registration{Name: "local", URL: "http://original"}
	e.Owned = map[string]Registration{"local": reg}
	if err := a.Unregister(context.Background(), e, "local"); err == nil {
		t.Fatal("query failure treated as absent")
	}
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://replacement"}); err == nil {
		t.Fatal("query failure allowed overwrite")
	}
	for _, call := range r.calls {
		for _, arg := range call.args {
			if arg == "add" || arg == "remove" {
				t.Fatalf("mutation after failed query: %v", r.calls)
			}
		}
	}
}

type cancelBetweenWritesContext struct {
	context.Context
	checks int
}

func (c *cancelBetweenWritesContext) Err() error {
	c.checks++
	if c.checks > 2 {
		return context.Canceled
	}
	return nil
}
func TestOpenCodeCancellationRestoresWrittenSibling(t *testing.T) {
	first := `{"theme":"first","mcp":{}}`
	second := "{ // preserve\n \"theme\":\"second\",\"mcp\":{} }"
	a, e := configFixture(t, "opencode", first)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte(second), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelBetweenWritesContext{Context: context.Background()}
	err := a.Register(ctx, e, Registration{Name: "local", URL: "http://localhost:1/mcp"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation got %v", err)
	}
	for path, want := range map[string]string{e.ConfigPath: first, jsonc: second} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("changed sibling %s: %s %v", path, got, err)
		}
	}
}
