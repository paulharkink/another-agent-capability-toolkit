package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// GroupProfile is a Capability Pack-level profile that groups existing
// Capability Instances. It is distinct from Instance/Profile configuration.
type GroupProfile struct {
	PackID      string        `json:"pack_id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Members     []InstanceRef `json:"members"`
	Error       string        `json:"error,omitempty"`
}

type groupProfileDeclaration struct {
	Name        string                          `toml:"name"`
	Description string                          `toml:"description"`
	Members     []groupProfileMemberDeclaration `toml:"members"`
}

type groupProfileMemberDeclaration struct {
	Capability string `toml:"capability"`
	Instance   string `toml:"instance"`
}

func resolveGroupProfiles(source Source, declarations []groupProfileDeclaration) []GroupProfile {
	profiles := make([]GroupProfile, 0, len(declarations))
	counts := make(map[string]int)
	for _, declaration := range declarations {
		counts[strings.ToLower(strings.TrimSpace(declaration.Name))]++
	}
	for _, declaration := range declarations {
		profile := GroupProfile{PackID: source.ID, Name: strings.TrimSpace(declaration.Name), Description: strings.TrimSpace(declaration.Description), Members: []InstanceRef{}}
		if err := validateGroupProfileName(profile.Name); err != nil {
			profile.Error = err.Error()
		} else if counts[strings.ToLower(profile.Name)] > 1 {
			profile.Error = fmt.Sprintf("duplicate Pack Profile name %q", profile.Name)
		}
		seen := map[InstanceRef]bool{}
		for _, declarationMember := range declaration.Members {
			member := InstanceRef{PackID: source.ID, CapabilityID: strings.TrimSpace(declarationMember.Capability), Name: strings.TrimSpace(declarationMember.Instance)}
			if !ValidInstanceName(member.CapabilityID) || !ValidInstanceName(member.Name) {
				if profile.Error == "" {
					profile.Error = fmt.Sprintf("invalid Capability Instance reference %q/%q", member.CapabilityID, member.Name)
				}
				continue
			}
			if seen[member] {
				if profile.Error == "" {
					profile.Error = fmt.Sprintf("duplicate Capability Instance %s/%s", member.CapabilityID, member.Name)
				}
				continue
			}
			seen[member] = true
			if _, err := profilePackage(source.CapabilityPack(), member.CapabilityID); err != nil {
				// Keep stale or removed capability references visible so the
				// Profile UI can report them as skipped without blocking siblings.
				profile.Members = append(profile.Members, member)
				continue
			}
			if _, err := LoadInstance(source.CapabilityPack(), member.CapabilityID, member.Name); err != nil {
				profile.Members = append(profile.Members, member)
				continue
			}
			profile.Members = append(profile.Members, member)
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

func validateGroupProfileName(name string) error {
	if name == "" || strings.TrimSpace(name) != name || len(name) > 80 {
		return fmt.Errorf("Pack Profile name must be non-empty, trimmed, and at most 80 characters")
	}
	if strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("Pack Profile name cannot contain a path separator")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return fmt.Errorf("Pack Profile name cannot contain control characters")
		}
	}
	if filepath.Clean(name) == "." || name == ".." {
		return fmt.Errorf("invalid Pack Profile name %q", name)
	}
	return nil
}
