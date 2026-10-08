package forms

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"reflect"
	"strings"
)

// Resolve maps declared fields and applies layers from lowest to highest precedence.
func Resolve(defs []catalog.Input, layers ...map[string]any) (map[string]any, error) {
	result, err := ResolvePartial(defs, layers...)
	if err != nil {
		return nil, err
	}
	if err = Validate(defs, result); err != nil {
		return nil, err
	}
	return result, nil
}

// ResolvePartial maps and normalizes an editable prefill without requiring completeness.
func ResolvePartial(defs []catalog.Input, layers ...map[string]any) (map[string]any, error) {
	result := map[string]any{}
	for _, def := range defs {
		if def.Default != nil {
			value, e := normalize(def, def.Default)
			if e != nil {
				return nil, fmt.Errorf("default %s: %w", def.Name, e)
			}
			result[def.Name] = value
		}
	}
	for index, layer := range layers {
		for _, def := range defs {
			var selected any
			found := false
			candidates := []struct {
				value   any
				present bool
			}{}
			direct, ok := layer[def.Name]
			candidates = append(candidates, struct {
				value   any
				present bool
			}{direct, ok})
			if inputs, ok := layer["inputs"].(map[string]any); ok {
				v, p := inputs[def.Name]
				candidates = append(candidates, struct {
					value   any
					present bool
				}{v, p})
			}
			if def.ConfigKey != "" {
				v, p := lookup(layer, def.ConfigKey)
				candidates = append(candidates, struct {
					value   any
					present bool
				}{v, p})
			}
			for _, candidate := range candidates {
				if !candidate.present {
					continue
				}
				v, e := normalize(def, candidate.value)
				if e != nil {
					return nil, fmt.Errorf("input %s in layer %d: %w", def.Name, index+1, e)
				}
				if found && !reflect.DeepEqual(selected, v) {
					return nil, fmt.Errorf("input %s: conflicting inputs and config_key values in layer %d", def.Name, index+1)
				}
				selected = v
				found = true
			}
			if found {
				result[def.Name] = selected
			}
		}
	}
	if err := ValidateProvided(defs, result); err != nil {
		return result, err
	}
	return result, nil
}
func lookup(values map[string]any, key string) (any, bool) {
	if v, ok := values[key]; ok {
		return v, true
	}
	parts := strings.Split(key, ".")
	var current any = values
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[part]
		if !ok {
			return nil, false
		}
		current = v
	}
	return current, true
}
func normalize(def catalog.Input, value any) (any, error) {
	def = scalarDefinition(def)
	if value == nil {
		return nil, nil
	}
	if !def.Multiple {
		return normalizeScalar(def.Type, value)
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("expected an array for %s", def.Type)
	}
	scalar := def
	scalar.Multiple = false
	switch def.Type {
	case "string", "secret", "choice", "file", "directory":
		out := make([]string, rv.Len())
		for i := range out {
			v, e := normalizeScalar(scalar.Type, rv.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out[i] = v.(string)
		}
		return out, nil
	case "integer":
		out := make([]int64, rv.Len())
		for i := range out {
			v, e := normalizeScalar(scalar.Type, rv.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out[i] = v.(int64)
		}
		return out, nil
	case "boolean":
		out := make([]bool, rv.Len())
		for i := range out {
			v, e := normalizeScalar(scalar.Type, rv.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out[i] = v.(bool)
		}
		return out, nil
	case "number", "float":
		out := make([]float64, rv.Len())
		for i := range out {
			v, e := normalizeScalar(scalar.Type, rv.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out[i] = v.(float64)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported input type %q", def.Type)
	}
}

func scalarDefinition(def catalog.Input) catalog.Input {
	if def.Type == "multichoice" || def.Type == "multiple-choice" {
		def.Type = "choice"
		def.Multiple = true
	}
	return def
}
