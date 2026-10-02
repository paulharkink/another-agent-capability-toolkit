package app

import (
	"fmt"
	"reflect"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
)

// fixedTargetInputs reads policy only from the selected target TOML. The
// supplied layer has already had file and directory values resolved relative
// to that file.
func fixedTargetInputs(defs []catalog.Input, target config.Target, layer map[string]any) (map[string]any, error) {
	fixed := map[string]any{}
	if len(target.InputPolicy) == 0 {
		return fixed, nil
	}
	declared := map[string]bool{}
	for _, def := range defs {
		declared[def.Name] = true
	}
	bare := append([]catalog.Input(nil), defs...)
	for i := range bare {
		bare[i].Default = nil
	}
	values, err := forms.ResolvePartial(bare, layer)
	if err != nil {
		return nil, err
	}
	for name, policy := range target.InputPolicy {
		if !declared[name] {
			return nil, fmt.Errorf("%s: input policy refers to undeclared input %q", target.Path, name)
		}
		if policy != "fixed" {
			continue
		}
		value, ok := values[name]
		if !ok || value == nil {
			return nil, fmt.Errorf("%s: fixed input %q requires a value in this target", target.Path, name)
		}
		kind := reflect.ValueOf(value).Kind()
		if (kind == reflect.String || kind == reflect.Slice) && reflect.ValueOf(value).Len() == 0 {
			return nil, fmt.Errorf("%s: fixed input %q requires a value in this target", target.Path, name)
		}
		fixed[name] = value
	}
	return fixed, nil
}

func withoutFixed(defs []catalog.Input, values map[string]any, fixed map[string]any) ([]catalog.Input, map[string]any) {
	visible := make([]catalog.Input, 0, len(defs))
	editable := make(map[string]any, len(values))
	for _, def := range defs {
		if _, ok := fixed[def.Name]; !ok {
			visible = append(visible, def)
		}
	}
	for name, value := range values {
		if _, ok := fixed[name]; !ok {
			editable[name] = value
		}
	}
	return visible, editable
}

func withFixed(values, fixed map[string]any) map[string]any {
	for name, value := range fixed {
		values[name] = value
	}
	return values
}
