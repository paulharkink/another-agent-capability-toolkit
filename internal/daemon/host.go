package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Host composes HTTP APIs provided by services running inside the AACT daemon.
// The first hosted service is the shared-secret vault.
type Host struct {
	mux                   *http.ServeMux
	services              map[string]string
	routes                map[string]struct{}
	authenticate          func(string) (ClientIdentity, bool)
	requireAuthentication bool
}

type clientContextKey struct{}

// ClientIdentity describes the instance and container bound to a credential.
type ClientIdentity struct {
	Instance    string
	ContainerID string
}

// ClientFromContext returns the client authenticated by the daemon host.
func ClientFromContext(ctx context.Context) (ClientIdentity, bool) {
	client, ok := ctx.Value(clientContextKey{}).(ClientIdentity)
	return client, ok && client.Instance != ""
}

// NewAuthenticatedHost authenticates callers before routing to any service.
func NewAuthenticatedHost(authenticate func(string) (ClientIdentity, bool)) *Host {
	h := NewHost()
	h.authenticate = authenticate
	h.requireAuthentication = true
	return h
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
	if h.requireAuthentication {
		client, ok := ClientIdentity{}, false
		if h.authenticate != nil {
			client, ok = h.authenticate(r.Header.Get("Authorization"))
		}
		if !ok || client.Instance == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), clientContextKey{}, client))
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
