package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// RegistrationRequest connects agents in this AACT installation to an MCP
// endpoint. The endpoint's runtime may belong to another installation.
type RegistrationRequest struct {
	Key          state.Key
	URL          string
	ExternalURLs map[string]string
	Transport    string
	Agents       []agents.Environment

	// preserve contains existing ineligible MCP entries carried through UI
	// desired-state updates without rewriting their config or endpoint.
	preserve []agents.Environment
}

func (s *Service) registrationTimeoutForKey(key state.Key) int {
	if key.Source == s.Source.ID {
		for _, p := range s.Source.Catalog {
			if p.ID == key.Package {
				for _, definition := range p.MCPDefinitions() {
					if (len(p.MCPs) == 0 && key.MCP == "") || definition.Name == key.MCP {
						return registrationTimeoutMS(&definition)
					}
				}
			}
		}
	}
	return 30000
}

// ConfigureRegistrations changes only local agent registration files and this
// installation's registration ledger. It never starts or claims the MCP runtime.
func (s *Service) ConfigureRegistrations(ctx context.Context, q RegistrationRequest) (out Result, err error) {
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	if q.Key.Source == "" || q.Key.Package == "" || (q.URL == "" && len(q.ExternalURLs) == 0) {
		return out, invalid(errors.New("MCP source, package, and endpoint URL are required"))
	}
	if q.Key.Source == s.Source.ID {
		p, lookupErr := s.packageByID(q.Key.Package)
		if lookupErr != nil {
			return out, invalid(fmt.Errorf("cannot attach this MCP without its catalog capability; locate the package source first: %w", lookupErr))
		}
		if p.HasMCP() && p.Skill != nil {
			externalURLs := q.ExternalURLs
			externalURL := q.URL
			if len(p.MCPs) > 0 {
				if q.URL != "" {
					if len(p.MCPs) != 1 || len(q.ExternalURLs) > 0 {
						return out, invalid(errors.New("multi-MCP capability requires a separately keyed endpoint for each definition"))
					}
					externalURLs = map[string]string{p.MCPs[0].Name: q.URL}
				}
				externalURL = ""
			}
			return s.Install(ctx, InstallRequest{
				Package: q.Key.Package, Environment: q.Key.Environment, Target: q.Key.Target,
				Agents: q.Agents, ExternalURL: externalURL, ExternalURLs: externalURLs,
			})
		}
	}
	desired := make(map[string]agents.Environment, len(q.Agents))
	timeoutMS := s.registrationTimeoutForKey(q.Key)
	for _, env := range q.Agents {
		env, err = s.registrationEnvironment(env)
		if err != nil {
			return out, err
		}
		desired[env.ID+"\x00"+env.ConfigPath] = env
	}
	preserved := make(map[string]bool, len(q.preserve))
	for _, env := range q.preserve {
		if env.ID != "" && env.ConfigPath != "" {
			preserved[env.ID+"\x00"+env.ConfigPath] = true
		}
	}
	err = s.Store.WithLock(ctx, func() error {
		rows, err := s.Store.Installations()
		if err != nil {
			return err
		}
		// Registration-only setup with no local runtime or prior local
		// registration is an explicit attach. A ledger record for an owned
		// runtime (including a now-missing one), or a legacy/local registration,
		// preserves the target's local lifecycle intent.
		externalOnly := true
		for _, row := range rows {
			if row.Key != q.Key {
				continue
			}
			if row.Component == "runtime" || (row.Component == "mcp" && !row.ExternalRegistration) {
				externalOnly = false
				break
			}
		}
		for _, row := range rows {
			registrationKey := row.AgentID + "\x00" + row.Destination
			if row.Key != q.Key || row.Component != "mcp" || desired[registrationKey].ID != "" || preserved[registrationKey] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			kind := row.AgentKind
			if kind == "" {
				kind, _, _ = strings.Cut(row.AgentID, ":")
			}
			env := agents.Environment{ID: row.AgentID, Kind: kind, Home: row.AgentHome, ConfigPath: row.Destination}
			env = s.ownedEnvironment(env, rows)
			adapter, err := agents.For(env.Kind, s.Options.Runner)
			if err == nil {
				var files []registrationFile
				files, err = snapshotRegistration(env)
				if err == nil {
					err = adapter.Unregister(ctx, env, row.RegistrationName)
					if err == nil {
						err = s.removeRegistration(row)
					}
					if err != nil {
						err = errors.Join(err, restoreRegistration(files))
					}
				}
			}
			if err != nil {
				out.Errors = append(out.Errors, env.ID+": "+err.Error())
			} else {
				out.Changes = append(out.Changes, row)
			}
		}
		for _, env := range desired {
			if err := ctx.Err(); err != nil {
				return err
			}
			rows, err := s.Store.Installations()
			if err != nil {
				return err
			}
			unchanged := false
			for _, row := range rows {
				if row.Key == q.Key && row.Component == "mcp" && row.AgentID == env.ID &&
					row.Destination == env.ConfigPath && row.URL == q.URL &&
					row.Transport == q.Transport && row.TimeoutMS == timeoutMS && row.RegistrationName == registrationName(q.Key) {
					unchanged = true
					break
				}
			}
			if unchanged {
				continue
			}
			env = s.ownedEnvironment(env, rows)
			adapter, err := agents.For(env.Kind, s.Options.Runner)
			if err != nil {
				out.Errors = append(out.Errors, env.ID+": "+err.Error())
				continue
			}
			registration := agents.Registration{Name: registrationName(q.Key), URL: q.URL, Transport: q.Transport, TimeoutMS: timeoutMS}
			files, err := snapshotRegistration(env)
			if err == nil {
				err = adapter.Register(ctx, env, registration)
				if err != nil {
					err = errors.Join(err, restoreRegistration(files))
				}
			}
			if err == nil {
				row := state.Installation{Key: q.Key, AgentID: env.ID, AgentHome: env.Home, AgentKind: env.Kind, Component: "mcp", Destination: env.ConfigPath, Mode: "registration", RegistrationName: registration.Name, URL: registration.URL, Transport: registration.Transport, TimeoutMS: registration.TimeoutMS, ExternalRegistration: externalOnly}
				if agents.IsManual(env.Kind) {
					row.Mode = "manual"
				}
				err = s.recordRegistration(row)
				if err != nil {
					err = errors.Join(err, restoreRegistration(files))
				} else {
					out.Changes = append(out.Changes, row)
				}
			}
			if err != nil {
				out.Errors = append(out.Errors, env.ID+": "+err.Error())
			}
		}
		if len(out.Errors) != 0 {
			return fmt.Errorf("registration failed: %s", strings.Join(out.Errors, "; "))
		}
		return nil
	})
	return out, err
}

