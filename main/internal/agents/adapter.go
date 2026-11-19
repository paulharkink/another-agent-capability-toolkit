package agents

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func validate(r Registration) error {
	if r.Name == "" || strings.HasPrefix(r.Name, "-") || strings.ContainsAny(r.Name, "\r\n\x00") {
		return errors.New("invalid MCP registration name")
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("MCP registration requires an HTTP(S) URL")
	}
	if r.TimeoutMS < 0 {
		return errors.New("MCP timeout must not be negative")
	}
	for name, value := range r.Headers {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n\x00:") {
			return errors.New("MCP request header has an invalid name")
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("MCP request header %q has an invalid value", name)
		}
	}
	return nil
}
