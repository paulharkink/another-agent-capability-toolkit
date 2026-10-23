package config

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/expressions"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
)

type InstanceRef struct {
	PackID       string `json:"pack_id"`
	CapabilityID string `json:"capability_id"`
	Name         string `json:"name"`
}

// ProfileRef is retained for callers migrating from the former terminology.
type ProfileRef = InstanceRef

type Instance struct {
	Ref              InstanceRef       `json:"ref"`
	Path             string            `json:"path"`
	Error            string            `json:"error,omitempty"`
	Raw              map[string]any    `json:"raw"`
	InputPolicy      map[string]string `json:"input_policy,omitempty"`
	ExpressionSource map[string]any    `json:"-"`
}

// Profile is retained for source compatibility; selected capability settings
// are instances in the current API.
type Profile = Instance

func profilePackage(pack Pack, capabilityID string) (catalog.Package, error) {
	if !safeID.MatchString(capabilityID) || capabilityID == "." || capabilityID == ".." {
		return catalog.Package{}, fmt.Errorf("invalid capability name %q", capabilityID)
	}
	for _, p := range pack.Catalog {
		if p.ID == capabilityID {
			return p, nil
		}
	}
	return catalog.Package{}, fmt.Errorf("Capability Pack %s has no capability %q", pack.ID, capabilityID)
}
func DiscoverInstances(pack Pack, capabilityID string) ([]Instance, error) {
	if _, err := profilePackage(pack, capabilityID); err != nil {
		return nil, err
	}
	if pack.instanceRoot() == "" {
		return []Instance{}, nil
	}
	dir := filepath.Join(pack.instanceRoot(), capabilityID)
	if err := contained(pack.instanceRoot(), dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Instance{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("instance directory %s: %w", dir, err)
	}
	instances := []Instance{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		instance, err := LoadInstance(pack, capabilityID, name)
		if err != nil {
			instance = Instance{Ref: InstanceRef{PackID: pack.ID, CapabilityID: capabilityID, Name: name}, Path: filepath.Join(dir, entry.Name()), Error: err.Error()}
		}
		instances = append(instances, instance)
	}
	return instances, nil
}
func DiscoverProfiles(pack Pack, capabilityID string) ([]Profile, error) {
	return DiscoverInstances(pack, capabilityID)
}

func ValidInstanceName(name string) bool {
	return safeID.MatchString(name) && name != "." && name != ".."
}

// ValidProfileName is retained for source compatibility.
func ValidProfileName(name string) bool { return ValidInstanceName(name) }

func LoadInstance(pack Pack, capabilityID, name string) (Instance, error) {
	pkg, err := profilePackage(pack, capabilityID)
	if err != nil {
		return Instance{}, err
	}
	if !ValidInstanceName(name) {
		return Profile{}, fmt.Errorf("invalid instance name %q", name)
	}
	if pack.instanceRoot() == "" {
		return Instance{}, fmt.Errorf("Capability Pack %s has no instance configuration directory", pack.ID)
	}
	path := filepath.Join(pack.instanceRoot(), capabilityID, name+".toml")
	if err := contained(pack.instanceRoot(), path); err != nil {
		return Instance{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Instance{}, fmt.Errorf("instance %s: %w", path, err)
	}
	raw := map[string]any{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Instance{}, fmt.Errorf("instance %s: %w", path, err)
	}
	expressionSource := cloneMap(raw)
	evaluated, err := expressions.EvaluateTreeDeferringRoot(raw, expressions.DocumentEnvironment(raw), path, "inputs")
	if err != nil {
		return Instance{}, err
	}
	raw, _ = evaluated.(map[string]any)
	policy, err := parseInputPolicy(raw, path)
	if err != nil {
		return Profile{}, err
	}
	declared := map[string]bool{}
	for _, in := range pkg.Inputs {
		declared[in.Name] = true
	}
	for field := range policy {
		if !declared[field] {
			return Instance{}, fmt.Errorf("%s: input policy refers to undeclared input %q", path, field)
		}
	}
	raw, err = ResolveInputPathsUnresolved(pkg.Inputs, raw, path)
	if err != nil {
		return Instance{}, fmt.Errorf("%s: %w", path, err)
	}
	return Instance{Ref: InstanceRef{PackID: pack.ID, CapabilityID: capabilityID, Name: name}, Path: path, Raw: raw, InputPolicy: policy, ExpressionSource: expressionSource}, nil
}

func LoadProfile(pack Pack, capabilityID, name string) (Profile, error) {
	return LoadInstance(pack, capabilityID, name)
}

// ResolveExpressions reevaluates profile values against the final resolved
// inputs while preserving the document's own top-level HCL context.
func ResolveExpressions(instance Instance, inputs map[string]any, defs []catalog.Input) (Instance, error) {
	if len(instance.ExpressionSource) == 0 {
		return instance, nil
	}
	environment := expressions.DocumentEnvironment(instance.ExpressionSource)
	environment["inputs"] = inputs
	value, err := expressions.EvaluateTree(instance.ExpressionSource, environment, instance.Path)
	if err != nil {
		return Instance{}, err
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return Instance{}, fmt.Errorf("%s: instance must be a TOML table", instance.Path)
	}
	policy, err := parseInputPolicy(raw, instance.Path)
	if err != nil {
		return Instance{}, err
	}
	raw, err = ResolveInputPaths(defs, raw, instance.Path)
	if err != nil {
		return Instance{}, fmt.Errorf("%s: %w", instance.Path, err)
	}
	instance.Raw, instance.InputPolicy = raw, policy
	return instance, nil
}

func parseInputPolicy(raw map[string]any, path string) (map[string]string, error) {
	policy := map[string]string{}
	aact, present := raw["aact"]
	if !present {
		return policy, nil
	}
	settings, ok := aact.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: aact must be a table", path)
	}
	declared, present := settings["input_policy"]
	if !present {
		return policy, nil
	}
	fields, ok := declared.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: aact.input_policy must be a table", path)
	}
	for name, value := range fields {
		mode, ok := value.(string)
		if !ok || (mode != "fixed" && mode != "default") {
			return nil, fmt.Errorf("%s: aact.input_policy.%s must be fixed or default", path, name)
		}
		policy[name] = mode
	}
	return policy, nil
}
