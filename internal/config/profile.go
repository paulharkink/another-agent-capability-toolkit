package config

import (
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"strings"
)

type ProfileRef struct {
	PackID       string `json:"pack_id"`
	CapabilityID string `json:"capability_id"`
	Name         string `json:"name"`
}
type Profile struct {
	Ref         ProfileRef        `json:"ref"`
	Path        string            `json:"path"`
	Error       string            `json:"error,omitempty"`
	Raw         map[string]any    `json:"raw"`
	InputPolicy map[string]string `json:"input_policy,omitempty"`
}

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
func DiscoverProfiles(pack Pack, capabilityID string) ([]Profile, error) {
	if _, err := profilePackage(pack, capabilityID); err != nil {
		return nil, err
	}
	if pack.ProfileRoot == "" {
		return []Profile{}, nil
	}
	dir := filepath.Join(pack.ProfileRoot, capabilityID)
	if err := contained(pack.ProfileRoot, dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile directory %s: %w", dir, err)
	}
	profiles := []Profile{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		profile, err := LoadProfile(pack, capabilityID, name)
		if err != nil {
			profile = Profile{Ref: ProfileRef{PackID: pack.ID, CapabilityID: capabilityID, Name: name}, Path: filepath.Join(dir, entry.Name()), Error: err.Error()}
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}
func LoadProfile(pack Pack, capabilityID, name string) (Profile, error) {
	pkg, err := profilePackage(pack, capabilityID)
	if err != nil {
		return Profile{}, err
	}
	if !safeID.MatchString(name) || name == "." || name == ".." {
		return Profile{}, fmt.Errorf("invalid profile name %q", name)
	}
	if pack.ProfileRoot == "" {
		return Profile{}, fmt.Errorf("Capability Pack %s has no profile configuration directory", pack.ID)
	}
	path := filepath.Join(pack.ProfileRoot, capabilityID, name+".toml")
	if err := contained(pack.ProfileRoot, path); err != nil {
		return Profile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("profile %s: %w", path, err)
	}
	raw := map[string]any{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Profile{}, fmt.Errorf("profile %s: %w", path, err)
	}
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
			return Profile{}, fmt.Errorf("%s: input policy refers to undeclared input %q", path, field)
		}
	}
	raw, err = ResolveInputPaths(pkg.Inputs, raw, path)
	if err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	return Profile{Ref: ProfileRef{PackID: pack.ID, CapabilityID: capabilityID, Name: name}, Path: path, Raw: raw, InputPolicy: policy}, nil
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
