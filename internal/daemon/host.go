package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Host composes HTTP APIs provided by services running inside the AACT daemon.
// The first hosted service is the shared-secret vault.
type Host struct {
	mux      *http.ServeMux
	services map[string]string
	routes   map[string]struct{}
}

func NewHost() *Host {
	return &Host{mux: http.NewServeMux(), services: make(map[string]string), routes: make(map[string]struct{})}
}

func (h *Host) Register(name, pattern string, handler http.Handler) error {
	if h == nil || h.mux == nil {
		return errors.New("AACT daemon host is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("daemon service name is required")
	}
	if handler == nil {
		return fmt.Errorf("daemon service %q handler is required", name)
	}
	if !strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("daemon service %q route must start with /", name)
	}
	if _, exists := h.services[name]; exists {
		return fmt.Errorf("daemon service %q is already registered", name)
	}
	if _, exists := h.routes[pattern]; exists {
		return fmt.Errorf("daemon route %q is already registered", pattern)
	}
	if err := registerHTTPRoute(h.mux, pattern, handler); err != nil {
		return fmt.Errorf("register daemon service %q: %w", name, err)
	}
	h.services[name] = pattern
	h.routes[pattern] = struct{}{}
	return nil
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.mux == nil {
		http.Error(w, "AACT daemon is unavailable", http.StatusServiceUnavailable)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func registerHTTPRoute(mux *http.ServeMux, pattern string, handler http.Handler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("invalid or conflicting route %q", pattern)
		}
	}()
	mux.Handle(pattern, handler)
	return nil
}
