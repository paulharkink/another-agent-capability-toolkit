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

type jsonAdapter struct {
	kind, parent    string
	requireExisting bool
}

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
func (a jsonAdapter) value(r Registration) map[string]any {
	if a.kind == "opencode" {
		return map[string]any{"type": "remote", "url": r.URL, "enabled": true, "oauth": false, "timeout": r.TimeoutMS}
	}
	return map[string]any{"url": r.URL}
}
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (a jsonAdapter) Register(ctx context.Context, e Environment, r Registration) error {
	if err := validate(r); err != nil {
		return err
	}
	return a.update(ctx, e, r.Name, &r)
}
func (a jsonAdapter) Unregister(ctx context.Context, e Environment, name string) error {
	return a.update(ctx, e, name, nil)
}

type fileEdit struct {
	path              string
	original, updated []byte
	existed           bool
	mode              os.FileMode
}

func (a jsonAdapter) update(ctx context.Context, e Environment, name string, r *Registration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := e.ConfigPath
	if path == "" {
		if IsManual(a.kind) {
			return errors.New("generic MCP requires an explicit manual artifact ConfigPath")
		}
		resolved, err := ResolveEnvironment(e.ID, a.kind, e.Home)
		if err != nil {
			return err
		}
		path = resolved.ConfigPath
	}
	paths := []string{path}
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
	}
	edits := []fileEdit{}
	for _, file := range paths {
		original, err := os.ReadFile(file)
		existed := err == nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !existed {
			if a.requireExisting {
				return fmt.Errorf("Copilot IntelliJ config does not exist: %s; open Copilot Chat and select Add MCP Tools first", file)
			}
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
		if parent, ok := document[a.parent]; ok {
			var valid bool
			servers, valid = parent.(map[string]any)
			if !valid {
				return fmt.Errorf("agent config %s must contain an object at %s", file, a.parent)
			}
		}
		current := servers[name]
		if current != nil {
			owned, ok := e.Owned[name]
			if !ok {
				return fmt.Errorf("refusing foreign MCP registration %q in %s", name, file)
			}
			if !sameJSON(current, a.value(owned)) {
				return fmt.Errorf("owned MCP registration %q changed in %s", name, file)
			}
		}
		if r == nil && current == nil {
			continue
		}
		v, err := hujson.Parse(original)
		if err != nil {
			return err
		}
		ops := []map[string]any{}
		if servers == nil {
			ops = append(ops, map[string]any{"op": "add", "path": "/" + pointer(a.parent), "value": map[string]any{}})
		}
		op := map[string]any{"op": "remove", "path": "/" + pointer(a.parent) + "/" + pointer(name)}
		if r != nil {
			op["op"] = "add"
			op["value"] = a.value(*r)
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
	for n, edit := range edits {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := state.WriteAtomic(edit.path, edit.updated, edit.mode); err != nil {
			var restoration []error
			for j := n - 1; j >= 0; j-- {
				prior := edits[j]
				if prior.existed {
					restoration = append(restoration, state.WriteAtomic(prior.path, prior.original, prior.mode))
				} else {
					restoration = append(restoration, os.Remove(prior.path))
				}
			}
			return errors.Join(append([]error{err}, restoration...)...)
		}
	}
	return nil
}
