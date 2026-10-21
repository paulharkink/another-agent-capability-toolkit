package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/polyrepo"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

func polyrepoCommand(ctx context.Context, args []string, f flags, store *state.Store, out, errOut io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errOut, err); return 1 }
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: aact polyrepo configure|list|peer add|remove")
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
		webhookValues := []string{f.webhookHost, f.webhookPort, f.webhookSecret}
		webhookCount := 0
		for _, value := range webhookValues {
			if value != "" {
				webhookCount++
			}
		}
		if f.disableWebhook && webhookCount != 0 {
			fmt.Fprintln(errOut, "--disable-webhook cannot be combined with webhook settings")
			return 2
		}
		if webhookCount != 0 && webhookCount != len(webhookValues) {
			fmt.Fprintln(errOut, "configure the complete webhook setting with all of --webhook-host, --webhook-port, and --webhook-secret-file")
			return 2
		}
		if webhookCount == len(webhookValues) {
			if strings.TrimSpace(f.webhookHost) == "" {
				fmt.Fprintln(errOut, "--webhook-host must not be empty")
				return 2
			}
			port, e := strconv.Atoi(f.webhookPort)
			if e != nil || port < 1 || port > 65535 {
				fmt.Fprintln(errOut, "--webhook-port must be an integer from 1 to 65535")
				return 2
			}
			secret, e := privateSecretFile(f.webhookSecret)
			if e != nil {
				return fail(e)
			}
			s.Listener = &polyrepo.Listener{Host: f.webhookHost, Port: port, SecretFile: secret}
		}
		if f.disableWebhook {
			s.Listener = nil
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
		secret, e := privateSecretFile(f.peerSecret)
		if e != nil {
			return fail(e)
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
	default:
		fmt.Fprintln(errOut, "usage: aact polyrepo configure|list|peer add|remove")
		return 2
	}
}

func privateSecretFile(path string) (string, error) {
	secret, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(secret)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("secret file must be a regular file")
	}
	// Windows file modes do not report ACL readability using POSIX permission
	// bits. Windows users must protect the file with the normal user ACL.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("secret file must not be group/world accessible")
	}
	return secret, nil
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
