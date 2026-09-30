package install

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"strings"
)

type Skills struct {
	Store *state.Store
	Link  func(string, string) error
}

func NewSkills(s *state.Store) *Skills { return &Skills{Store: s, Link: os.Symlink} }
func (s *Skills) Install(ctx context.Context, p catalog.Package, e agents.Environment, k state.Key, generatedDir string) error {
	if s.Store == nil {
		return errors.New("state store required")
	}
	if p.Skill == nil {
		return errors.New("package has no skill")
	}
	name := p.Skill.Name
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:") {
		return errors.New("unsafe skill name")
	}
	if e.SkillsDir == "" || e.ID == "" {
		return errors.New("explicit agent skills directory and ID required")
	}
	source := p.Dir
	if generatedDir != "" {
		source = generatedDir
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(source, "SKILL.md"))
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("skill source requires SKILL.md")
	}
	destination, err := filepath.Abs(filepath.Join(e.SkillsDir, name))
	if err != nil {
		return err
	}
	if source == destination {
		return errors.New("skill source cannot be its destination")
	}
	digest, err := treeDigest(ctx, source)
	if err != nil {
		return err
	}
	return func() error {
		rows, err := s.Store.Installations()
		if err != nil {
			return err
		}
		var shared []state.Installation
		var previous *state.Installation
		for _, row := range rows {
			if row.Component == "skill" && row.Destination == destination {
				shared = append(shared, row)
				if row.Key == k && row.AgentID == e.ID {
					copy := row
					previous = &copy
				}
			}
		}
		_, statErr := os.Lstat(destination)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if exists && len(shared) == 0 {
			return fmt.Errorf("refusing foreign skill at %s", destination)
		}
		for _, row := range shared {
			if row.Key.Source != k.Source || row.Key.Package != k.Package || row.AgentID != e.ID {
				return fmt.Errorf("skill destination owned by another source/package/agent: %s", destination)
			}
			if err := verifyOwned(ctx, row); err != nil {
				return err
			}
			if row.Key != k && row.Digest != digest {
				return fmt.Errorf("different rendered content already shared at %s", destination)
			}
		}
		if len(shared) > 0 && exists && shared[0].Digest == digest && (previous == nil || len(shared) > 1 || previous.SourcePath == source) {
			// The original generated source remains alive while any target references it.
			row := shared[0]
			row.Key = k
			row.AgentID = e.ID
			return s.Store.Record(row)
		}
		if len(shared) > 1 || (len(shared) == 1 && previous == nil) {
			return fmt.Errorf("cannot update a shared skill with different content at %s", destination)
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		stage, err := os.MkdirTemp(filepath.Dir(destination), ".aact-install-*")
		if err != nil {
			return err
		}
		os.Remove(stage)
		defer os.RemoveAll(stage)
		link := s.Link
		if link == nil {
			link = os.Symlink
		}
		mode := "symlink"
		if err = link(source, stage); err != nil {
			mode = "copy"
			if err = render.CopyTree(ctx, source, stage); err != nil {
				return err
			}
		}
		backup := ""
		if exists {
			backup, err = os.MkdirTemp(filepath.Dir(destination), ".aact-install-backup-*")
			if err != nil {
				return err
			}
			os.Remove(backup)
			if err = os.Rename(destination, backup); err != nil {
				return err
			}
		}
		restore := func() error {
			if err := os.RemoveAll(destination); err != nil {
				return err
			}
			if backup != "" {
				return os.Rename(backup, destination)
			}
			return nil
		}
		if err = os.Rename(stage, destination); err != nil {
			if backup != "" {
				if rollback := os.Rename(backup, destination); rollback != nil {
					return fmt.Errorf("install failed: %v; rollback: %w; backup %s", err, rollback, backup)
				}
			}
			return err
		}
		row := state.Installation{Key: k, AgentID: e.ID, Component: "skill", Destination: destination, SourcePath: source, Mode: mode, Digest: digest}
		if err = s.Store.Record(row); err != nil {
			if rollback := restore(); rollback != nil {
				return fmt.Errorf("ledger write: %v; rollback: %w", err, rollback)
			}
			return err
		}
		if backup != "" {
			os.RemoveAll(backup)
		}
		if previous != nil && previous.SourcePath != source {
			s.removeGeneratedIfUnused(previous.SourcePath)
		}
		return nil
	}()
}
func (s *Skills) Uninstall(ctx context.Context, k state.Key, e agents.Environment) error {
	if s.Store == nil {
		return errors.New("state store required")
	}
	return func() error {
		rows, err := s.Store.Installations()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Component != "skill" || row.Key != k || row.AgentID != e.ID {
				continue
			}
			if err = verifyOwned(ctx, row); err != nil {
				return err
			}
			shared := false
			for _, other := range rows {
				if other.Component == "skill" && other.Destination == row.Destination && (other.Key != row.Key || other.AgentID != row.AgentID) {
					shared = true
				}
			}
			if shared {
				if err = s.Store.Remove(row); err != nil {
					return err
				}
				continue
			}
			// Rename first to permit exact restoration if a ledger write fails.
			backup := ""
			if _, err = os.Lstat(row.Destination); err == nil {
				backup, err = os.MkdirTemp(filepath.Dir(row.Destination), ".aact-remove-*")
				if err != nil {
					return err
				}
				os.Remove(backup)
				if err = os.Rename(row.Destination, backup); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
			if err = s.Store.Remove(row); err != nil {
				if backup != "" {
					if restore := os.Rename(backup, row.Destination); restore != nil {
						return fmt.Errorf("ledger removal: %v; restore: %w; backup %s", err, restore, backup)
					}
				}
				return err
			}
			if backup != "" {
				if err = os.RemoveAll(backup); err != nil {
					return err
				}
			}
			s.removeGeneratedIfUnused(row.SourcePath)
		}
		return nil
	}()
}
func (s *Skills) removeGeneratedIfUnused(path string) {
	root := filepath.Join(s.Store.Root(), "generated")
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	rows, err := s.Store.Installations()
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.SourcePath == path {
			return
		}
	}
	os.RemoveAll(path)
}
