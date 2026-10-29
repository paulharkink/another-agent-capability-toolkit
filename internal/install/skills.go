package install

import (
	"context"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Skills struct {
	Store             *state.Store
	Link              func(string, string) error
	AllowSourceUpdate bool
	readLXLink        func(string) (string, bool, error)
	recordEffect      func(state.Installation) error
	removeDestination string
}

func NewSkills(s *state.Store) *Skills {
	return &Skills{Store: s, Link: os.Symlink, readLXLink: readLXReparseLink}
}
func (s *Skills) Install(ctx context.Context, p catalog.Package, e SkillDestination, k state.Key, generatedDir string) error {
	return s.install(ctx, p, e, k, generatedDir, nil)
}

// ForeignSkillProof is supplied only after the caller has verified the exact
// external Docker instance and its matching Windows MCP binding.
type ForeignSkillProof struct {
	ParentKeyID      string
	RegistrationName string
	URL              string
}

func (s *Skills) install(ctx context.Context, p catalog.Package, e SkillDestination, k state.Key, generatedDir string, proof *ForeignSkillProof) error {
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
		if previous != nil && generatedDir == "" && previous.SourcePath != source && !s.AllowSourceUpdate {
			return fmt.Errorf("skill source moved from %s to %s; use --update-source to update the owned link", previous.SourcePath, source)
		}
		_, statErr := os.Lstat(destination)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if exists && len(shared) == 0 {
			if err := s.allowVerifiedForeignLXLink(destination, source, k.ID(), proof); err != nil {
				return fmt.Errorf("refusing foreign skill at %s: %w", destination, err)
			}
		}
		for _, row := range shared {
			if row.Key.Source != k.Source || row.Key.Package != k.Package {
				return fmt.Errorf("skill destination owned by another source/package: %s", destination)
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
			row.AgentHome = e.Home
			row.AgentKind = e.Kind
			return s.recordInstallation(row)
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
		row := state.Installation{Key: k, AgentID: e.ID, AgentHome: e.Home, AgentKind: e.Kind, Component: "skill", Destination: destination, SourcePath: source, Mode: mode, Digest: digest}
		if err = s.recordInstallation(row); err != nil {
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

func (s *Skills) allowVerifiedForeignLXLink(destination, source, requestedKeyID string, proof *ForeignSkillProof) error {
	if proof == nil || !validForeignSkillProof(*proof) {
		return errors.New("verified external binding proof required")
	}
	if proof.ParentKeyID != requestedKeyID {
		return errors.New("verified external parent key does not match requested skill key")
	}
	if !isUnder(filepath.Join(s.Store.Root(), "generated"), source) {
		return errors.New("replacement skill must be staged under local generated storage")
	}
	reader := s.readLXLink
	if reader == nil {
		reader = readLXReparseLink
	}
	target, isLX, err := reader(destination)
	if err != nil {
		return err
	}
	if !isLX {
		return errors.New("destination is not a verified Linux LX symlink")
	}
	if !matchesGeneratedLXTarget(target, requestedKeyID) {
		return errors.New("LX link target does not match the verified external parent key")
	}
	return nil
}

func validForeignSkillProof(proof ForeignSkillProof) bool {
	if len(proof.ParentKeyID) != 64 || proof.RegistrationName == "" || proof.URL == "" {
		return false
	}
	for _, c := range proof.ParentKeyID {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func isUnder(root, target string) bool {
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func matchesGeneratedLXTarget(target, parentKeyID string) bool {
	if target == "" || !strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\\\x00") || path.Clean(target) != target {
		return false
	}
	suffix := "/generated/" + parentKeyID + "/output-"
	idx := strings.LastIndex(target, suffix)
	if idx <= 0 {
		return false
	}
	prefix := target[:idx]
	if !strings.HasPrefix(prefix, "/") || path.Clean(prefix) != prefix {
		return false
	}
	output := target[idx+len(suffix):]
	if output == "" {
		return false
	}
	for _, c := range output {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func (s *Skills) Uninstall(ctx context.Context, k state.Key, e SkillDestination) error {
	if s.Store == nil {
		return errors.New("state store required")
	}
	selectedDir, err := filepath.Abs(e.SkillsDir)
	if err != nil {
		return err
	}
	if e.SkillsDir == "" {
		return errors.New("explicit agent skills directory required")
	}
	return func() error {
		rows, err := s.Store.Installations()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Component != "skill" || row.Key != k || row.AgentID != e.ID || filepath.Dir(row.Destination) != selectedDir || (s.removeDestination != "" && row.Destination != s.removeDestination) {
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
