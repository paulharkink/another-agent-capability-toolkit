package app

import (
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func withChoices(inputs []catalog.Input, choices map[string][]catalog.Choice) []catalog.Input {
	defs := append([]catalog.Input{}, inputs...)
	for n, d := range defs {
		if options, ok := choices[d.Name]; ok {
			defs[n].Options = append([]catalog.Choice{}, options...)
			if d.Type == "string" {
				defs[n].Type = "choice"
			}
		}
	}
	return defs
}

type registrationFile = agents.ConfigSnapshot

func snapshotRegistration(env agents.Environment) ([]registrationFile, error) {
	return agents.SnapshotRegistration(env)
}
func restoreRegistration(files []registrationFile) error { return agents.RestoreRegistration(files) }
func (s *Service) recordRegistration(r state.Installation) error {
	if s.Options.RecordInstallation != nil {
		return s.Options.RecordInstallation(r)
	}
	return s.Store.Record(r)
}
func (s *Service) removeRegistration(r state.Installation) error {
	if s.Options.RemoveInstallation != nil {
		return s.Options.RemoveInstallation(r)
	}
	return s.Store.Remove(r)
}
