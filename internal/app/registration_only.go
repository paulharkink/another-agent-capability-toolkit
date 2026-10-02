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
	Key       state.Key
	URL       string
	Transport string
	Agents    []agents.Environment
}

// ConfigureRegistrations changes only local agent registration files and this
// installation's registration ledger. It never starts or claims the MCP runtime.
func (s *Service) ConfigureRegistrations(ctx context.Context, q RegistrationRequest) (out Result, err error) {
	out.Changes = []state.Installation{}
	out.Errors = []string{}
	if q.Key.Source == "" || q.Key.Package == "" || q.URL == "" {
		return out, invalid(errors.New("MCP source, package, and endpoint URL are required"))
	}
	desired := make(map[string]agents.Environment, len(q.Agents))
	for _, env := range q.Agents {
		env, err = s.registrationEnvironment(env)
		if err != nil {
			return out, err
		}
		desired[env.ID+"\x00"+env.ConfigPath] = env
	}
	err = s.Store.WithLock(ctx, func() error {
		rows, err := s.Store.Installations()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Key != q.Key || row.Component != "mcp" || desired[row.AgentID+"\x00"+row.Destination].ID != "" {
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
					row.Transport == q.Transport && row.RegistrationName == registrationName(q.Key) {
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
			registration := agents.Registration{Name: registrationName(q.Key), URL: q.URL, Transport: q.Transport, TimeoutMS: 30000}
			files, err := snapshotRegistration(env)
			if err == nil {
				err = adapter.Register(ctx, env, registration)
				if err != nil {
					err = errors.Join(err, restoreRegistration(files))
				}
			}
			if err == nil {
				row := state.Installation{Key: q.Key, AgentID: env.ID, AgentHome: env.Home, AgentKind: env.Kind, Component: "mcp", Destination: env.ConfigPath, Mode: "registration", RegistrationName: registration.Name, URL: registration.URL, Transport: registration.Transport, TimeoutMS: registration.TimeoutMS}
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