type registrationIdentity struct {
	AgentID     string
	Destination string
}

func registrationIdentityKey(agentID, destination string) string {
	return agentID + "\x00" + destination
}

// removeUIRegistrations removes only selected, currently recorded MCP entries.
// It is separate from desired-state reconciliation because removal must not
// require or observe the external endpoint and must preserve every other row.
// Legacy agent-ID requests are resolved up front and rejected if they match
// more than one destination, before any config or ledger is changed.
func (s *Service) removeUIRegistrations(ctx context.Context, key state.Key, identities []registrationIdentity, legacyAgentIDs []string) (out Result, err error) {
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	if key.Source == "" || key.Package == "" {
		return out, invalid(errors.New("MCP source and package are required"))
	}
	if len(identities) > 0 && len(legacyAgentIDs) > 0 {
		return out, invalid(errors.New("mix exact registration identities or legacy agent IDs in a removal request, not both"))
	}
	if len(identities) == 0 && len(legacyAgentIDs) == 0 {
		return out, invalid(errors.New("select at least one recorded MCP registration to remove"))
	}
	err = s.Store.WithLock(ctx, func() error {
		rows, readErr := s.Store.Installations()
		if readErr != nil {
			return readErr
		}
		byAgent := make(map[string]map[string]state.Installation)
		for _, row := range rows {
			if row.Key != key || row.Component != "mcp" || strings.TrimSpace(row.Destination) == "" {
				continue
			}
			if byAgent[row.AgentID] == nil {
				byAgent[row.AgentID] = make(map[string]state.Installation)
			}
			byAgent[row.AgentID][row.Destination] = row
		}
		selected := make(map[string]bool, len(identities)+len(legacyAgentIDs))
		for _, identity := range identities {
			if strings.TrimSpace(identity.AgentID) == "" || strings.TrimSpace(identity.Destination) == "" {
				out.Errors = append(out.Errors, "exact registration identity requires an agent ID and destination")
				continue
			}
			key := registrationIdentityKey(identity.AgentID, identity.Destination)
			if selected[key] {
				out.Errors = append(out.Errors, identity.AgentID+": duplicate registration identity")
				continue
			}
			if _, exists := byAgent[identity.AgentID][identity.Destination]; !exists {
				out.Errors = append(out.Errors, identity.AgentID+": no recorded local MCP registration was found at "+identity.Destination)
				continue
			}
			selected[key] = true
		}
		legacySeen := map[string]bool{}
		for _, id := range legacyAgentIDs {
			if id == "" || legacySeen[id] {
				continue
			}
			legacySeen[id] = true
			matches := byAgent[id]
			if len(matches) == 0 {
				out.Errors = append(out.Errors, id+": no recorded local MCP registration was found")
				continue
			}
			if len(matches) > 1 {
				out.Errors = append(out.Errors, id+": ambiguous removal; choose an exact config destination")
				continue
			}
			for destination := range matches {
				selected[registrationIdentityKey(id, destination)] = true
			}
		}
		// Resolve and validate the entire request before touching any agent
		// file. A typo or ambiguous legacy ID must not partially remove a
		// different, valid selection in the same request.
		if len(out.Errors) > 0 {
			return fmt.Errorf("registration removal failed: %s", strings.Join(out.Errors, "; "))
		}
		matched := make(map[string]bool, len(selected))
		for _, row := range rows {
			if row.Key != key || row.Component != "mcp" || !selected[registrationIdentityKey(row.AgentID, row.Destination)] {
				continue
			}
			matchKey := registrationIdentityKey(row.AgentID, row.Destination)
			matched[matchKey] = true
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			kind := row.AgentKind
			if kind == "" {
				kind, _, _ = strings.Cut(row.AgentID, ":")
			}
			env := agents.Environment{ID: row.AgentID, Kind: kind, Home: row.AgentHome, ConfigPath: row.Destination}
			env = s.ownedEnvironment(env, rows)
			adapter, adapterErr := agents.For(env.Kind, s.Options.Runner)
			var files []registrationFile
			if adapterErr == nil {
				files, adapterErr = snapshotRegistration(env)
			}
			if adapterErr == nil {
				adapterErr = adapter.Unregister(ctx, env, row.RegistrationName)
			}
			if adapterErr == nil {
				adapterErr = s.removeRegistration(row)
			}
			if adapterErr != nil {
				adapterErr = errors.Join(adapterErr, restoreRegistration(files))
				out.Errors = append(out.Errors, row.AgentID+": "+adapterErr.Error())
				continue
			}
			out.Changes = append(out.Changes, row)
		}
		for identityKey := range selected {
			if !matched[identityKey] {
				out.Errors = append(out.Errors, "selected local MCP registration was not removed")
			}
		}
		if len(out.Errors) > 0 {
			return fmt.Errorf("registration removal failed: %s", strings.Join(out.Errors, "; "))
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && len(out.Errors) == 0 {
		out.Errors = append(out.Errors, err.Error())
	}
	if err == nil {
		out.Message = "Selected local MCP registrations removed"
	}
	return out, err
}

func (s *Service) registrationEnvironment(env agents.Environment) (agents.Environment, error) {
	if env.ConfigPath != "" {
		return env, nil
	}
	if agents.IsManual(env.Kind) {
		env.ConfigPath = s.manualConfigPath(env)
		return env, nil
	}
	resolved, err := agents.ResolveEnvironment(env.ID, env.Kind, env.Home)
	if err != nil {
		return env, err
	}
	env.ConfigPath = resolved.ConfigPath
	if env.SkillsDir == "" {
		env.SkillsDir = resolved.SkillsDir
	}
	return env, nil
}
