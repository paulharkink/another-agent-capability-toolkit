package config

import (
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
)

type Target struct {
	Environment string            `json:"environment"`
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Raw         map[string]any    `json:"raw"`
	InputPolicy map[string]string `json:"input_policy,omitempty"`
}

func LoadTarget(s Source, packageID, environment, target string) (Target, error) {
	if s.EnvironmentRoot == "" {
		return Target{}, fmt.Errorf("environment root is not configured")
	}
	for _, name := range []string{packageID, environment, target} {
		if !safeID.MatchString(name) || name == "." || name == ".." {
			return Target{}, fmt.Errorf("invalid target path component %q", name)
		}
	}
	path := filepath.Join(s.EnvironmentRoot, environment, packageID, target+".toml")
	if err := contained(s.EnvironmentRoot, path); err != nil {
		return Target{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Target{}, fmt.Errorf("target %s/%s/%s: %w", environment, packageID, target, err)
	}
	raw := map[string]any{}
	if err = toml.Unmarshal(data, &raw); err != nil {
		return Target{}, fmt.Errorf("%s: %w", path, err)
	}
	policy := map[string]string{}
	if aact, ok := raw["aact"]; ok {
		settings, ok := aact.(map[string]any)
		if !ok {
			return Target{}, fmt.Errorf("%s: aact must be a table", path)
		}
		if declared, ok := settings["input_policy"]; ok {
			fields, ok := declared.(map[string]any)
			if !ok {
				return Target{}, fmt.Errorf("%s: aact.input_policy must be a table", path)
			}
			for name, value := range fields {
				mode, ok := value.(string)
				if !ok || (mode != "fixed" && mode != "default") {
					return Target{}, fmt.Errorf("%s: aact.input_policy.%s must be fixed or default", path, name)
				}
				policy[name] = mode
			}
		}
	}
	return Target{Environment: environment, Name: target, Path: path, Raw: raw, InputPolicy: policy}, nil
}
func contained(root, path string) error {
	absRoot, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	absPath, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	if e = relativeContained(absRoot, absPath); e != nil {
		return e
	}
	realRoot, e := filepath.EvalSymlinks(absRoot)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	realPath, e := filepath.EvalSymlinks(absPath)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	return relativeContained(realRoot, realPath)
}
func relativeContained(root, path string) error {
	rel, e := filepath.Rel(root, path)
	if e != nil {
		return e
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %s escapes root %s", path, root)
	}
	return nil
}
