package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

type targetChooserState struct {
	SourceID, PackageID string
	Targets             []viewmodel.EnvironmentTarget
}

func (m *Model) openTargetChooser(capability CapabilityRow) {
	chooser := &targetChooserState{SourceID: capability.Source, PackageID: capability.Package}
	if m.environmentSnapshot != nil {
		for _, target := range m.environmentSnapshot.Targets {
			if target.SourceID == capability.Source && target.PackageID == capability.Package && target.Error == "" {
				chooser.Targets = append(chooser.Targets, target)
			}
		}
	}
	m.home.TargetChooser = chooser
	m.home.Modal = &modalState{Kind: "target-chooser"}
}

func (m *Model) chooseTarget(index int) tea.Cmd {
	chooser := m.home.TargetChooser
	if chooser == nil {
		return nil
	}
	m.home.Modal = nil
	m.home.TargetChooser = nil
	if index >= 0 && index < len(chooser.Targets) {
		target := chooser.Targets[index]
		return m.beginSetup(target.SourceID, target.PackageID, target.Environment, target.Name)
	}
	return m.beginSetup(chooser.SourceID, chooser.PackageID, "", "")
}

func (m *Model) targetChooserItems() []homeMenuItem {
	chooser := m.home.TargetChooser
	if chooser == nil {
		return nil
	}
	items := make([]homeMenuItem, 0, len(chooser.Targets)+1)
	for i, target := range chooser.Targets {
		items = append(items, homeMenuItem{Label: fmt.Sprintf("%s / %s", target.Environment, target.Name), Action: fmt.Sprintf("target:%d", i)})
	}
	items = append(items, homeMenuItem{Label: "Without an environment preset", Action: "target:none"})
	return items
}

func (m *Model) setupInformationView() tea.View {
	width := max(40, m.width)
	height := max(12, m.height)
	inner := max(20, width-4)
	lines := []string{
		"Capability: " + m.pendingSetup.PackageName + " · Source: " + m.pendingSetup.Key.Source,
		"Environment: " + m.pendingSetup.Key.Environment + " · Target: " + m.pendingSetup.Key.Target,
		"Exact TOML: " + m.pendingSetup.TargetPath,
		"Source checkout: " + m.pendingSetup.SourceRoot,
	}
	if m.pendingSetup.TargetPath == "" {
		lines[2] = "Exact TOML: no environment preset is selected"
	}
	for _, destination := range m.pendingSetup.Destinations {
		if destination.ConfigPath != "" {
			lines = append(lines, fmt.Sprintf("Destination %s config: %s", destination.ID, destination.ConfigPath))
		}
		if destination.SkillsPath != "" {
			lines = append(lines, fmt.Sprintf("Destination %s skills: %s", destination.ID, destination.SkillsPath))
		}
	}
	lines = append(lines, "Resolved inputs and value origins")
	for _, input := range m.pendingSetup.Inputs {
		if !input.HasValue {
			continue
		}
		label := input.Definition.Label
		if label == "" {
			label = input.Definition.Name
		}
		lines = append(lines, splitDisplayLine(fmt.Sprintf("%s: %v · from %s · %s", label, input.Value, input.Provenance, input.ProvenancePath), inner)...)
	}
	if m.pendingSetup.TargetTOML != "" {
		lines = append(lines, "Raw TOML")
		for _, rawLine := range strings.Split(strings.TrimSuffix(m.pendingSetup.TargetTOML, "\n"), "\n") {
			lines = append(lines, splitDisplayLine(rawLine, inner)...)
		}
	} else {
		lines = append(lines, "Raw TOML: (empty)")
	}
	visible := max(1, height-8)
	start := min(max(0, m.setupInfoOffset), max(0, len(lines)-visible))
	m.setupInfoOffset = start
	box := []string{"╔" + fit(" Target information · F3 / Esc Back", width-2) + "╗"}
	for i := 0; i < visible; i++ {
		line := ""
		if start+i < len(lines) {
			line = lines[start+i]
		}
		box = append(box, "║"+fit(line, width-2)+"║")
	}
	position := fmt.Sprintf("Line %d/%d", start+1, max(1, len(lines)))
	if start > 0 {
		position = "↑ More above · " + position
	}
	if start+visible < len(lines) {
		position += " · ↓ More below"
	}
	box = append(box, "║"+fit(position, width-2)+"║", "║"+fit("↑↓ / PgUp / PgDn Scroll · Esc or F3 Return to configuration", width-2)+"║", "╚"+strings.Repeat("═", width-2)+"╝")
	return tea.NewView(navyCanvas(strings.Join(box, "\n")))
}

func splitDisplayLine(line string, width int) []string {
	runes := []rune(line)
	if len(runes) == 0 {
		return []string{""}
	}
	var out []string
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	return append(out, string(runes))
}
