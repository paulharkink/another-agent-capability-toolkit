package app

import (
	"errors"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
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

type registrationFile struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

func snapshotRegistration(env agents.Environment) ([]registrationFile, error) {
	paths := []string{env.ConfigPath}
	if env.Kind == "opencode" {
		ext := filepath.Ext(env.ConfigPath)
		if ext == ".json" {
			paths = append(paths, strings.TrimSuffix(env.ConfigPath, ext)+".jsonc")
		} else if ext == ".jsonc" {
			paths = append(paths, strings.TrimSuffix(env.ConfigPath, ext)+".json")
		}
	}
	out := []registrationFile{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			out = append(out, registrationFile{path: path})
			continue
		}
		if e != nil {
			return nil, e
		}
		info, e := os.Stat(path)
		if e != nil {
			return nil, e
		}
		out = append(out, registrationFile{path: path, data: b, mode: info.Mode().Perm(), existed: true})
	}
	return out, nil
}
func restoreRegistration(files []registrationFile) error {
	errs := []error{}
	for _, f := range files {
		var e error
		if f.existed {
			e = state.WriteAtomic(f.path, f.data, f.mode)
		} else {
			e = os.Remove(f.path)
			if os.IsNotExist(e) {
				e = nil
			}
		}
		errs = append(errs, e)
	}
	return errors.Join(errs...)
}
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
