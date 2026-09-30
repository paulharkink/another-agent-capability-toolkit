package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type cliAdapter struct {
	kind   string
	runner process.Executor
}

func (a cliAdapter) env(e Environment) (map[string]string, error) {
	if e.Home == "" {
		return nil, errors.New("CLI agent home must be explicit")
	}
	env := map[string]string{"HOME": e.Home, "USERPROFILE": e.Home, "XDG_CONFIG_HOME": filepath.Join(e.Home, ".config")}
	if a.kind == "codex" {
		home := filepath.Join(e.Home, ".codex")
		if e.ConfigPath != "" {
			home = filepath.Dir(e.ConfigPath)
		}
		env["CODEX_HOME"] = home
	}
	return env, nil
}
func (a cliAdapter) program() string {
	if a.kind == "codex" {
		return "codex"
	}
	return "copilot"
}
func (a cliAdapter) current(ctx context.Context, e Environment, name string) (*Registration, error) {
	env, err := a.env(e)
	if err != nil {
		return nil, err
	}
	args := []string{a.program(), "mcp", "get", name}
	if a.kind == "codex" {
		args = append(args, "--json")
	}
	var diagnostic []byte
	output, err := a.runner.Run(ctx, args, e.Home, nil, env, func(chunk []byte) {
		space := (64 << 10) - len(diagnostic)
		if space > 0 {
			if len(chunk) > space {
				chunk = chunk[:space]
			}
			diagnostic = append(diagnostic, chunk...)
		}
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if a.kind == "copilot-cli" {
			if existing, lookupErr := copilotConfigRegistration(e, name); lookupErr == nil {
				return existing, nil
			}
		}
		if knownMissingRegistration(name, string(diagnostic)+"\n"+string(output)+"\n"+err.Error()) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot verify existing %s registration %q: %w", a.program(), name, err)
	}
	var value map[string]any
	if err = json.Unmarshal(output, &value); err != nil {
		if a.kind == "copilot-cli" {
			if existing, lookupErr := copilotConfigRegistration(e, name); lookupErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("cannot verify existing %s registration %q: expected JSON from mcp get", a.program(), name)
	}
	r := Registration{Name: name}
	if u, ok := value["url"].(string); ok {
		r.URL = u
	}
	if tr, ok := value["transport"].(map[string]any); ok {
		if u, ok := tr["url"].(string); ok {
			r.URL = u
		}
		if typ, ok := tr["type"].(string); ok {
			r.Transport = typ
		}
	}
	if timeout, ok := value["timeout"].(float64); ok {
		r.TimeoutMS = int(timeout)
	}
	if r.URL == "" {
		return nil, fmt.Errorf("cannot verify existing %s registration %q URL", a.program(), name)
	}
	return &r, nil
}
func (a cliAdapter) verify(e Environment, current *Registration, name string) error {
	if current == nil {
		return nil
	}
	expected, owned := e.Owned[name]
	if !owned {
		return fmt.Errorf("refusing foreign MCP registration %q", name)
	}
	if current.URL != expected.URL {
		return fmt.Errorf("owned MCP registration %q changed", name)
	}
	if a.kind != "codex" && current.TimeoutMS != 0 && current.TimeoutMS != expected.TimeoutMS {
		return fmt.Errorf("owned MCP registration %q timeout changed", name)
	}
	return nil
}
func (a cliAdapter) Register(ctx context.Context, e Environment, r Registration) error {
	if err := validate(r); err != nil {
		return err
	}
	current, err := a.current(ctx, e, r.Name)
	if err != nil {
		return err
	}
	if err = a.verify(e, current, r.Name); err != nil {
		return err
	}
	env, err := a.env(e)
	if err != nil {
		return err
	}
	if current != nil {
		expected := e.Owned[r.Name]
		if expected.URL == r.URL && (a.kind == "codex" || expected.TimeoutMS == r.TimeoutMS) {
			return nil
		}
	}
	// CLI replaces only a positively verified owned name; the coordinator retains
	// its previous ledger entry until registration and record both succeed.
	if current != nil {
		if _, err = a.runner.Run(ctx, []string{a.program(), "mcp", "remove", r.Name}, e.Home, nil, env, nil); err != nil {
			return err
		}
	}
	args := []string{"codex", "mcp", "add", r.Name, "--url", r.URL}
	if a.kind != "codex" {
		args = []string{"copilot", "mcp", "add", "--transport", "http", "--timeout", strconv.Itoa(r.TimeoutMS), r.Name, r.URL}
	}
	_, err = a.runner.Run(ctx, args, e.Home, nil, env, nil)
	if err != nil && current != nil {
		restore := e.Owned[r.Name]
		rollback := []string{"codex", "mcp", "add", restore.Name, "--url", restore.URL}
		if a.kind != "codex" {
			rollback = []string{"copilot", "mcp", "add", "--transport", "http", "--timeout", strconv.Itoa(restore.TimeoutMS), restore.Name, restore.URL}
		}
		_, restoreErr := a.runner.Run(ctx, rollback, e.Home, nil, env, nil)
		return errors.Join(err, restoreErr)
	}
	return err
}
func (a cliAdapter) Unregister(ctx context.Context, e Environment, name string) error {
	current, err := a.current(ctx, e, name)
	if err != nil {
		return err
	}
	if err = a.verify(e, current, name); err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	env, err := a.env(e)
	if err != nil {
		return err
	}
	_, err = a.runner.Run(ctx, []string{a.program(), "mcp", "remove", name}, e.Home, nil, env, nil)
	return err
}

func copilotConfigRegistration(e Environment, name string) (*Registration, error) {
	path := e.ConfigPath
	if path == "" {
		path = filepath.Join(e.Home, ".copilot", "mcp-config.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	document, err := standardJSON(b)
	if err != nil {
		return nil, err
	}
	for _, parent := range []string{"mcpServers", "servers"} {
		servers, ok := document[parent].(map[string]any)
		if !ok {
			continue
		}
		entry, ok := servers[name].(map[string]any)
		if !ok {
			continue
		}
		u, ok := entry["url"].(string)
		if !ok {
			return nil, errors.New("Copilot config registration has no URL")
		}
		reg := Registration{Name: name, URL: u}
		reg.Transport, _ = entry["type"].(string)
		if timeout, ok := entry["timeout"].(float64); ok {
			reg.TimeoutMS = int(timeout)
		}
		return &reg, nil
	}
	if _, ok := document["mcpServers"].(map[string]any); ok {
		return nil, nil
	}
	if _, ok := document["servers"].(map[string]any); ok {
		return nil, nil
	}
	return nil, errors.New("Copilot config does not contain a servers object")
}

func knownMissingRegistration(name, diagnostic string) bool {
	lower := strings.ToLower(diagnostic)
	n := strings.ToLower(name)
	for _, quoted := range []string{"'" + n + "'", "\"" + n + "\"", n} {
		for _, signal := range []string{"no mcp server named " + quoted + " found", "mcp server " + quoted + " not found", "mcp server " + quoted + " does not exist"} {
			if strings.Contains(lower, signal) {
				return true
			}
		}
	}
	return false
}
