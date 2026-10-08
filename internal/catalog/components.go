package catalog

import (
	"fmt"
	"path/filepath"
)

type ComponentSet struct {
	Name   string   `toml:"name" json:"name"`
	Skills []string `toml:"skills" json:"skills"`
	MCPs   []string `toml:"mcps" json:"mcps"`
}

type InstallationItem struct {
	ID, Label             string
	Skills, MCPs, Plugins []string
}

func (p Package) SkillDefinitions() []Skill {
	if p.Skill == nil {
		return append([]Skill(nil), p.Skills...)
	}
	s := *p.Skill
	if s.Source == "" {
		s.Source = p.Dir
	} else if !filepath.IsAbs(s.Source) {
		s.Source = filepath.Join(p.Dir, s.Source)
	}
	if len(s.Templates) == 0 {
		s.Templates = p.Templates
	}
	if s.Generator == nil {
		s.Generator = p.Generator
	}
	return []Skill{s}
}
func (p Package) HasSkill() bool              { return len(p.SkillDefinitions()) > 0 }
func (p Package) PluginDefinitions() []Plugin { return append([]Plugin(nil), p.Plugins...) }

// InstallationItems resolves linked skill/MCP sets without changing enable inputs.
func InstallationItems(p Package, sets []ComponentSet) ([]InstallationItem, error) {
	known := map[string]bool{}
	for _, s := range p.SkillDefinitions() {
		known["skill:"+s.Name] = true
	}
	for _, m := range p.MCPDefinitions() {
		known["mcp:"+m.Name] = true
	}
	for _, plugin := range p.Plugins {
		known["plugin:"+plugin.Name] = true
	}
	if len(sets) == 0 && p.Skill != nil && p.HasMCP() {
		set := ComponentSet{Name: p.Skill.Name, Skills: []string{p.Skill.Name}}
		for _, m := range p.MCPDefinitions() {
			set.MCPs = append(set.MCPs, m.Name)
		}
		sets = []ComponentSet{set}
	}
	items := []InstallationItem{}
	used := map[string]bool{}
	names := map[string]bool{}
	for _, set := range sets {
		if !identifier.MatchString(set.Name) || names[set.Name] {
			return nil, fmt.Errorf("invalid or duplicate component set %q", set.Name)
		}
		names[set.Name] = true
		if len(set.Skills)+len(set.MCPs) == 0 {
			return nil, fmt.Errorf("component set %s is empty", set.Name)
		}
		for kind, members := range map[string][]string{"skill": set.Skills, "mcp": set.MCPs} {
			for _, name := range members {
				id := kind + ":" + name
				if !known[id] {
					return nil, fmt.Errorf("component set %s references unknown %s", set.Name, id)
				}
				if used[id] {
					return nil, fmt.Errorf("component sets overlap on %s", id)
				}
				used[id] = true
			}
		}
		items = append(items, InstallationItem{ID: "set:" + set.Name, Label: set.Name, Skills: append([]string(nil), set.Skills...), MCPs: append([]string(nil), set.MCPs...)})
	}
	for _, s := range p.SkillDefinitions() {
		if !used["skill:"+s.Name] {
			items = append(items, InstallationItem{ID: "skill:" + s.Name, Label: s.Name, Skills: []string{s.Name}})
		}
	}
	for _, m := range p.MCPDefinitions() {
		if !used["mcp:"+m.Name] {
			items = append(items, InstallationItem{ID: "mcp:" + m.Name, Label: m.Name, MCPs: []string{m.Name}})
		}
	}
	for _, plugin := range p.Plugins {
		items = append(items, InstallationItem{ID: "plugin:" + plugin.Name, Label: plugin.Name, Plugins: []string{plugin.Name}})
	}
	return items, nil
}
