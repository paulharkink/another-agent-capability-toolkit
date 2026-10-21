package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/polyrepo"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func polyrepoCommand(ctx context.Context, args []string, f flags, store *state.Store, out, errOut io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errOut, err); return 1 }
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: aact polyrepo configure|list|apply")
		return 2
	}
	s, err := polyrepo.Load(store.Root())
	if err != nil {
		return fail(err)
	}
	switch args[1] {
	case "configure":
		if len(f.roots) == 0 && len(s.Roots) == 0 {
			fmt.Fprintln(errOut, "polyrepo configure requires --root PATH")
			return 2
		}
		s.Roots = append(s.Roots, f.roots...)
		s.Hosts = append(s.Hosts, f.hosts...)
		if f.worktreeRoot != "" {
			s.WorktreeRoot = f.worktreeRoot
		}
		if err = polyrepo.Save(store.Root(), s); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "Configured %d trusted root(s)\n", len(s.Roots))
		return 0
	case "list":
		inv, e := polyrepo.Refresh(ctx, store.Root())
		if e != nil {
			return fail(e)
		}
		return printJSON(out, inv)
	case "peer":
		if len(args) == 3 && args[2] == "remove" {
			if f.peerID == "" {
				fmt.Fprintln(errOut, "polyrepo peer remove requires --peer-id ID")
				return 2
			}
			peers := s.Peers[:0]
			for _, p := range s.Peers {
				if p.PeerID != f.peerID {
					peers = append(peers, p)
				}
			}
			s.Peers = peers
			if err = polyrepo.Save(store.Root(), s); err != nil {
				return fail(err)
			}
			fmt.Fprintln(out, "Removed peer", f.peerID)
			return 0
		}
		if len(args) != 3 || args[2] != "add" || f.peerID == "" || f.peerURL == "" || f.peerSecret == "" {
			fmt.Fprintln(errOut, "usage: aact polyrepo peer add --peer-id ID --peer-url URL --secret-file PATH [--allowed-root PATH]")
			return 2
		}
		u, e := url.Parse(f.peerURL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			fmt.Fprintln(errOut, "peer URL must be HTTP(S) without credentials")
			return 2
		}
		secret, e := filepath.Abs(f.peerSecret)
		if e != nil {
			return fail(e)
		}
		st, e := os.Stat(secret)
		if e != nil {
			return fail(e)
		}
		if !st.Mode().IsRegular() {
			fmt.Fprintln(errOut, "peer secret file must be a regular file")
			return 2
		}
		if st.Mode().Perm()&0077 != 0 {
			fmt.Fprintln(errOut, "peer secret file must not be group/world accessible")
			return 2
		}
		for _, p := range s.Peers {
			if p.PeerID == f.peerID {
				fmt.Fprintln(errOut, "peer ID already configured")
				return 2
			}
		}
		s.Peers = append(s.Peers, polyrepo.Peer{PeerID: f.peerID, URL: f.peerURL, SecretFile: secret, AllowedRoots: f.allowedRoots})
		if err = polyrepo.Save(store.Root(), s); err != nil {
			return fail(err)
		}
		fmt.Fprintln(out, "Added peer", f.peerID)
		return 0
	case "apply":
		if f.opencodeConfig == "" {
			fmt.Fprintln(errOut, "polyrepo apply requires --opencode-config PATH")
			return 2
		}
		home := polyrepo.OpenCodeHome(f.homes)
		if home == "" {
			fmt.Fprintln(errOut, "polyrepo apply requires --agent-home opencode=PATH")
			return 2
		}
		entry, count, nodeID, revision, e := polyrepo.Apply(ctx, store, f.courierSource, f.opencodeConfig, home, errOut)
		if e != nil {
			return fail(e)
		}
		fmt.Fprintf(out, "Built Courier revision %s at %s; registered OpenCode plugin with %d repositories from node %s\n", revision, entry, count, nodeID)
		return 0
	default:
		fmt.Fprintln(errOut, "usage: aact polyrepo configure|list|apply")
		return 2
	}
}

func printJSON(w io.Writer, v any) int {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		fmt.Fprintln(w, e)
		return 1
	}
	_, e = w.Write(append(b, '\n'))
	if e != nil {
		return 1
	}
	return 0
}
