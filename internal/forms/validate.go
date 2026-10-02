package forms

import (
	"encoding/json"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"math"
	"reflect"
	"strconv"
	"strings"
)

func Validate(defs []catalog.Input, values map[string]any) error {
	activeGroup := map[string]string{}
	for _, def := range defs {
		def = scalarDefinition(def)
		value, present := values[def.Name]
		if !present || value == nil {
			if def.Required {
				return required(def.Name)
			}
			continue
		}
		if def.Multiple {
			rv := reflect.ValueOf(value)
			if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
				return fmt.Errorf("input %s: expected an array", def.Name)
			}
			if def.Required && rv.Len() == 0 {
				return required(def.Name)
			}
			if def.MinItems != nil && rv.Len() < *def.MinItems {
				return fmt.Errorf("input %s: requires at least %d items", def.Name, *def.MinItems)
			}
			if def.MaxItems != nil && rv.Len() > *def.MaxItems {
				return fmt.Errorf("input %s: allows at most %d items", def.Name, *def.MaxItems)
			}
			for i := 0; i < rv.Len(); i++ {
				if e := validateScalar(def, rv.Index(i).Interface()); e != nil {
					return fmt.Errorf("input %s item %d: %w", def.Name, i+1, e)
				}
			}
		} else {
			if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
				if def.Required {
					return required(def.Name)
				}
				if def.Type == "string" || def.Type == "secret" || def.Type == "file" || def.Type == "directory" || def.Type == "choice" {
					continue
				}
			}
			if e := validateScalar(def, value); e != nil {
				return fmt.Errorf("input %s: %w", def.Name, e)
			}
		}
		if def.ExclusiveGroup != "" && filled(value) {
			if previous := activeGroup[def.ExclusiveGroup]; previous != "" {
				return fmt.Errorf("inputs %s and %s are mutually exclusive", previous, def.Name)
			}
			activeGroup[def.ExclusiveGroup] = def.Name
		}
	}
	return nil
}
func filled(value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case bool:
		return v
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array || rv.Kind() == reflect.Map {
		return rv.Len() > 0
	}
	return true
}
func required(name string) error {
	return fmt.Errorf("input %s is required; supply an answer or use --interactive", name)
}
func validateScalar(def catalog.Input, value any) error {
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" && (def.Type == "file" || def.Type == "directory" || def.Required) {
		return fmt.Errorf("value is required")
	}

	v, e := normalizeScalar(def.Type, value)
	if e != nil {
		return e
	}
	if def.Type == "integer" || def.Type == "number" || def.Type == "float" {
		var n float64
		if def.Type == "integer" {
			n = float64(v.(int64))
		} else {
			n = v.(float64)
		}
		if def.Min != nil && n < *def.Min {
			return fmt.Errorf("must be at least %g", *def.Min)
		}
		if def.Max != nil && n > *def.Max {
			return fmt.Errorf("must be at most %g", *def.Max)
		}
	}
	if def.Type == "choice" && len(def.Options) > 0 {
		for _, option := range def.Options {
			if option.Value == v.(string) {
				return nil
			}
		}
		return fmt.Errorf("value %q is not an available choice", v)
	}
	return nil
}
func normalizeScalar(kind string, value any) (any, error) {
	switch kind {
	case "string", "secret", "choice", "file", "directory":
		if s, ok := value.(string); ok {
			return s, nil
		}
		return nil, fmt.Errorf("expected %s string, got %T", kind, value)
	case "boolean":
		if b, ok := value.(bool); ok {
			return b, nil
		}
		if s, ok := value.(string); ok {
			if s == "true" {
				return true, nil
			}
			if s == "false" {
				return false, nil
			}
		}
		return nil, fmt.Errorf("expected boolean, got %T", value)
	case "integer":
		if s, ok := value.(string); ok {
			n, e := strconv.ParseInt(s, 10, 64)
			if e == nil {
				return n, nil
			}
		}
		if n, ok := value.(json.Number); ok {
			i, e := n.Int64()
			if e == nil {
				return i, nil
			}
		}
		if value != nil {
			rv := reflect.ValueOf(value)
			switch rv.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				return rv.Int(), nil
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				if rv.Uint() <= math.MaxInt64 {
					return int64(rv.Uint()), nil
				}
			case reflect.Float32, reflect.Float64:
				f := rv.Float()
				if !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) == f && f >= -math.Exp2(63) && f < math.Exp2(63) {
					return int64(f), nil
				}
			}
		}
		return nil, fmt.Errorf("expected integer, got %T", value)
	case "number", "float":
		var n float64
		ok := false
		if s, is := value.(string); is {
			var e error
			n, e = strconv.ParseFloat(s, 64)
			ok = e == nil
		} else if number, is := value.(json.Number); is {
			var e error
			n, e = number.Float64()
			ok = e == nil
		} else if value != nil {
			rv := reflect.ValueOf(value)
			switch rv.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				n = float64(rv.Int())
				ok = true
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				n = float64(rv.Uint())
				ok = true
			case reflect.Float32, reflect.Float64:
				n = rv.Float()
				ok = true
			}
		}
		if ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, nil
		}
		return nil, fmt.Errorf("expected finite number, got %T", value)
	default:
		return nil, fmt.Errorf("unsupported input type %q", kind)
	}
}
