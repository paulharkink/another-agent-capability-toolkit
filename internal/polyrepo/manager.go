package polyrepo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/repomap"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

const InventoryName = "repo-map-v1.json"

type Peer struct {
	PeerID       string   `json:"peerId"`
	URL          string   `json:"url"`
	SecretFile   string   `json:"secretFile"`
	AllowedRoots []string `json:"allowedRoots,omitempty"`
}
type Settings struct {
	NodeID       string   `json:"nodeId,omitempty"`
	Roots        []string `json:"roots,omitempty"`
	Hosts        []string `json:"hosts,omitempty"`
	Peers        []Peer   `json:"peers,omitempty"`
	WorktreeRoot string   `json:"worktreeRoot,omitempty"`
}
type Worktree struct {
	WorktreeID string `json:"worktreeId"`
	Branch     string `json:"branch"`
	Kind       string `json:"kind"`
	Dirty      bool   `json:"dirty"`
}
type Repository struct {
	Host      string     `json:"host"`
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	RepoID    string     `json:"repoId"`
	Worktrees []Worktree `json:"worktrees"`
}
type Inventory struct {
	Version      int          `json:"version"`
	NodeID       string       `json:"nodeId"`
	Repositories []Repository `json:"repositories"`
	Peers        []Peer       `json:"peers"`
	WorktreeRoot string       `json:"worktreeRoot,omitempty"`
}

func settingsFile(root string) string { return filepath.Join(root, "manager", "settings.json") }
func load(root string) (map[string]json.RawMessage, Settings, error) {
	b, e := os.ReadFile(settingsFile(root))
	raw := map[string]json.RawMessage{}
	if e != nil && !os.IsNotExist(e) {
		return nil, Settings{}, e
	}
	if os.IsNotExist(e) {
		e = nil
	}
	if e == nil && len(b) > 0 {
		if e = json.Unmarshal(b, &raw); e != nil {
			return nil, Settings{}, e
		}
	}
	var s Settings
	if v := raw["polyrepo"]; len(v) > 0 {
		e = json.Unmarshal(v, &s)
	}
	return raw, s, e
}
func Save(root string, s Settings) error {
	if s.NodeID == "" {
		s.NodeID = makeID()
		if s.NodeID == "" {
			return errors.New("cannot create node UUID")
		}
	}
	s.Roots = cleanPaths(s.Roots)
	s.Hosts = cleanStrings(s.Hosts)
	if s.WorktreeRoot != "" {
		roots := cleanPaths([]string{s.WorktreeRoot})
		if len(roots) > 0 {
			s.WorktreeRoot = roots[0]
		}
	}
	for i := range s.Peers {
		s.Peers[i].AllowedRoots = cleanPaths(s.Peers[i].AllowedRoots)
		s.Peers[i].SecretFile = filepath.Clean(s.Peers[i].SecretFile)
	}
	raw, _, e := load(root)
	if e != nil {
		return e
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	raw["polyrepo"] = b
	return state.WriteJSON(settingsFile(root), raw)
}
func Load(root string) (Settings, error) {
	_, s, e := load(root)
	if e == nil && s.NodeID == "" {
		s.NodeID = ""
	}
	return s, e
}
func cleanPaths(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range v {
		if strings.TrimSpace(p) == "" {
			continue
		}
		a, e := filepath.Abs(p)
		if e != nil {
			continue
		}
		a = filepath.Clean(a)
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}
func cleanStrings(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Refresh writes a node-local inventory by walking explicitly trusted roots. It
// uses only local Git metadata and never contacts remotes.
func Refresh(ctx context.Context, stateRoot string) (Inventory, error) {
	s, e := Load(stateRoot)
	if e != nil {
		return Inventory{}, e
	}
	if len(s.Roots) == 0 {
		return Inventory{}, errors.New("configure at least one trusted root with aact polyrepo configure")
	}
	if s.NodeID == "" {
		s.NodeID = makeID()
		if e = Save(stateRoot, s); e != nil {
			return Inventory{}, e
		}
	}
	inv := Inventory{Version: 1, NodeID: s.NodeID, Repositories: []Repository{}, Peers: append([]Peer{}, s.Peers...), WorktreeRoot: s.WorktreeRoot}
	rows, e := repomap.Scan(ctx, s.Roots, s.Hosts)
	if e != nil {
		return inv, e
	}
	for _, row := range rows {
		real, x := filepath.EvalSymlinks(row.Path)
		if x != nil {
			return inv, x
		}
		common, x := repomap.CanonicalGitCommonDir(real)
		if x != nil {
			return inv, x
		}
		branch, _ := git(ctx, real, "branch", "--show-current")
		dirty, _ := git(ctx, real, "status", "--porcelain")
		kind := "main"
		if info, x := os.Stat(filepath.Join(real, ".git")); x == nil && !info.IsDir() {
			kind = "worktree"
		}
		inv.Repositories = append(inv.Repositories, Repository{row.Host, row.Name, real, hash(common), []Worktree{{WorktreeID: hash(real), Branch: strings.TrimSpace(string(branch)), Kind: kind, Dirty: len(dirty) > 0}}})
	}
	sort.Slice(inv.Repositories, func(i, j int) bool {
		a, b := inv.Repositories[i], inv.Repositories[j]
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	if e = state.WriteJSON(filepath.Join(stateRoot, "manager", InventoryName), inv); e != nil {
		return inv, e
	}
	return inv, nil
}
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	return c.Output()
}
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func makeID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	x := hex.EncodeToString(b[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
}
func AddRoot(s Settings, p string) Settings { s.Roots = append(s.Roots, p); return s }
func CheckInventory(root string) error {
	var i Inventory
	b, e := os.ReadFile(filepath.Join(root, "manager", InventoryName))
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &i); e != nil {
		return e
	}
	if i.Version != 1 || i.NodeID == "" {
		return fmt.Errorf("unsupported or malformed repo map")
	}
	return nil
}
