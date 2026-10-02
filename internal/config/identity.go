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
	"time"
)

func sourceIdentity(manifest, explicit, stateRoot string, preview bool) (string, error) {
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
	hash := sha256.Sum256([]byte(canonical))
	recordPath := filepath.Join(stateRoot, "manager", "source-identities", hex.EncodeToString(hash[:])+".json")
	if id, err := readIdentityRecord(recordPath, canonical); err == nil {
		return id, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if preview {
		sum := sha256.Sum256([]byte(canonical))
		return "preview-local-" + hex.EncodeToString(sum[:16]), nil
	}
	if e = os.MkdirAll(filepath.Dir(recordPath), 0700); e != nil {
		return "", e
	}
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(recordPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			id, readErr := readIdentityRecord(recordPath, canonical)
			if os.IsNotExist(readErr) {
				continue
			}
			return id, readErr
		}
		if err != nil {
			return "", err
		}
		var nonce [16]byte
		_, err = rand.Read(nonce[:])
		id := "local-" + hex.EncodeToString(nonce[:])
		if err == nil {
			err = json.NewEncoder(f).Encode(identityRecord{Path: canonical, ID: id})
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(recordPath)
			return "", err
		}
		return id, nil
	}
	return "", fmt.Errorf("source identity record disappeared while being created: %s", recordPath)
}

type identityRecord struct {
	Path string `json:"path"`
	ID   string `json:"id"`
}

func readIdentityRecord(path, canonical string) (string, error) {
	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		var record identityRecord
		err = json.Unmarshal(data, &record)
		if err == nil && record.Path == canonical && safeID.MatchString(record.ID) {
			return record.ID, nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("invalid path or source id")
			}
			return "", fmt.Errorf("%s: incomplete or invalid source identity record: %w", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readOrigin(gitDir string) string {
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			gitDir = filepath.Clean(common)
		}
	}
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
