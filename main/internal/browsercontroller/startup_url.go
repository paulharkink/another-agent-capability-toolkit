package browsercontroller

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// ValidateStartupURL accepts only a non-secret HTTP(S) URL suitable for the
// browser authentication helper image's traced shell startup path. Query strings, fragments,
// credentials, non-ASCII hosts, shell metacharacters, and traversal paths are
// intentionally unsupported.
func ValidateStartupURL(raw string) error {
	if raw == "" {
		return nil
	}
	for _, r := range raw {
		if r > 0x7e || r <= 0x20 || !startupURLRuneAllowed(r) {
			return errors.New("browser startup URL must use safe HTTP(S) origin/path characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || strings.ContainsAny(raw, "?#@\\") {
		return errors.New("browser startup URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host == "" || strings.Contains(host, ":") || strings.HasSuffix(host, ".") || strings.HasSuffix(u.Host, ":") || host != strings.ToLower(host) || (ip == nil && !validDomain(host)) || (ip != nil && ip.String() != host) {
		return errors.New("browser startup URL host is invalid")
	}
	if ip == nil && !strings.ContainsAny(host, "abcdefghijklmnopqrstuvwxyz") {
		return errors.New("browser startup URL host must be a canonical DNS name or IP address")
	}
	if ip == nil && whatwgNumericFinalLabel(host) {
		return errors.New("browser startup URL host has a noncanonical IPv4 numeric form")
	}
	if port := u.Port(); port != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil || parsed < 1 || parsed > 65535 || strconv.Itoa(parsed) != port {
			return errors.New("browser startup URL port is invalid")
		}
	}
	if u.RawPath != "" {
		return errors.New("browser startup URL path must use canonical unescaped characters")
	}
	decodedPath, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return errors.New("browser startup URL path is invalid")
	}
	if strings.ContainsAny(decodedPath, "\\?#@") {
		return errors.New("browser startup URL path is invalid")
	}
	for _, r := range decodedPath {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("/._~-", r)) {
			return errors.New("browser startup URL path contains unsafe characters")
		}
	}
	for _, segment := range strings.Split(decodedPath, "/") {
		if segment == "." || segment == ".." {
			return errors.New("browser startup URL path must not contain traversal segments")
		}
	}
	if decodedPath != "" {
		cleaned := path.Clean(decodedPath)
		if strings.HasSuffix(decodedPath, "/") && !strings.HasSuffix(cleaned, "/") {
			cleaned += "/"
		}
		if cleaned != decodedPath {
			return errors.New("browser startup URL path must be canonical")
		}
	}
	return nil
}

func startupURLRuneAllowed(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(":/.-_~%", r)
}

func whatwgNumericFinalLabel(host string) bool {
	label := host
	if dot := strings.LastIndexByte(host, '.'); dot >= 0 {
		label = host[dot+1:]
	}
	if label == "" {
		return false
	}
	if strings.HasPrefix(label, "0x") {
		hexDigits := label[2:]
		if hexDigits == "" {
			return true
		}
		for _, r := range hexDigits {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				return false
			}
		}
		return true
	}
	for _, r := range label {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ValidateStartupURLAgainstAccessConfig also ensures an explicit browser
// allowlist permits the startup destination. Popup policy is independent.
func ValidateStartupURLAgainstAccessConfig(startupURL, accessConfig string) error {
	if err := ValidateStartupURL(startupURL); err != nil {
		return err
	}
	if startupURL == "" {
		return nil
	}
	if err := ValidateAccessConfig(accessConfig); err != nil {
		return err
	}
	var config browserAccessConfig
	if accessConfig != "" {
		if err := json.Unmarshal([]byte(accessConfig), &config); err != nil {
			return errors.New("browser access configuration is invalid")
		}
	}
	parsed, _ := url.Parse(startupURL)
	for _, pattern := range config.BlockedURLs {
		if startupURLMatchesPattern(parsed, pattern) {
			return errors.New("browser startup URL is blocked by the configured blocked_urls policy")
		}
	}
	if len(config.AllowedURLs) == 0 {
		return nil
	}
	for _, pattern := range config.AllowedURLs {
		if startupURLMatchesPattern(parsed, pattern) {
			return nil
		}
	}
	return errors.New("browser startup URL is blocked by the configured allowed_urls policy")
}

func startupURLMatchesPattern(target *url.URL, pattern string) bool {
	scheme, rest, ok := strings.Cut(pattern, "://")
	if !ok || scheme != target.Scheme {
		return false
	}
	separator := strings.IndexByte(rest, '/')
	hostPort, pathPrefix := rest, ""
	if separator >= 0 {
		hostPort, pathPrefix = rest[:separator], rest[separator:]
	}
	targetPath := target.EscapedPath()
	if targetPath == "" {
		targetPath = "/"
	}
	if pathPrefix != "" && !strings.HasPrefix(targetPath, pathPrefix) {
		return false
	}
	hostPattern, portPattern, ok := splitPatternHostPort(hostPort)
	if !ok {
		return false
	}
	host := strings.ToLower(target.Hostname())
	patternHost := strings.ToLower(hostPattern)
	hostMatches := patternHost == "*"
	if strings.HasPrefix(patternHost, ".") {
		hostMatches = host == strings.TrimPrefix(patternHost, ".")
	} else if patternHost != "*" {
		hostMatches = host == patternHost || strings.HasSuffix(host, "."+patternHost)
	}
	if !hostMatches {
		return false
	}
	if portPattern == "" || portPattern == "*" {
		return true
	}
	targetPort := target.Port()
	if targetPort == "" {
		if target.Scheme == "https" {
			targetPort = "443"
		} else {
			targetPort = "80"
		}
	}
	return targetPort == portPattern
}
