package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
)

type AuthAdoption struct {
	Key  Key    `json:"key"`
	Path string `json:"path"`
}
type Migration struct {
	Changes         []Installation `json:"changes"`
	Conflicts       []string       `json:"conflicts"`
	EnvironmentRoot string         `json:"environment_root,omitempty"`
	Settings        map[string]any `json:"settings,omitempty"`
	Auth            []AuthAdoption `json:"auth,omitempty"`
	store           *Store
	source          config.Source
}

// PlanMigration reads legacy metadata and verifies exact ownership without writing files.
// Authentication file contents are never read; adoption stores only a directory alias.
func PlanMigration(ctx context.Context, source config.Source, store *Store) (Migration, error) {
	plan := Migration{Changes: []Installation{}, Conflicts: []string{}, Auth: []AuthAdoption{}, Settings: map[string]any{}, store: store, source: source}
	if store == nil || source.ID == "" {
		return plan, errors.New("migration requires a source ID and state store")
	}
	if e := ctx.Err(); e != nil {
		return plan, e
	}
	existing, e := store.Installations()
	if e != nil {
		return plan, e
	}
	settings, e := migrationSettings(store)
	if e != nil {
		return plan, e
	}
	plan.Settings = settings
	plan.EnvironmentRoot = source.EnvironmentRoot
	if plan.EnvironmentRoot == "" {
		if b, e := os.ReadFile(filepath.Join(store.Root(), "config-root")); e == nil {
			if value := strings.TrimSpace(string(b)); value != "" {
				plan.EnvironmentRoot, e = config.ResolvePath(value, filepath.Join(store.Root(), "config-root"))
				if e != nil {
					return plan, e
				}
			}
		} else if !os.IsNotExist(e) {
			return plan, e
		}
	}
	if plan.EnvironmentRoot != "" {
		if _, ok := plan.Settings["environment_root"]; !ok {
			plan.Settings["environment_root"] = plan.EnvironmentRoot
		}
	}
	skillRoot, e := legacySkillRoot(source)
	if e != nil {
		return plan, e
	}
	for _, p := range source.Catalog {
		if e := ctx.Err(); e != nil {
			return plan, e
		}
		if p.Skill == nil {
			continue
		}
		destination := filepath.Join(skillRoot, p.Skill.Name)
		info, e := os.Lstat(destination)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return plan, e
		}
		allowed := []string{p.Dir, filepath.Join(source.Root, "skills", p.Skill.Name)}
		mode, origin, digest, e := legacyOwnership(ctx, destination, info, allowed)
		if e != nil {
			plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("%s: %v", destination, e))
			continue
		}
		row := Installation{Key: Key{Source: source.ID, Package: p.ID, Target: "default"}, AgentID: legacyAgentID(skillRoot, settings), Component: "skill", Destination: destination, SourcePath: origin, Mode: mode, Digest: digest}
		owned := false
		conflict := false
		for _, known := range existing {
			if known.Destination == destination && known.Component == "skill" {
				if known.Key.Source != source.ID || known.Key.Package != p.ID {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("%s is already owned by source %s", destination, known.Key.Source))
					conflict = true
					break
				}
				owned = true
			}
		}
		if !owned && !conflict {
			plan.Changes = append(plan.Changes, row)
		}
	}
	source.EnvironmentRoot = plan.EnvironmentRoot
	aliases := map[string]string{}
	if e = readJSON(filepath.Join(store.Root(), "manager", "auth-aliases.json"), &aliases); e != nil && !os.IsNotExist(e) {
		return plan, e
	}
	var refs []struct {
		ID              string `json:"id"`
		EnvironmentRoot string `json:"environment_root"`
	}
	if e = readJSON(filepath.Join(store.Root(), "manager", "sources.json"), &refs); e != nil && !os.IsNotExist(e) {
		return plan, e
	}
	for _, p := range source.Catalog {
		if p.MCP == nil {
			continue
		}
		legacyRoot := filepath.Join(store.Root(), p.ID)
		environments, e := os.ReadDir(legacyRoot)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return plan, e
		}
		for _, environment := range environments {
			if !environment.IsDir() {
				continue
			}
			targets, e := os.ReadDir(filepath.Join(legacyRoot, environment.Name()))
			if e != nil {
				return plan, e
			}
			for _, target := range targets {
				if e := ctx.Err(); e != nil {
					return plan, e
				}
				if !target.IsDir() {
					continue
				}
				path := filepath.Join(legacyRoot, environment.Name(), target.Name())
				if e := migrationPathContained(store.Root(), path); e != nil {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("legacy authentication %s: %v", path, e))
					continue
				}

				if _, e := config.LoadTarget(source, p.ID, environment.Name(), target.Name()); e != nil {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("legacy authentication %s lacks a valid matching target", path))
					continue
				}
				k := Key{Source: source.ID, Package: p.ID, Environment: environment.Name(), Target: target.Name()}
				ambiguous := false
				for id, ownedPath := range aliases {
					if id != k.ID() && filepath.Clean(ownedPath) == filepath.Clean(path) {
						ambiguous = true
					}
				}
				for _, ref := range refs {
					if ref.ID != source.ID && ref.EnvironmentRoot != "" && filepath.Clean(ref.EnvironmentRoot) == filepath.Clean(source.EnvironmentRoot) {
						ambiguous = true
					}
				}
				if ambiguous {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("legacy authentication %s is owned by or ambiguous with another source; use separate state", path))
					continue
				}
				if aliases[k.ID()] == path {
					continue
				}
				plan.Auth = append(plan.Auth, AuthAdoption{Key: k, Path: path})
			}
		}
	}
	sort.Slice(plan.Changes, func(i, j int) bool { return plan.Changes[i].Destination < plan.Changes[j].Destination })
	sort.Slice(plan.Auth, func(i, j int) bool { return plan.Auth[i].Path < plan.Auth[j].Path })
	sort.Strings(plan.Conflicts)
	return plan, nil
}

