package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
)

func componentSelection(p catalog.Package, values map[string]any, ids []string, skillsOnly bool) ([]catalog.InstallationItem, []string, error) {
	items, err := catalog.InstallationItems(p, p.Sets)
	if err != nil {
		return nil, nil, err
	}
	enabled := map[string]bool{}
	for _, m := range packageMCPProfiles(p, values) {
		enabled[m.Name] = true
	}
	chosen := map[string]bool{}
	if ids != nil {
		for _, id := range ids {
			chosen[id] = true
		}
	} else {
		for _, item := range items {
			yes := len(item.Skills) > 0
			for _, m := range item.MCPs {
				yes = yes || enabled[m]
			}
			for _, name := range item.Plugins {
				for _, plugin := range p.Plugins {
					if plugin.Name == name && plugin.EnabledInput != "" && values[plugin.EnabledInput] == true {
						yes = true
					}
				}
			}
			if yes {
				chosen[item.ID] = true
			}
		}
	}
	result := []catalog.InstallationItem{}
	normalized := []string{}
	for _, item := range items {
		if !chosen[item.ID] {
			continue
		}
		delete(chosen, item.ID)
		if skillsOnly && len(item.Skills) == 0 {
			continue
		}
		normalized = append(normalized, item.ID)
		if skillsOnly {
			item.MCPs = nil
			item.Plugins = nil
		} else {
			item.MCPs = slices.DeleteFunc(append([]string(nil), item.MCPs...), func(name string) bool { return !enabled[name] })
		}
		result = append(result, item)
	}
	for id := range chosen {
		return nil, nil, fmt.Errorf("unknown installation item %q; linked components must be selected as their set", id)
	}
	if skillsOnly && len(result) == 0 {
		return nil, nil, fmt.Errorf("no skill components are available to install")
	}
	return result, normalized, nil
}

// normalizePersistedItemIDs keeps saved selections usable when a catalog starts
// exposing a selected component only through a linked installation set.
func normalizePersistedItemIDs(p catalog.Package, ids []string) []string {
	if len(ids) == 0 {
		return ids
	}
	items, err := catalog.InstallationItems(p, p.Sets)
	if err != nil {
		return ids
	}
	selectable := make(map[string]bool, len(items))
	linkedByMember := map[string][]string{}
	for _, item := range items {
		selectable[item.ID] = true
		if !strings.HasPrefix(item.ID, "set:") {
			continue
		}
		for _, name := range item.Skills {
			linkedByMember["skill:"+name] = append(linkedByMember["skill:"+name], item.ID)
		}
		for _, name := range item.MCPs {
			linkedByMember["mcp:"+name] = append(linkedByMember["mcp:"+name], item.ID)
		}
	}

	result := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		current := id
		if !selectable[id] {
			if candidates := linkedByMember[id]; len(candidates) == 1 {
				current = candidates[0]
			}
		}
		if !seen[current] {
			result = append(result, current)
			seen[current] = true
		}
	}
	return result
}
