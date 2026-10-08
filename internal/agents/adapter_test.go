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

func TestCopilotCLIUsesCOPILOTHomeAndLeavesJSONConfigAsCLIManaged(t *testing.T) {
	home := t.TempDir()
	configRoot := filepath.Join(t.TempDir(), "copilot-home")
	t.Setenv("COPILOT_HOME", configRoot)
	e, err := ResolveEnvironment("copilot", "copilot-cli", home)
	if err != nil {
		t.Fatal(err)
	}
	e, err = ApplyNativeConfigOverrides(e)
	if err != nil {
		t.Fatal(err)
	}
	if e.ConfigPath != filepath.Join(configRoot, "mcp-config.json") {
		t.Fatalf("COPILOT_HOME config path = %q", e.ConfigPath)
	}
	runner := &captureRunner{getErr: errors.New("MCP server 'local' not found")}
	a, err := For("copilot-cli", runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if call.env["COPILOT_HOME"] != configRoot {
			t.Fatalf("CLI invocation did not use COPILOT_HOME: %#v", call.env)
		}
	}
	if _, err := os.Stat(e.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("adapter bypassed the official CLI and edited its managed config: %v", err)
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
	jsonBefore, _ := os.ReadFile(e.ConfigPath)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	os.WriteFile(jsonc, []byte("{ // comment\n \"model\":\"sample\",\n}"), 0644)
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	jsonAfter, _ := os.ReadFile(e.ConfigPath)
	if string(jsonAfter) != string(jsonBefore) {
		t.Fatalf("lower precedence JSON changed: %s", jsonAfter)
	}
	v := readJSON(t, jsonc)
	m := server(t, v, "mcp", "local")
	if m["url"] != reg.URL || m["type"] != "remote" || m["timeout"] != float64(30000) {
		t.Fatalf("%v", v)
	}
}

func TestHTTPRegistrationIncludesHeadersForOpenCodeClaudeAndGeneric(t *testing.T) {
	for _, kind := range []string{"opencode", "claude", "generic"} {
		t.Run(kind, func(t *testing.T) {
			adapter, _ := For(kind, nil)
			jsonConfig, ok := adapter.(jsonAdapter)
			if !ok {
				t.Fatalf("%s adapter is not JSON-backed", kind)
			}
			registration := Registration{
				Name: "git-provider-github", URL: "http://127.0.0.1:8080/mcp", Transport: "http",
				Headers: map[string]string{"Authorization": "Bearer test-token"},
			}
			value := jsonConfig.value(registration)
			headers, ok := value["headers"].(map[string]string)
			if !ok || headers["Authorization"] != "Bearer test-token" {
				t.Fatalf("registration headers were not serialized: %#v", value)
			}
		})
	}
}

func TestOpenCodeJSONCOnlyDoesNotCreateJSON(t *testing.T) {
	a, e := configFixture(t, "opencode", "")
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte("{ // keep\n \"mcp\":{}\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("JSON sibling unexpectedly created: %v", err)
	}
	if server(t, readJSON(t, jsonc), "mcp", "local")["url"] != reg.URL {
		t.Fatal("effective JSONC config was not updated")
	}
}

func TestOpenCodeBothFilesRegisterInEffectiveJSONCOnly(t *testing.T) {
	a, e := configFixture(t, "opencode", `{"theme":"json","mcp":{}}`)
	before, _ := os.ReadFile(e.ConfigPath)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte("{ // keep\n \"mcp\":{}\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	if err := a.Register(context.Background(), e, reg); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(e.ConfigPath)
	if string(after) != string(before) {
		t.Fatalf("lower precedence JSON changed: %s", after)
	}
	if server(t, readJSON(t, jsonc), "mcp", "local")["url"] != reg.URL {
		t.Fatal("effective JSONC config was not updated")
	}
}

func TestOpenCodeRemovalDoesNotExposeLowerPrecedenceOwnedEntry(t *testing.T) {
	reg := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	entry, _ := json.Marshal(jsonAdapter{kind: "opencode"}.value(reg))
	body := `{"mcp":{"local":` + string(entry) + `}}`
	a, e := configFixture(t, "opencode", body)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	e.Owned = map[string]Registration{"local": reg}
	if err := a.Unregister(context.Background(), e, "local"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{e.ConfigPath, jsonc} {
		servers := readJSON(t, path)["mcp"].(map[string]any)
		if servers["local"] != nil {
			t.Fatalf("owned entry still exposed in %s: %+v", path, servers)
		}
	}
}

func TestOpenCodeUpdateRetiresOwnedLowerPrecedenceEntry(t *testing.T) {
	previous := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	next := Registration{Name: "local", URL: "http://localhost:2/mcp", TimeoutMS: 30000}
	entry, _ := json.Marshal(jsonAdapter{kind: "opencode"}.value(previous))
	body := `{"mcp":{"local":` + string(entry) + `}}`
	a, e := configFixture(t, "opencode", body)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	e.Owned = map[string]Registration{"local": previous}
	if err := a.Register(context.Background(), e, next); err != nil {
		t.Fatal(err)
	}
	e.Owned["local"] = next
	if err := a.Unregister(context.Background(), e, "local"); err != nil {
		t.Fatalf("updated registration could not be removed: %v", err)
	}
	for _, path := range []string{e.ConfigPath, jsonc} {
		if readJSON(t, path)["mcp"].(map[string]any)["local"] != nil {
			t.Fatalf("owned entry remains in %s", path)
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
func TestIntellijAIAssistantJSONAdapterPreservesOtherServers(t *testing.T) {
	for _, kind := range []string{"intellij", "intellij-ai-assistant"} {
		a, e := configFixture(t, kind, "{\"theme\":\"dark\",\"mcpServers\":{\"other\":{\"url\":\"http://other\"}}}")
		reg := Registration{Name: "local", URL: "http://localhost:1/mcp", Transport: "http"}
		if err := a.Register(context.Background(), e, reg); err != nil {
			t.Fatalf("%s register: %v", kind, err)
		}
		v := readJSON(t, e.ConfigPath)
		if v["theme"] != "dark" || server(t, v, "mcpServers", "local")["url"] != reg.URL || server(t, v, "mcpServers", "other")["url"] != "http://other" {
			t.Fatalf("%s changed unrelated config: %+v", kind, v)
		}
		e.Owned = map[string]Registration{"local": reg}
		if err := a.Unregister(context.Background(), e, "local"); err != nil {
			t.Fatalf("%s unregister: %v", kind, err)
		}
		v = readJSON(t, e.ConfigPath)
		if v["theme"] != "dark" || v["mcpServers"].(map[string]any)["local"] != nil || server(t, v, "mcpServers", "other")["url"] != "http://other" {
			t.Fatalf("%s removed unrelated configuration: %+v", kind, v)
		}
	}
}

func TestJetBrainsAIAssistantCreatesDefaultMCPJSONWhenMissing(t *testing.T) {
	a, e := configFixture(t, "intellij", "")
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1/mcp"}); err != nil {
		t.Fatal(err)
	}
	if got := server(t, readJSON(t, e.ConfigPath), "mcpServers", "local")["url"]; got != "http://localhost:1/mcp" {
		t.Fatalf("new JetBrains configuration URL = %v", got)
	}
}

func TestJetBrainsAIAssistantHonorsExplicitConfigPath(t *testing.T) {
	a, e := configFixture(t, "intellij", "{\"mcpServers\":{}}")
	other := filepath.Join(t.TempDir(), "registry-selected", "mcp.json")
	e.ConfigPath = other
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1/mcp"}); err != nil {
		t.Fatal(err)
	}
	if got := server(t, readJSON(t, other), "mcpServers", "local")["url"]; got != "http://localhost:1/mcp" {
		t.Fatalf("explicit JetBrains config path not used: %v", got)
	}
	if readJSON(t, filepath.Join(e.Home, ".ai", "mcp", "mcp.json"))["mcpServers"].(map[string]any)["local"] != nil {
		t.Fatal("adapter wrote default path when explicit path was selected")
	}
}

func TestResolveJetBrainsAIAssistantDefaultMCPPath(t *testing.T) {
	home := t.TempDir()
	e, err := ResolveEnvironment("intellij", "intellij", home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".ai", "mcp", "mcp.json")
	if e.ConfigPath != want {
		t.Fatalf("JetBrains AI Assistant path = %q, want %q", e.ConfigPath, want)
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
	if err := a.Register(context.Background(), e, Registration{Name: "local", URL: "http://localhost:1"}); err != nil {
		t.Fatalf("registration should initialize a missing MCP config: %v", err)
	}
	if got := readJSON(t, e.ConfigPath)["servers"].(map[string]any)["local"].(map[string]any)["url"]; got != "http://localhost:1" {
		t.Fatalf("new Copilot in JetBrains config has wrong server URL: %v", got)
	}
}

func TestResolveWindowsCopilotJetBrainsConfigUsesRoamingAppData(t *testing.T) {
	home := t.TempDir()
	getenv := func(name string) string {
		if name == "APPDATA" {
			return filepath.Join(home, "Roaming")
		}
		return ""
	}
	e, err := resolveEnvironment("copilot-intellij", "copilot-intellij", home, "windows", getenv)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Roaming", "github-copilot", "intellij", "mcp.json")
	if e.ConfigPath != want {
		t.Fatalf("Windows Copilot for JetBrains config = %q, want %q", e.ConfigPath, want)
	}
}

func TestClaudeCodeUserRegistrationPreservesOtherConfiguration(t *testing.T) {
	a, e := configFixture(t, "claude", `{"theme":"dark","mcpServers":{"other":{"type":"http","url":"http://other"}}}`)
	registration := Registration{Name: "local", URL: "http://localhost:1/mcp", Transport: "streamable-http"}
	if err := a.Register(context.Background(), e, registration); err != nil {
		t.Fatal(err)
	}
	document := readJSON(t, e.ConfigPath)
	if document["theme"] != "dark" || server(t, document, "mcpServers", "local")["type"] != "http" || server(t, document, "mcpServers", "other")["url"] != "http://other" {
		t.Fatalf("Claude Code configuration changed unexpectedly: %+v", document)
	}
	e.Owned = map[string]Registration{"local": registration}
	if err := a.Unregister(context.Background(), e, "local"); err != nil {
		t.Fatal(err)
	}
	document = readJSON(t, e.ConfigPath)
	if document["theme"] != "dark" || document["mcpServers"].(map[string]any)["local"] != nil || server(t, document, "mcpServers", "other")["url"] != "http://other" {
		t.Fatalf("Claude Code removal changed unrelated config: %+v", document)
	}
}

func TestClaudeCodeConfigDirectoryOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	e, err := ResolveEnvironment("claude", "claude", t.TempDir())
	if err != nil || e.ConfigPath != filepath.Join(root, ".claude.json") {
		t.Fatalf("Claude Code override ignored: %+v, %v", e, err)
	}
}
func TestChangedOwnedRegistrationRefused(t *testing.T) {
	a, e := configFixture(t, "generic-mcp", `{"servers":{"local":{"url":"http://changed"}}}`)
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
	a, e := configFixture(t, "generic-mcp", `{"servers":{"local":{"url":"http://foreign"}}}`)
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

type cancelBeforeWriteContext struct {
	context.Context
	checks int
}

type cancelBetweenSiblingWritesContext struct {
	context.Context
	checks int
}

func (c *cancelBetweenSiblingWritesContext) Err() error {
	c.checks++
	if c.checks > 2 {
		return context.Canceled
	}
	return nil
}

func TestOpenCodeCancellationRestoresRetiredOwnedShadow(t *testing.T) {
	previous := Registration{Name: "local", URL: "http://localhost:1/mcp", TimeoutMS: 30000}
	next := Registration{Name: "local", URL: "http://localhost:2/mcp", TimeoutMS: 30000}
	entry, _ := json.Marshal(jsonAdapter{kind: "opencode"}.value(previous))
	body := `{"mcp":{"local":` + string(entry) + `}}`
	a, e := configFixture(t, "opencode", body)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	e.Owned = map[string]Registration{"local": previous}
	ctx := &cancelBetweenSiblingWritesContext{Context: context.Background()}
	if err := a.Register(ctx, e, next); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation between files: %v", err)
	}
	for _, path := range []string{e.ConfigPath, jsonc} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != body {
			t.Fatalf("cancellation changed %s: %s, %v", path, content, err)
		}
	}
}

func (c *cancelBeforeWriteContext) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}
func TestOpenCodeCancellationLeavesBothFilesUnchanged(t *testing.T) {
	first := `{"theme":"first","mcp":{}}`
	second := "{ // preserve\n \"theme\":\"second\",\"mcp\":{} }"
	a, e := configFixture(t, "opencode", first)
	jsonc := strings.TrimSuffix(e.ConfigPath, ".json") + ".jsonc"
	if err := os.WriteFile(jsonc, []byte(second), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelBeforeWriteContext{Context: context.Background()}
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
