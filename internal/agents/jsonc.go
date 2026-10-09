package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/tailscale/hujson"
	"os"
	"path/filepath"
	"strings"
)

func standardJSON(b []byte) (map[string]any, error) {
	v, err := hujson.Parse(append([]byte(nil), b...))
	if err != nil {
		return nil, err
	}
	if err = validateAST(&v); err != nil {
		return nil, err
	}
	v.Standardize()
	var out map[string]any
	if err = json.Unmarshal(v.Pack(), &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("agent config must be an object")
	}
	return out, nil
}
func validateAST(v *hujson.Value) error {
	var problem error
	v.Range(func(node *hujson.Value) bool {
		if problem != nil {
			return false
		}
		if obj, ok := node.Value.(*hujson.Object); ok {
			seen := map[string]bool{}
			for _, member := range obj.Members {
				var name string
				if err := json.Unmarshal([]byte(member.Name.Value.(hujson.Literal)), &name); err != nil {
					problem = err
					return false
				}
				if seen[name] {
					problem = fmt.Errorf("duplicate agent config key %q", name)
					return false
				}
				seen[name] = true
			}
		}
		return true
	})
	return problem
}
func pointer(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func (a *mcpAdapter) configParent() string {
	switch a.kind {
	case "opencode":
		return "mcp"
	case "claude":
		return "mcpServers"
	default:
		return "servers"
	}
}

func (a *mcpAdapter) registrationValue(r Registration) map[string]any {
	var value map[string]any
	if a.kind == "opencode" {
		value = map[string]any{"type": "remote", "url": r.URL, "enabled": true, "oauth": false, "timeout": r.TimeoutMS}
	} else if a.kind == "claude" {
		transport := "http"
		if r.Transport == "sse" {
			transport = "sse"
		}
		value = map[string]any{"type": transport, "url": r.URL}
	} else {
		value = map[string]any{"url": r.URL}
	}
	if len(r.Headers) > 0 {
		value["headers"] = r.Headers
	}
	return value
}
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (a *mcpAdapter) registerWithJSON(ctx context.Context, e Environment, r Registration) error {
	if err := validate(r); err != nil {
		return err
	}
	return a.updateJSON(ctx, e, r.Name, &r)
}
func (a *mcpAdapter) unregisterWithJSON(ctx context.Context, e Environment, name string) error {
	return a.updateJSON(ctx, e, name, nil)
}

type fileEdit struct {
	path              string
	original, updated []byte
	existed           bool
	mode              os.FileMode
}

func (a *mcpAdapter) updateJSON(ctx context.Context, e Environment, name string, r *Registration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := e.ConfigPath
	if path == "" {
		resolved, err := ResolveEnvironment(e.ID, a.kind, e.Home)
		if err != nil {
			return err
		}
		path = resolved.ConfigPath
	}
	paths := []string{path}
	primary := path
	if a.kind == "opencode" {
		ext := filepath.Ext(path)
		other := ""
		if ext == ".json" {
			other = strings.TrimSuffix(path, ext) + ".jsonc"
		} else if ext == ".jsonc" {
			other = strings.TrimSuffix(path, ext) + ".json"
		}
		if other != "" {
			if _, err := os.Stat(other); err == nil {
				paths = append(paths, other)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		jsonc := path
		if ext == ".json" {
			jsonc = strings.TrimSuffix(path, ext) + ".jsonc"
		}
		if _, err := os.Stat(jsonc); err == nil {
			primary = jsonc
		} else if !os.IsNotExist(err) {
			return err
		} else if _, err := os.Stat(path); os.IsNotExist(err) && len(paths) == 1 {
			// Match OpenCode's default config file when neither sibling exists.
			primary = jsonc
			paths = []string{jsonc}
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	edits := []fileEdit{}
	for _, file := range paths {
		original, err := os.ReadFile(file)
		existed := err == nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !existed {
			if r == nil {
				continue
			}
			original = []byte("{}\n")
			if a.kind == "opencode" {
				original = []byte("{\"$schema\":\"https://opencode.ai/config.json\"}\n")
			}
		}
		document, err := standardJSON(original)
		if err != nil {
			return fmt.Errorf("invalid agent config %s: %w", file, err)
		}
		var servers map[string]any
		configParent := a.configParent()
		if parent, ok := document[configParent]; ok {
			var valid bool
			servers, valid = parent.(map[string]any)
			if !valid {
				return fmt.Errorf("agent config %s must contain an object at %s", file, configParent)
			}
		}
		current := servers[name]
		if current != nil {
			owned, ok := e.Owned[name]
			if !ok {
				return fmt.Errorf("refusing foreign MCP registration %q in %s", name, file)
			}
			if !sameJSON(current, a.registrationValue(owned)) {
				return fmt.Errorf("owned MCP registration %q changed in %s", name, file)
			}
		}
		fileRegistration := r
		if a.kind == "opencode" && r != nil && file != primary {
			// Retire an owned shadow entry. Keeping its old value would make the
			// next update fail ownership verification after the ledger advances.
			fileRegistration = nil
		}
		if fileRegistration == nil && current == nil {
			continue
		}
		v, err := hujson.Parse(original)
		if err != nil {
			return err
		}
		ops := []map[string]any{}
		if servers == nil {
			ops = append(ops, map[string]any{"op": "add", "path": "/" + pointer(a.configParent()), "value": map[string]any{}})
		}
		op := map[string]any{"op": "remove", "path": "/" + pointer(a.configParent()) + "/" + pointer(name)}
		if fileRegistration != nil {
			op["op"] = "add"
			op["value"] = a.registrationValue(*fileRegistration)
		}
		ops = append(ops, op)
		patch, _ := json.Marshal(ops)
		if err = v.Patch(patch); err != nil {
			return err
		}
		updated := v.Pack()
		if _, err = standardJSON(updated); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if info, err := os.Stat(file); err == nil {
			mode = info.Mode().Perm()
		}
		edits = append(edits, fileEdit{file, original, updated, existed, mode})
	}
	restore := func(n int, cause error) error {
		var restoration []error
		for j := n - 1; j >= 0; j-- {
			prior := edits[j]
			if prior.existed {
				restoration = append(restoration, state.WriteAtomic(prior.path, prior.original, prior.mode))
			} else {
				restoration = append(restoration, os.Remove(prior.path))
			}
		}
		return errors.Join(append([]error{cause}, restoration...)...)
	}
	for n, edit := range edits {
		if err := ctx.Err(); err != nil {
			return restore(n, err)
		}
		if err := state.WriteAtomic(edit.path, edit.updated, edit.mode); err != nil {
			return restore(n, err)
		}
	}

	return nil
}