func migrationSettings(store *Store) (map[string]any, error) {
	settings := map[string]any{}
	e := readJSON(filepath.Join(store.Root(), "manager", "settings.json"), &settings)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	path := os.Getenv("AACT_LEGACY_SETTINGS")
	if path == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			base = filepath.Join(home, ".config")
		}
		path = filepath.Join(base, "agent-skills", "agent-manager.json")
	}
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return settings, nil
	}
	if e != nil {
		return nil, e
	}
	var old struct {
		Agents []string `json:"agents"`
	}
	if e = json.Unmarshal(data, &old); e != nil {
		return nil, fmt.Errorf("%s: %w", path, e)
	}
	if old.Agents != nil {
		if _, present := settings["agents"]; !present {
			settings["agents"] = old.Agents
		}
	}
	return settings, nil
}
func legacySkillRoot(source config.Source) (string, error) {
	value := os.Getenv("AACT_LEGACY_SKILLS_DIR")
	if value == "" {
		value = os.Getenv("LOCAL_AGENT_SKILLS_DIR")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	if value == "" {
		if codex := os.Getenv("CODEX_HOME"); codex != "" {
			value = filepath.Join(codex, "skills")
		} else {
			file := filepath.Join(source.Root, "skills", ".config", "config")
			if data, e := os.ReadFile(file); e == nil {
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "LOCAL_AGENT_SKILLS_DIR=") {
						value = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "LOCAL_AGENT_SKILLS_DIR=")), "\"'")
						break
					}
				}
			} else if !os.IsNotExist(e) {
				return "", e
			}
		}
	}
	if value == "" {
		value = filepath.Join(home, ".agents", "skills")
	}
	value = os.Expand(value, func(key string) string {
		if key == "HOME" {
			return home
		}
		if key == "CODEX_HOME" {
			return os.Getenv("CODEX_HOME")
		}
		return "${" + key + "}"
	})
	if strings.Contains(value, "${") || strings.Contains(value, "$(") || strings.Contains(value, "`") {
		return "", fmt.Errorf("legacy skill path contains unsupported shell expansion")
	}
	return config.ResolvePath(value, filepath.Join(source.Root, "skills", ".config", "config"))
}
func legacyAgentID(root string, settings map[string]any) string {
	switch {
	case strings.Contains(filepath.ToSlash(root), "/.config/opencode/"):
		return "opencode"
	case strings.Contains(filepath.ToSlash(root), "/.copilot/"):
		return "copilot-cli"
	case strings.Contains(filepath.ToSlash(root), "/.ai/"):
		return "intellij"
	}
	if ids, ok := settings["agents"].([]string); ok && len(ids) > 0 {
		return ids[0]
	}
	if ids, ok := settings["agents"].([]any); ok && len(ids) > 0 {
		if id, ok := ids[0].(string); ok {
			return id
		}
	}
	return "codex"
}
func legacyOwnership(ctx context.Context, destination string, info os.FileInfo, allowed []string) (mode, origin, digest string, err error) {
	if info.Mode()&os.ModeSymlink != 0 {
		link, e := os.Readlink(destination)
		if e != nil {
			return "", "", "", e
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(destination), link)
		}
		for _, source := range allowed {
			if source != "" && sameLegacyPath(link, source) {
				digest, e := migrationDigest(ctx, source)
				return "symlink", filepath.Clean(source), digest, e
			}
		}
		return "", "", "", errors.New("symlink is not owned by this source")
	}
	if !info.IsDir() {
		return "", "", "", errors.New("legacy destination is not an owned skill directory")
	}
	for _, marker := range []string{".agent-skills-generated-skill", ".ozon-devtools-generated-skill"} {
		data, e := os.ReadFile(filepath.Join(destination, marker))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", "", "", e
		}
		for _, source := range allowed {
			if source != "" && sameLegacyPath(strings.TrimSpace(string(data)), source) {
				if info, e := os.Stat(filepath.Join(destination, "SKILL.md")); e != nil || !info.Mode().IsRegular() {
					return "", "", "", errors.New("generated skill lacks a regular SKILL.md")
				}
				digest, e := migrationDigest(ctx, destination)
				return "copy", destination, digest, e
			}
		}
	}
	return "", "", "", errors.New("unowned legacy resource refused")
}
func sameLegacyPath(a, b string) bool {
	absA, e := filepath.Abs(a)
	if e != nil {
		return false
	}
	absB, e := filepath.Abs(b)
	if e != nil {
		return false
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

// Keep the install ownership digest format: relative path, permissions, then file bytes.
func migrationDigest(ctx context.Context, root string) (string, error) {
	h := sha256.New()
	e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill resource symlink unsupported: %s", path)
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		base := entry.Name()
		if base == "package.toml" || filepath.Ext(base) == ".mustache" || filepath.Ext(base) == ".tmpl" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), info.Mode().Perm())
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported skill resource %s", path)
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		_, e = io.Copy(h, f)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		h.Write([]byte{0})
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), e
}

