package main

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var scpRemote = regexp.MustCompile(`^(?:[^/@:\s]+@)?(\[[^\]]+\]|[^/:\\\s]+):(.+)$`)

// NormalizeRemote identifies a network remote without exposing user credentials.
// Hostnames are case insensitive; repository names retain their case. Default
// protocol ports are removed while non-default ports remain part of identity.
func NormalizeRemote(remote string) (string, string, error) {
	remote = strings.TrimSpace(remote)
	invalid := errors.New("unsupported or malformed network remote")
	var host, name string
	if strings.Contains(remote, "\n") || strings.Contains(remote, "\r") {
		return "", "", invalid
	}
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return "", "", invalid
		}
		defaults := map[string]string{"https": "443", "http": "80", "ssh": "22", "git": "9418"}
		def, ok := defaults[strings.ToLower(u.Scheme)]
		if !ok {
			return "", "", invalid
		}
		host = strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		port := u.Port()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if port != "" && port != def {
			host = net.JoinHostPort(strings.Trim(host, "[]"), port)
		}
		name = u.Path
	} else {
		// Windows drive paths are local paths rather than SCP remotes.
		if len(remote) >= 2 && remote[1] == ':' && ((remote[0] >= 'a' && remote[0] <= 'z') || (remote[0] >= 'A' && remote[0] <= 'Z')) {
			return "", "", invalid
		}
		parts := scpRemote.FindStringSubmatch(remote)
		if parts == nil {
			return "", "", invalid
		}
		host = strings.ToLower(strings.TrimSuffix(parts[1], "."))
		name = parts[2]
	}
	name = strings.Trim(name, "/")
	name = strings.TrimSuffix(name, ".git")
	if host == "" || name == "" || strings.ContainsAny(name, "\\?#") {
		return "", "", invalid
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", "", invalid
		}
	}
	for _, c := range host + name {
		if unicode.IsControl(c) {
			return "", "", invalid
		}
	}
	return host, name, nil
}
