package browsercontroller

import (
	"errors"
	"net"
	"net/url"
)

func validBrowserOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	host := parsed.Hostname()
	if host == "" || (net.ParseIP(host) == nil && !validDomain(host)) {
		return false
	}
	if port := parsed.Port(); port != "" {
		for _, r := range port {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func validateViewerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("viewer URL must be plain HTTP")
	}
	host := u.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("viewer URL must use loopback")
		}
	}
	if u.Port() == "" {
		return errors.New("viewer URL must include a port")
	}
	return nil
}
