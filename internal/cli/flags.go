package cli

import (
	"errors"
	"fmt"
	"strings"
)

type flags struct {
	args, agents, homes                                                       []string
	sets                                                                      map[string][]string
	config, envroot, state, environment, target, url                          string
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
		case "--config", "--environment-root", "--state-dir", "--environment", "--target", "--external-url", "--agent", "--agent-home", "--set":
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
			case "--environment-root":
				f.envroot = value
			case "--state-dir":
				f.state = value
			case "--environment":
				f.environment = value
			case "--target":
				f.target = value
			case "--external-url":
				f.url = value
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
		if has && name != "--config" && name != "--environment-root" && name != "--state-dir" && name != "--environment" && name != "--target" && name != "--external-url" && name != "--agent" && name != "--agent-home" && name != "--set" {
			return f, fmt.Errorf("%s does not take a value", name)
		}
	}
	return f, nil
}
