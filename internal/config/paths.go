package config

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"reflect"
	"strings"
)

func ResolvePath(path, declaringFile string) (string, error) { return resolvePath(path, declaringFile) }

// ResolveInputPaths makes declared path answers absolute against their own source file.
// Call this for each layer before merging answers so a later layer keeps its origin.
func ResolveInputPaths(defs []catalog.Input, values map[string]any, declaringFile string) (map[string]any, error) {
	out := cloneMap(values)
	for _, def := range defs {
		if def.Type != "file" && def.Type != "directory" {
			continue
		}
		keys := []string{def.Name, "inputs." + def.Name}
		if def.ConfigKey != "" {
			keys = append(keys, def.ConfigKey)
		}
		for _, key := range keys {
			container, name, ok := pathField(out, key)
			if !ok {
				continue
			}
			value := container[name]
			if value == nil {
				continue
			}
			resolved, e := resolvePathValue(value, def.Multiple, declaringFile)
			if e != nil {
				return nil, fmt.Errorf("input %s: %w", def.Name, e)
			}
			container[name] = resolved
		}
	}
	return out, nil
}
func cloneMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		if nested, ok := value.(map[string]any); ok {
			value = cloneMap(nested)
		}
		out[key] = value
	}
	return out
}
func pathField(values map[string]any, key string) (map[string]any, string, bool) {
	if _, ok := values[key]; ok {
		return values, key, true
	}
	parts := strings.Split(key, ".")
	m := values
	for _, part := range parts[:len(parts)-1] {
		nested, ok := m[part].(map[string]any)
		if !ok {
			return nil, "", false
		}
		m = nested
	}
	name := parts[len(parts)-1]
	_, ok := m[name]
	return m, name, ok
}
func resolvePathValue(value any, multiple bool, file string) (any, error) {
	if !multiple {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected path string")
		}
		if text == "" {
			return text, nil
		}
		return ResolvePath(text, file)
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Array && rv.Kind() != reflect.Slice {
		return nil, fmt.Errorf("expected path array")
	}
	out := make([]string, rv.Len())
	for i := range out {
		text, ok := rv.Index(i).Interface().(string)
		if !ok {
			return nil, fmt.Errorf("expected path string at item %d", i+1)
		}
		path, e := ResolvePath(text, file)
		if e != nil {
			return nil, e
		}
		out[i] = path
	}
	return out, nil
}
