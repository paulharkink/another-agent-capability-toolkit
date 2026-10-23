package state

import (
	"fmt"
	"sort"
	"strings"
)

// ResolveInstanceKey preserves existing identity evidence. Hash-only answer
// files cannot be reverse-mapped and are never guessed or rewritten.
func (s *Store) ResolveInstanceKey(packID, capabilityID, instanceName string) (Key, error) {
	if packID == "" || capabilityID == "" || instanceName == "" {
		return Key{}, fmt.Errorf("Capability Pack, capability and instance names are required")
	}
	profiles, err := s.Instances()
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
		if key.Source != packID || key.Package != capabilityID || key.Target != instanceName {
			continue
		}
		key.MCP = ""
		key.MCPDefinition = ""
		matched[key] = true
	}
	if len(matched) > 1 {
		details := []string{}
		for key := range matched {
			details = append(details, fmt.Sprintf("%s/%s (%s)", key.Environment, key.Target, key.ID()))
		}
		sort.Strings(details)
		return Key{}, fmt.Errorf("ambiguous existing state for instance %s/%s/%s: %s; repair the conflicting records explicitly", packID, capabilityID, instanceName, strings.Join(details, ", "))
	}
	for key := range matched {
		return key, nil
	}
	return Key{Source: packID, Package: capabilityID, Target: instanceName}, nil
}

// ResolveProfileKey is retained for source compatibility.
func (s *Store) ResolveProfileKey(packID, capabilityID, name string) (Key, error) {
	return s.ResolveInstanceKey(packID, capabilityID, name)
}
