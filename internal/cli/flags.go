package cli

import (
	"errors"
	"fmt"
	"strings"
)

type flags struct {
	args, agents, homes                                                       []string
	sets                                                                      map[string][]string
	externalURLs                                                              map[string]string
	config, envroot, state, environment, target, profile, url, mcp            string
	interactive, skillsOnly, json, dryrun, apply, help, version, updateSource bool
}

func parse(args []string) (flags, error) {
	f := flags{sets: map[string][]string{}}
	for n := 0; n < len(args); n++ {
		a := args[n]
		if a == "--" {
			f.args = append(f.args, args[n+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			f.args = append(f.args, a)
			continue
		}
		name, value, has := strings.Cut(a, "=")
		switch name {
		case "--interactive":
			f.interactive = true
		case "--skills-only":
			f.skillsOnly = true
		case "--update-source":
			f.updateSource = true
		case "--json":
			f.json = true
		case "--dry-run":
			f.dryrun = true
		case "--apply":
			f.apply = true
		case "--help", "-h":
			f.help = true
		case "--version":
			f.version = true
		case "--config", "--environment-directory", "--environment-root", "--state-dir", "--environment", "--target", "--profile", "--external-url", "--mcp", "--agent", "--agent-home", "--set":
			if !has {
				n++
				if n == len(args) {
					return f, fmt.Errorf("%s requires a value", name)
				}
				value = args[n]
			}
			if value == "" {
				return f, fmt.Errorf("%s requires a nonempty value", name)
			}
			switch name {
			case "--config":
				f.config = value
			case "--environment-directory", "--environment-root":
				f.envroot = value
			case "--state-dir":
				f.state = value
			case "--environment":
				f.environment = value
			case "--target":
				f.target = value
			case "--profile":
				f.profile = value
			case "--external-url":
				if name, endpoint, named := strings.Cut(value, "="); named && name != "" && !strings.Contains(name, "://") {
					if f.externalURLs == nil {
						f.externalURLs = map[string]string{}
					}
					if _, exists := f.externalURLs[name]; exists {
						return f, fmt.Errorf("--external-url repeats MCP %q", name)
					}
					if endpoint == "" {
						return f, fmt.Errorf("--external-url %s requires a URL", name)
					}
					f.externalURLs[name] = endpoint
				} else {
					if f.url != "" {
						return f, errors.New("--external-url URL may be supplied only once")
					}
					f.url = value
				}
			case "--mcp":
				f.mcp = value
			case "--agent":
				f.agents = append(f.agents, value)
			case "--agent-home":
				f.homes = append(f.homes, value)
			case "--set":
				k, v, ok := strings.Cut(value, "=")
				if !ok || k == "" {
					return f, errors.New("--set expects name=value")
				}
				f.sets[k] = append(f.sets[k], v)
			}
		default:
			return f, fmt.Errorf("unknown flag %s", name)
		}
		if has && name != "--config" && name != "--environment-directory" && name != "--environment-root" && name != "--state-dir" && name != "--environment" && name != "--target" && name != "--profile" && name != "--external-url" && name != "--mcp" && name != "--agent" && name != "--agent-home" && name != "--set" {
			return f, fmt.Errorf("%s does not take a value", name)
		}
	}
	return f, nil
}
