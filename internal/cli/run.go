package cli

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/agents"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/app"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/forms"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/tui"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func externalURLValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

var Version = "0.1.0-dev"

const usage = `Another Agent Capability Toolkit (aact)

aact                                     Open Catalog / MCPs / Agents / Settings
aact catalog [--json]                     List this Capability Pack's capabilities
aact install PACKAGE --agent AGENT       Install skill and register its MCP
aact install PACKAGE --agent hermes --skills-only  Install only its Hermes skill
aact uninstall PACKAGE --agent AGENT     Remove owned registrations and skill
aact mcp list|status [--json]             Show MCPs from every Capability Pack
aact mcp start|stop|logs|prepare|authenticate CAPABILITY --profile NAME [--mcp SERVER]
aact profile create CAPABILITY NAME
aact agents | settings                   Show supported agents / configuration
aact config set-profile-directory PATH  Save a default profile configuration directory
aact migrate --dry-run | --apply         Inspect or adopt legacy owned state

Flags: --config PATH --state-dir PATH --profile-directory PATH
       (legacy alias: --environment-root PATH)
       --profile NAME --mcp SERVER --item ITEM --agent-home [AGENT=]PATH
       --set name=value (repeat for collections) --interactive
       --external-url URL|NAME=URL --mcp NAME --skills-only --update-source --json --help --version

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

func appOptions(errOut io.Writer, bundle string, tuiMode bool) app.Options {
	options := app.Options{Editor: forms.RunEditor, BundledRoot: bundle}
	if !tuiMode {
		options.OnStderr = func(b []byte) { errOut.Write(b) }
	}
	return options
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
	if f.environment != "" || f.target != "" {
		fmt.Fprintln(errOut, "--environment and --target are no longer selection levels; use --profile NAME from the Capability Pack profile directory")
		return 2
	}
	migrating := len(f.args) > 0 && f.args[0] == "migrate"
	if (f.dryrun || f.apply) && !migrating {
		fmt.Fprintln(errOut, "--dry-run/--apply are only supported by migrate")
		return 2
	}
	if migrating && (len(f.args) != 1 || f.dryrun == f.apply) {
		fmt.Fprintln(errOut, "usage: aact migrate --dry-run | --apply")
		return 2
	}
	cwd, e := os.Getwd()
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	var store *state.Store
	if migrating && f.dryrun {
		store, e = state.OpenReadOnly(f.state)
	} else {
		store, e = state.Open(f.state)
	}
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	bundle := bundledRoot()
	var src config.Source
	if migrating && f.dryrun {
		src, e = config.DiscoverPreview(cwd, f.config, bundle, store.Root())
	} else {
		src, e = config.Discover(cwd, f.config, bundle, store.Root())
	}
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
	if migrating {
		return runMigration(ctx, src, store, f.apply, f.json, out, errOut)
	}
	options := appOptions(errOut, bundle, len(f.args) == 0)
	svc := app.New(src, store, options)
	if len(f.args) == 0 {
		uiCtx, cancelUI := context.WithCancel(ctx)
		defer cancelUI()
		opts := []tea.ProgramOption{tea.WithContext(uiCtx), tea.WithOutput(out)}
		if in != nil {
			opts = append(opts, tea.WithInput(in))
		}
		_, e := tea.NewProgram(tui.NewContext(uiCtx, svc), opts...).Run()
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
		case viewmodel.OperationResult:
			for _, r := range value.Changes {
				fmt.Fprintf(out, "%s %s: %s\n", r.AgentID, r.Component, r.Destination)
			}
			if value.Message != "" {
				fmt.Fprintln(out, value.Message)
			}
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
		p, e := svc.PackSettings(ctx)
		if e != nil {
			return fail(e)
		}
		if e = emit(p); e != nil {
			return fail(e)
		}
		return 0
	case "config":
		if len(f.args) != 3 || (f.args[1] != "set-profile-directory" && f.args[1] != "set-environment-directory" && f.args[1] != "set-environment-root") {
			fmt.Fprintln(errOut, "usage: aact config set-profile-directory PATH")
			return 2
		}
		message, e := svc.UIRun(ctx, "set-environment-root", "", "", "", "", "", f.args[2])
		if e != nil {
			return fail(e)
		}
		fmt.Fprintln(out, strings.ReplaceAll(message, "Environment directory:", "Profile configuration directory:"))
		return 0
	case "profile":
		if len(f.args) != 4 || f.args[1] != "create" {
			fmt.Fprintln(errOut, "usage: aact profile create CAPABILITY NAME")
			return 2
		}
		if e := svc.CreateProfile(ctx, config.ProfileRef{PackID: src.ID, CapabilityID: f.args[2], Name: f.args[3]}); e != nil {
			return fail(e)
		}
		fmt.Fprintln(out, "Created profile", f.args[3])
		return 0
	case "install", "apply", "uninstall":
		if len(f.args) != 2 {
			fmt.Fprintln(errOut, "install/uninstall requires one package ID")
			return 2
		}
		if f.skillsOnly && f.args[0] != "install" {
			fmt.Fprintln(errOut, "--skills-only is only supported by install")
			return 2
		}
		inputs, e := inputValues(src, f.args[1], f.sets)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		scopes, e := agentScopes(f)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		if f.url != "" && len(f.externalURLs) > 0 {
			fmt.Fprintln(errOut, "--external-url cannot mix a bare URL with named NAME=URL values")
			return 2
		}
		for _, endpoint := range append([]string{f.url}, externalURLValues(f.externalURLs)...) {
			if endpoint == "" {
				continue
			}
			u, parseErr := url.Parse(endpoint)
			if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				fmt.Fprintln(errOut, "--external-url must be an HTTP(S) URL without credentials")
				return 2
			}
		}
		svc.Options.AgentScopes = scopes
		ref := config.ProfileRef{PackID: src.ID, CapabilityID: f.args[1], Name: f.profile}
		if ref.Name == "" && f.args[0] != "uninstall" {
			profiles, err := config.DiscoverProfiles(src.CapabilityPack(), ref.CapabilityID)
			if err != nil {
				return fail(err)
			}
			records, err := store.Profiles()
			if err != nil {
				return fail(err)
			}
			for _, r := range records {
				if r.Local && r.Key.Source == src.ID && r.Key.Package == ref.CapabilityID {
					profiles = append(profiles, config.Profile{Ref: config.ProfileRef{Name: r.Key.Target}})
				}
			}
			if len(profiles) == 0 {
				var pkg catalog.Package
				for _, p := range src.Catalog {
					if p.ID == ref.CapabilityID {
						pkg = p
					}
				}
				if _, err := forms.Resolve(pkg.Inputs, src.PackageDefaults[pkg.ID], inputs); err != nil {
					return fail(&app.InvalidInput{Err: err})
				}
				ref.Name = "default"
				if err := svc.CreateProfile(ctx, ref); err != nil {
					return fail(err)
				}
			}
		}
		urls := f.externalURLs
		if f.url != "" {
			for _, p := range src.Catalog {
				if p.ID == ref.CapabilityID {
					if len(p.MCPDefinitions()) != 1 {
						return fail(&app.InvalidInput{Err: errors.New("a bare endpoint requires exactly one MCP; use NAME=URL")})
					}
					urls = map[string]string{p.MCPDefinitions()[0].Name: f.url}
				}
			}
		}
		q := app.ProfileRequest{Ref: ref, Inputs: inputs, ItemIDs: f.items, DestinationIDs: f.agents, Interactive: f.interactive, SkillsOnly: f.skillsOnly, ExternalURLs: urls}
		var r viewmodel.OperationResult
		if f.args[0] == "uninstall" {
			r, e = svc.RemoveProfile(ctx, q)
		} else {
			r, e = svc.ApplyProfile(ctx, q)
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
		r, e := svc.RunProfileMCP(ctx, action, app.ProfileRequest{Ref: config.ProfileRef{PackID: src.ID, CapabilityID: p, Name: f.profile}, Inputs: inputs, Interactive: f.interactive}, f.mcp)
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

// Home overrides describe an agent scope; paths and native format stay inside adapters.
func agentScopes(f flags) (map[string]agents.Scope, error) {
	scopes := map[string]agents.Scope{}
	overrides := map[string]string{}
	shared := ""
	for _, v := range f.homes {
		key, path, named := strings.Cut(v, "=")
		if named {
			overrides[key] = path
		} else {
			if shared != "" {
				return nil, errors.New("provide AGENT=PATH for multiple home overrides")
			}
			shared = v
		}
	}
	for _, id := range f.agents {
		home := shared
		if v := overrides[id]; v != "" {
			home = v
		}
		scopes[id] = agents.Scope{ID: id, Home: home, ExplicitHome: home != ""}
	}
	for id := range overrides {
		if _, ok := scopes[id]; !ok {
			return nil, fmt.Errorf("agent-home %s does not name a selected --agent", id)
		}
	}
	return scopes, nil
}