// ApplyMigration adopts metadata only; original source paths, links and credentials stay in place.
func ApplyMigration(ctx context.Context, plan Migration) error {
	if plan.store == nil {
		return errors.New("migration plan must come from PlanMigration")
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("migration conflicts: %s", strings.Join(plan.Conflicts, "; "))
	}
	return plan.store.WithLock(ctx, func() error {
		current, e := PlanMigration(ctx, plan.source, plan.store)
		if e != nil {
			return e
		}
		if len(current.Conflicts) > 0 {
			return fmt.Errorf("migration ownership changed: %s", strings.Join(current.Conflicts, "; "))
		}
		if !reflect.DeepEqual(current.Changes, plan.Changes) || !reflect.DeepEqual(current.Auth, plan.Auth) || !reflect.DeepEqual(current.Settings, plan.Settings) || current.EnvironmentRoot != plan.EnvironmentRoot {
			return errors.New("migration resources changed since planning; run migrate --dry-run again")
		}
		for _, change := range plan.Changes {
			if e := ctx.Err(); e != nil {
				return e
			}
			if e = plan.store.Record(change); e != nil {
				return e
			}
		}
		for _, auth := range plan.Auth {
			if e := ctx.Err(); e != nil {
				return e
			}
			if e = plan.store.AdoptAuth(auth.Key, auth.Path); e != nil {
				return e
			}
		}
		if len(plan.Settings) > 0 {
			if e = WriteJSON(filepath.Join(plan.store.Root(), "manager", "settings.json"), plan.Settings); e != nil {
				return e
			}
		}
		return nil
	})
}

func migrationPathContained(root, path string) error {
	realRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	realPath, e := filepath.EvalSymlinks(path)
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(realRoot, realPath)
	if e != nil {
		return e
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("authentication directory escapes the state root")
	}
	return nil
}
