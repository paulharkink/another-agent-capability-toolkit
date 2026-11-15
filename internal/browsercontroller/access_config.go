package browsercontroller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxAccessConfigBytes = 64 << 10
	AccessConfigEnv      = "AACT_BROWSER_ACCESS_CONFIG_JSON"
	StartupURLEnv        = "AACT_BROWSER_STARTUP_URL"
)

type browserAccessConfig struct {
	AllowedURLs      []string          `json:"allowed_urls"`
	BlockedURLs      []string          `json:"blocked_urls"`
	PopupAllowedURLs []string          `json:"popup_allowed_urls"`
	AutoAllowPopups  bool              `json:"auto_allow_popups"`
	TabLimits        *browserTabLimits `json:"tab_limits"`
}

type browserTabLimits struct {
	MaxTabs         *int                 `json:"max_tabs"`
	HardMaxTabs     *int                 `json:"hard_max_tabs"`
	PerPattern      []browserTabRule     `json:"per_pattern"`
	PopupExemptions []browserPopupExempt `json:"popup_exemptions"`
}

type browserTabRule struct {
	Pattern string `json:"pattern"`
	MaxTabs int    `json:"max_tabs"`
}

type browserPopupExempt struct {
	Pattern      string `json:"pattern"`
	MaxTabs      int    `json:"max_tabs"`
	GraceSeconds int    `json:"grace_seconds"`
}

var browserHostLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidateAccessConfig checks the generic browser authentication helper image policy envelope before
// any controller or Docker operation. An empty string keeps the image default.
func ValidateAccessConfig(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > maxAccessConfigBytes || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return errors.New("browser access configuration must be a JSON object no larger than 64 KiB")
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil || containsAccessConfigNull(value) {
		return errors.New("browser access configuration is invalid")
	}
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	var config browserAccessConfig
	if err := dec.Decode(&config); err != nil {
		return errors.New("browser access configuration is invalid")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("browser access configuration contains trailing JSON")
	}
	if len(config.AllowedURLs) > 100 || len(config.BlockedURLs) > 100 || len(config.PopupAllowedURLs) > 100 {
		return errors.New("browser URL policy contains too many patterns")
	}
	for _, pattern := range config.AllowedURLs {
		if !validBrowserURLPattern(pattern) {
			return errors.New("browser access configuration contains an invalid allowed_urls pattern")
		}
	}
	for _, pattern := range config.PopupAllowedURLs {
		if !validBrowserURLPattern(pattern) {
			return errors.New("browser access configuration contains an invalid popup_allowed_urls pattern")
		}
	}
	for _, pattern := range config.BlockedURLs {
		if !validBrowserURLPattern(pattern) {
			return errors.New("browser access configuration contains an invalid blocked_urls pattern")
		}
	}
	if config.AutoAllowPopups && len(config.AllowedURLs) == 0 {
		return errors.New("auto_allow_popups requires allowed_urls")
	}
	if config.TabLimits == nil {
		return nil
	}
	limits := config.TabLimits
	if limits.MaxTabs == nil || limits.HardMaxTabs == nil || *limits.MaxTabs < 1 || *limits.MaxTabs > 100 || *limits.HardMaxTabs < *limits.MaxTabs || *limits.HardMaxTabs > 100 {
		return errors.New("browser tab limits require max_tabs 1..100 and hard_max_tabs between max_tabs and 100")
	}
	if len(limits.PerPattern) > 100 || len(limits.PopupExemptions) > 100 {
		return errors.New("browser tab limits contain too many rules")
	}
	for _, rule := range limits.PerPattern {
		if !validBrowserURLPattern(rule.Pattern) || rule.MaxTabs < 1 || rule.MaxTabs > *limits.HardMaxTabs {
			return errors.New("browser tab limits contain an invalid per_pattern rule")
		}
	}
	for _, rule := range limits.PopupExemptions {
		if !validBrowserURLPattern(rule.Pattern) || rule.MaxTabs < 1 || rule.MaxTabs > *limits.HardMaxTabs || rule.GraceSeconds < 0 || rule.GraceSeconds > 30 {
			return errors.New("browser tab limits contain an invalid popup_exemptions rule")
		}
	}
	return nil
}

// ApplyDefaultPopupAllowedURLs adds package-declared popup initiators only
// when the user has not supplied popup_allowed_urls. An explicit empty list
// is preserved so a user can disable popups for a package.
func ApplyDefaultPopupAllowedURLs(raw string, defaults []string) (string, error) {
	if len(defaults) == 0 {
		return raw, nil
	}
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if err := ValidateAccessConfig(raw); err != nil {
		return "", err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
		return "", errors.New("browser access configuration is invalid")
	}
	if _, explicitlySet := config["popup_allowed_urls"]; explicitlySet {
		return raw, nil
	}
	encodedDefaults, err := json.Marshal(defaults)
	if err != nil {
		return "", errors.New("browser popup defaults are invalid")
	}
	config["popup_allowed_urls"] = encodedDefaults
	merged, err := json.Marshal(config)
	if err != nil {
		return "", errors.New("browser access configuration is invalid")
	}
	result := string(merged)
	if err := ValidateAccessConfig(result); err != nil {
		return "", err
	}
	return result, nil
}

func validBrowserURLPattern(pattern string) bool {
	if len(pattern) == 0 || len(pattern) > 2048 || strings.ContainsAny(pattern, "?#@\\") || hasPatternWhitespace(pattern) {
		return false
	}
	scheme, rest, ok := strings.Cut(pattern, "://")
	if !ok || (scheme != "http" && scheme != "https") {
		return false
	}
	separator := strings.IndexByte(rest, '/')
	hostPort, path := rest, ""
	if separator >= 0 {
		hostPort, path = rest[:separator], rest[separator:]
	}
	if path != "" && (!strings.HasPrefix(path, "/") || strings.Contains(path, "*")) {
		return false
	}
	host, port, ok := splitPatternHostPort(hostPort)
	if !ok || (port != "" && !validBrowserPatternPort(port)) {
		return false
	}
	if host == "*" {
		return true
	}
	if strings.HasPrefix(host, ".") {
		return validDomain(strings.TrimPrefix(host, "."))
	}
	return validDomain(host) || net.ParseIP(host) != nil
}

func splitPatternHostPort(value string) (string, string, bool) {
	if strings.HasPrefix(value, "[") {
		close := strings.IndexByte(value, ']')
		if close < 0 {
			return "", "", false
		}
		host := value[1:close]
		if net.ParseIP(host) == nil || !strings.Contains(host, ":") {
			return "", "", false
		}
		suffix := value[close+1:]
		if suffix == "" {
			return host, "", true
		}
		if !strings.HasPrefix(suffix, ":") || strings.Contains(suffix[1:], ":") {
			return "", "", false
		}
		return host, suffix[1:], true
	}
	host, port, hasPort := strings.Cut(value, ":")
	if strings.Contains(host, ":") || (hasPort && strings.Contains(port, ":")) {
		return "", "", false
	}
	if !hasPort {
		return host, "", true
	}
	return host, port, true
}

func validBrowserPatternPort(value string) bool {
	if value == "*" {
		return true
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= 1 && number <= 65535
}

func containsAccessConfigNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		for _, item := range typed {
			if containsAccessConfigNull(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsAccessConfigNull(item) {
				return true
			}
		}
	}
	return false
}

func validDomain(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !browserHostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func hasPatternWhitespace(value string) bool {
	for _, r := range value {
		if r <= 0x20 {
			return true
		}
	}
	return false
}
