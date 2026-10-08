package state

import (
	"fmt"
	"sort"
	"strings"
)

// ResolveProfileKey preserves existing identity evidence. Hash-only answer
// files cannot be reverse-mapped and are never guessed or rewritten.
func (s *Store) ResolveProfileKey(packID, capabilityID, profileName string) (Key, error) {
	if packID == "" || capabilityID == "" || profileName == "" {
		return Key{}, fmt.Errorf("Capability Pack, capability and profile names are required")
	}
	profiles, err := s.Profiles()
	if err != nil {
		return Key{}, err
	}
	installations, err := s.Installations()
	if err != nil {
		return Key{}, err
	}
	keys := []Key{}
	for _, record := range profiles {
		keys = append(keys, record.Key)
	}
	for _, record := range installations {
		keys = append(keys, record.Key)
	}
	matched := map[Key]bool{}
	for _, key := range keys {
		if key.Source != packID || key.Package != capabilityID || key.Target != profileName {
			continue
		}
		key.MCP = ""
		key.Profile = ""
		matched[key] = true
	}
	if len(matched) > 1 {
		details := []string{}
		for key := range matched {
			details = append(details, fmt.Sprintf("%s/%s (%s)", key.Environment, key.Target, key.ID()))
		}
		sort.Strings(details)
		return Key{}, fmt.Errorf("ambiguous existing state for profile %s/%s/%s: %s; repair the conflicting records explicitly", packID, capabilityID, profileName, strings.Join(details, ", "))
	}
	for key := range matched {
		return key, nil
	}
	return Key{Source: packID, Package: capabilityID, Target: profileName}, nil
}
