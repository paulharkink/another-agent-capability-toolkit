package cli

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/tui"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var Version = "0.1.0-dev"

const usage = `Another Agent Capability Toolkit (aact)

aact                                     Open Catalog / MCPs / Agents / Settings
aact catalog [--json]                     List this checkout's capabilities
aact install PACKAGE --agent AGENT       Install skill and register its MCP
aact uninstall PACKAGE --agent AGENT     Remove owned registrations and skill
aact mcp list|status [--json]             Show MCPs from every source
aact mcp start|stop|logs|prepare|authenticate PACKAGE
aact agents | settings                   Show supported agents / configuration
aact config set-environment-root PATH    Save the environment checkout
aact migrate --dry-run | --apply         Inspect or adopt legacy owned state

Flags: --config PATH --state-dir PATH --environment-root PATH
       --environment NAME --target NAME --agent-home [AGENT=]PATH
       --set name=value (repeat for collections) --interactive
       --external-url URL --json --help --version

Docker is required for container capabilities. Plain skills need no host runtime.
`

func bundledRoot() string {
	if p := os.Getenv("AACT_BUNDLED_ROOT"); p != "" {
		return p
	}
	exe, e := os.Executable()
	if e == nil {
		if resolved, e := filepath.EvalSymlinks(exe); e == nil {
			exe = resolved
		}
		p := filepath.Join(filepath.Dir(filepath.Dir(exe)), "packages")
		if i, e := os.Stat(p); e == nil && i.IsDir() {
			return p
		}
	}
	cwd, _ := os.Getwd()
	for dir := cwd; ; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, "packages")
		if _, e := os.Stat(filepath.Join(p, "git-repo-map", "package.toml")); e == nil {
			return p
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return ""
}
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	f, e := parse(args)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	if f.help {
		fmt.Fprint(out, usage)
		return 0
	}
	if f.version || (len(f.args) == 1 && f.args[0] == "version") {
		fmt.Fprintln(out, Version)
		return 0
	}
	cwd, e := os.Getwd()
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	store, e := state.Open(f.state)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	bundle := bundledRoot()
	src, e := config.Discover(cwd, f.config, bundle, store.Root())
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	if f.envroot != "" {
		root, e := config.ResolvePath(f.envroot, filepath.Join(cwd, "aact.toml"))
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		src, e = config.WithEnvironmentRoot(src, root)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
	}
	svc := app.New(src, store, app.Options{Editor: forms.RunEditor, BundledRoot: bundle, OnStderr: func(b []byte) { errOut.Write(b) }})
	if len(f.args) == 0 {
		opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(out)}
		if in != nil {
			opts = append(opts, tea.WithInput(in))
		}
		_, e := tea.NewProgram(tui.NewContext(ctx, svc), opts...).Run()
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
		return 0
	}
	emit := func(v any) error {
		if f.json {
			return json.NewEncoder(out).Encode(v)
		}
		switch value := v.(type) {
		case app.Result:
			for _, r := range value.Changes {
				fmt.Fprintf(out, "%s %s: %s\n", r.AgentID, r.Component, r.Destination)
			}
			for _, i := range value.Instances {
				fmt.Fprintf(out, "%s/%s %s %s %s\n", i.Key.Source, i.Key.Package, i.Key.Target, i.Status, i.URL)
			}
			if value.Logs != "" {
				fmt.Fprint(out, value.Logs)
			}
			if value.Message != "" {
				fmt.Fprintln(out, value.Message)
			}
		default:
			b, e := json.MarshalIndent(v, "", "  ")
			if e != nil {
				return e
			}
			fmt.Fprintln(out, string(b))
		}
		return nil
	}
	fail := func(e error) int {
		fmt.Fprintln(errOut, e)
		var input *app.InvalidInput
		if errors.As(e, &input) {
			return 2
		}
		return 1
	}
	switch f.args[0] {
	case "catalog":
		if len(f.args) > 2 || (len(f.args) == 2 && f.args[1] != "list") {
			fmt.Fprintln(errOut, "usage: aact catalog [list]")
			return 2
		}
		p, e := svc.Catalog(ctx)
		if e != nil {
			return fail(e)
		}
		if e = emit(p); e != nil {
			return fail(e)
		}
		return 0
	case "agents":
		p, e := svc.UIAgents(ctx)
		if e != nil {
			return fail(e)
		}
		if e = emit(p); e != nil {
			return fail(e)
		}
		return 0
	case "settings":
		p, e := svc.UISettings(ctx)
		if e != nil {
			return fail(e)
		}
		if e = emit(p); e != nil {
			return fail(e)
		}
		return 0
	case "config":
		if len(f.args) != 3 || f.args[1] != "set-environment-root" {
			fmt.Fprintln(errOut, "usage: aact config set-environment-root PATH")
			return 2
		}
		message, e := svc.UIRun(ctx, "set-environment-root", "", "", "", "", f.args[2])
		if e != nil {
			return fail(e)
		}
		fmt.Fprintln(out, message)
		return 0
	case "install", "uninstall":
		if len(f.args) != 2 {
			fmt.Fprintln(errOut, "install/uninstall requires one package ID")
			return 2
		}
		inputs, e := inputValues(src, f.args[1], f.sets)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		envs, e := agentEnvironments(f)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		if f.url != "" {
			u, e := url.Parse(f.url)
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				fmt.Fprintln(errOut, "--external-url must be an HTTP(S) URL without credentials")
				return 2
			}
		}
		q := app.InstallRequest{Package: f.args[1], Environment: f.environment, Target: f.target, Agents: envs, Inputs: inputs, Interactive: f.interactive, ExternalURL: f.url}
		var r app.Result
		if f.args[0] == "install" {
			r, e = svc.Install(ctx, q)
		} else {
			r, e = svc.Uninstall(ctx, q)
		}
		if emitErr := emit(r); emitErr != nil {
			return fail(emitErr)
		}
		if e != nil {
			return fail(e)
		}
		return 0
	case "mcp":
		if len(f.args) < 2 || len(f.args) > 3 {
			fmt.Fprintln(errOut, "usage: aact mcp ACTION [PACKAGE]")
			return 2
		}
		action := f.args[1]
		p := ""
		if len(f.args) == 3 {
			p = f.args[2]
		}
		if p == "" && action != "list" && action != "status" {
			fmt.Fprintln(errOut, "MCP action requires a package ID")
			return 2
		}
		inputs := map[string]any{}
		if p != "" {
			inputs, e = inputValues(src, p, f.sets)
			if e != nil {
				fmt.Fprintln(errOut, e)
				return 2
			}
		}
		r, e := svc.MCP(ctx, app.MCPRequest{Action: action, Package: p, Environment: f.environment, Target: f.target, Inputs: inputs, Interactive: f.interactive})
		if emitErr := emit(r); emitErr != nil {
			return fail(emitErr)
		}
		if e != nil {
			return fail(e)
		}
		return 0
	default:
		fmt.Fprintf(errOut, "unknown command %q; use --help\n", f.args[0])
		return 2
	}
}
func inputValues(src config.Source, id string, raw map[string][]string) (map[string]any, error) {
	for _, p := range src.Catalog {
		if p.ID != id {
			continue
		}
		defs := map[string]bool{}
		for _, d := range p.Inputs {
			defs[d.Name] = d.Multiple || d.Type == "multichoice" || d.Type == "multiple-choice"
		}
		out := map[string]any{}
		for k, v := range raw {
			multiple, ok := defs[k]
			if !ok {
				return nil, fmt.Errorf("unknown input %s", k)
			}
			if multiple {
				out[k] = v
			} else {
				if len(v) != 1 {
					return nil, fmt.Errorf("input %s accepts one value", k)
				}
				out[k] = v[0]
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown package %s", id)
}
func agentEnvironments(f flags) ([]agents.Environment, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	overrides := map[string]string{}
	shared := ""
	for _, v := range f.homes {
		key, path, ok := strings.Cut(v, "=")
		if ok {
			overrides[key] = path
		} else {
			if shared != "" {
				return nil, errors.New("provide AGENT=PATH for multiple home overrides")
			}
			shared = v
		}
	}
	selected := map[string]bool{}
	out := []agents.Environment{}
	for _, id := range f.agents {
		if selected[id] {
			continue
		}
		selected[id] = true
		kind, _, _ := strings.Cut(id, ":")
		root := home
		if shared != "" {
			root = shared
		}
		if v := overrides[id]; v != "" {
			root = v
		}
		env, e := agents.ResolveEnvironment(id, kind, root)
		if e != nil {
			return nil, e
		}
		out = append(out, env)
	}
	for id := range overrides {
		if !selected[id] {
			return nil, fmt.Errorf("agent-home %s does not name a selected --agent", id)
		}
	}
	return out, nil
}
