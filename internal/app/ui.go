package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"os"
	"path/filepath"
	"sort"
)

type sourceRef struct {
	ID              string   `json:"id"`
	Root            string   `json:"root"`
	ManifestPath    string   `json:"manifest_path"`
	EnvironmentRoot string   `json:"environment_root"`
	BundledRoot     string   `json:"bundled_root"`
	PackageDirs     []string `json:"package_dirs"`
}

func (s *Service) sourceRefs() ([]sourceRef, error) {
	b, e := os.ReadFile(filepath.Join(s.Store.Root(), "manager", "sources.json"))
	if os.IsNotExist(e) {
		return []sourceRef{}, nil
	}
	if e != nil {
		return nil, e
	}
	var refs []sourceRef
	e = json.Unmarshal(b, &refs)
	return refs, e
}
func (s *Service) rememberSource() error {
	refs, e := s.sourceRefs()
	if e != nil {
		return e
	}
	ref := sourceRef{ID: s.Source.ID, Root: s.Source.Root, ManifestPath: s.Source.ManifestPath, EnvironmentRoot: s.Source.EnvironmentRoot, BundledRoot: s.Options.BundledRoot}
	for _, p := range s.Source.Catalog {
		ref.PackageDirs = append(ref.PackageDirs, p.Dir)
	}
	found := false
	for n, r := range refs {
		if r.ID == ref.ID {
			refs[n] = ref
			found = true
		}
	}
	if !found {
		refs = append(refs, ref)
	}
	return state.WriteJSON(filepath.Join(s.Store.Root(), "manager", "sources.json"), refs)
}
func (s *Service) forSource(id string) (*Service, error) {
	if id == "" || id == s.Source.ID {
		return s, nil
	}
	refs, e := s.sourceRefs()
	if e != nil {
		return nil, e
	}
	for _, ref := range refs {
		if ref.ID != id {
			continue
		}
		var src config.Source
		if ref.ManifestPath != "" {
			src, e = config.Discover(ref.Root, ref.ManifestPath, ref.BundledRoot, s.Store.Root())
			if e != nil {
				return nil, fmt.Errorf("source %s unavailable: %w", id, e)
			}
			if src.ID != id {
				return nil, errors.New("source identity changed; reopen its checkout")
			}
			src.EnvironmentRoot = ref.EnvironmentRoot
		} else {
			src = config.Source{ID: id, Root: ref.Root, EnvironmentRoot: ref.EnvironmentRoot, PackageDefaults: map[string]map[string]any{}}
			for _, dir := range ref.PackageDirs {
				p, e := catalog.Load(dir)
				if e != nil {
					return nil, fmt.Errorf("source %s package unavailable: %w", id, e)
				}
				src.Catalog = append(src.Catalog, p)
			}
		}
		o := s.Options
		o.BundledRoot = ref.BundledRoot
		return New(src, s.Store, o), nil
	}
	return nil, fmt.Errorf("source %s is not registered; launch aact in its checkout", id)
}
func (s *Service) UICatalog(ctx context.Context) ([]catalog.Package, error) { return s.Catalog(ctx) }
func (s *Service) UIInventory(ctx context.Context) ([]state.Installation, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return s.Store.Installations()
}
func (s *Service) UIMCPs(ctx context.Context) ([]mcp.Instance, error) {
	return s.Options.Runtime.List(ctx)
}
func (s *Service) UIAgents(context.Context) ([]string, error) {
	return []string{"codex", "opencode", "copilot-cli", "intellij", "copilot-intellij", "generic"}, nil
}
func (s *Service) UISettings(context.Context) (map[string]string, error) {
	return map[string]string{"source": s.Source.ID, "checkout": s.Source.Root, "environment-root": s.Source.EnvironmentRoot, "state-dir": s.Store.Root()}, nil
}
func (s *Service) UISourceLabels(context.Context) (map[string]string, error) {
	labels := map[string]string{s.Source.ID: s.Source.ID}
	for _, p := range s.Source.Catalog {
		labels[p.Dir] = s.Source.ID
	}
	refs, e := s.sourceRefs()
	if e != nil {
		return nil, e
	}
	for _, r := range refs {
		labels[r.ID] = r.ID
		for _, dir := range r.PackageDirs {
			labels[dir] = r.ID
		}
	}
	return labels, nil
}
func (s *Service) UIRun(ctx context.Context, action, sourceID, packageID, agentID, environment, target string) (string, error) {
	svc, e := s.forSource(sourceID)
	if e != nil {
		return "", e
	}
	if action == "set-environment-root" {
		root, e := config.ResolvePath(target, filepath.Join(s.Source.Root, "aact.toml"))
		if e != nil {
			return "", e
		}
		info, e := os.Stat(root)
		if e != nil || !info.IsDir() {
			return "", fmt.Errorf("environment root must be an existing directory: %s", root)
		}
		e = s.Store.WithLock(ctx, func() error {
			if e := state.WriteAtomic(filepath.Join(s.Store.Root(), "config-root"), []byte(root+"\n"), 0600); e != nil {
				return e
			}
			svc.Source.EnvironmentRoot = root
			return svc.rememberSource()
		})
		return "Environment root: " + root, e
	}
	if action == "agent-info" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		a, e := agents.ResolveEnvironment(agentID, agentID, home)
		if e != nil {
			return "", e
		}
		b, _ := json.MarshalIndent(a, "", "  ")
		return string(b), nil
	}
	var out Result
	if action == "install" || action == "uninstall" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		a, e := agents.ResolveEnvironment(agentID, agentID, home)
		if e != nil {
			return "", e
		}
		q := InstallRequest{Package: packageID, Environment: environment, Target: target, Agents: []agents.Environment{a}, Interactive: action == "install"}
		if action == "install" {
			out, e = svc.Install(ctx, q)
		} else {
			out, e = svc.Uninstall(ctx, q)
		}
	} else {
		out, e = svc.MCP(ctx, MCPRequest{Action: action, Package: packageID, Environment: environment, Target: target, Interactive: action == "start" || action == "authenticate"})
	}
	if out.Logs != "" {
		return out.Logs, e
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), e
}
func (s *Service) Sources() ([]string, error) {
	refs, e := s.sourceRefs()
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, r := range refs {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out, nil
}
