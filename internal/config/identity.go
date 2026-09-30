package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	gitconfig "github.com/go-git/go-git/v5/config"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func sourceIdentity(manifest, explicit, stateRoot string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	for dir := filepath.Dir(manifest); ; dir = filepath.Dir(dir) {
		gitDir := filepath.Join(dir, ".git")
		if info, e := os.Stat(gitDir); e == nil {
			if !info.IsDir() {
				data, e := os.ReadFile(gitDir)
				if e != nil {
					return "", e
				}
				p := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:"))
				if !filepath.IsAbs(p) {
					p = filepath.Join(dir, p)
				}
				gitDir = p
			}
			origin := readOrigin(gitDir)
			if origin != "" {
				rel, e := filepath.Rel(dir, manifest)
				if e != nil {
					return "", e
				}
				sum := sha256.Sum256([]byte(normalizeOrigin(origin) + "\n" + filepath.ToSlash(rel)))
				return "git-" + hex.EncodeToString(sum[:16]), nil
			}
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	canonical, e := filepath.Abs(manifest)
	if e != nil {
		return "", e
	}
	if path, e := filepath.EvalSymlinks(canonical); e == nil {
		canonical = path
	}
	if stateRoot == "" {
		sum := sha256.Sum256([]byte(canonical))
		return "local-" + hex.EncodeToString(sum[:16]), nil
	}
	path := filepath.Join(stateRoot, "manager", "source-identities.json")
	ids := map[string]string{}
	data, e := os.ReadFile(path)
	if e == nil {
		if e = json.Unmarshal(data, &ids); e != nil {
			return "", fmt.Errorf("%s: %w", path, e)
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if id := ids[canonical]; id != "" {
		return id, nil
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return "", e
	}
	id := "local-" + hex.EncodeToString(nonce[:])
	ids[canonical] = id
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	data, e = json.MarshalIndent(ids, "", "  ")
	if e != nil {
		return "", e
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), "source-identities-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(data); e != nil {
		tmp.Close()
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(tmp.Name(), path); e != nil {
		return "", e
	}
	return id, nil
}
func readOrigin(gitDir string) string {
	f, e := os.Open(filepath.Join(gitDir, "config"))
	if e != nil {
		return ""
	}
	defer f.Close()
	cfg, e := gitconfig.ReadConfig(f)
	if e != nil {
		return ""
	}
	if remote := cfg.Remotes["origin"]; remote != nil && len(remote.URLs) > 0 {
		return remote.URLs[0]
	}
	return ""
}
func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if !strings.Contains(origin, "://") {
		if at := strings.Index(origin, "@"); at >= 0 {
			origin = origin[at+1:]
		}
		if colon := strings.Index(origin, ":"); colon >= 0 {
			origin = "ssh://" + origin[:colon] + "/" + origin[colon+1:]
		}
	}
	if u, e := url.Parse(origin); e == nil && u.Host != "" {
		host := strings.ToLower(u.Host)
		path := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
		return host + "/" + strings.TrimPrefix(path, "/")
	}
	return strings.TrimSuffix(strings.TrimSuffix(origin, "/"), ".git")
}
