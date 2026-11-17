package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxBootstrapBytes = 1 << 20

type ServiceConfig struct {
	Name          string          `json:"name"`
	Configuration json.RawMessage `json:"configuration"`
}

// Bootstrap starts the configured services inside the AACT daemon process.
type Bootstrap struct {
	Services []ServiceConfig `json:"services"`
}

type ServiceRunner func(context.Context, json.RawMessage) error
type Registry map[string]ServiceRunner

func Run(ctx context.Context, in io.Reader, registry Registry) error {
	if in == nil {
		return errors.New("AACT daemon bootstrap input is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var bootstrap Bootstrap
	decoder := json.NewDecoder(io.LimitReader(in, maxBootstrapBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bootstrap); err != nil {
		return fmt.Errorf("read AACT daemon bootstrap: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after AACT daemon bootstrap")
		}
		return fmt.Errorf("read AACT daemon bootstrap trailer: %w", err)
	}
	if len(bootstrap.Services) == 0 {
		return errors.New("AACT daemon bootstrap has no services")
	}
	type configuredService struct {
		name string
		run  ServiceRunner
		cfg  json.RawMessage
	}
	services := make([]configuredService, 0, len(bootstrap.Services))
	seen := make(map[string]struct{}, len(bootstrap.Services))
	for _, item := range bootstrap.Services {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return errors.New("AACT daemon service name is required")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("AACT daemon service %q is configured more than once", name)
		}
		seen[name] = struct{}{}
		runner, exists := registry[name]
		if !exists || runner == nil {
			return fmt.Errorf("unknown daemon service %q", name)
		}
		configuration := append(json.RawMessage(nil), item.Configuration...)
		if len(configuration) == 0 {
			configuration = json.RawMessage("null")
		}
		if !json.Valid(configuration) {
			return fmt.Errorf("daemon service %q configuration is invalid JSON", name)
		}
		services = append(services, configuredService{name: name, run: runner, cfg: configuration})
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(services))
	for _, service := range services {
		service := service
		go func() {
			err := service.run(serviceCtx, service.cfg)
			results <- result{name: service.name, err: err}
		}()
	}
	var firstErr error
	received := 0
	select {
	case result := <-results:
		received++
		if result.err != nil {
			firstErr = fmt.Errorf("AACT daemon service %q: %w", result.name, result.err)
		}
		cancel()
	case <-ctx.Done():
		cancel()
	}
	shutdown := time.NewTimer(5 * time.Second)
	defer shutdown.Stop()
	for remaining := len(services) - received; remaining > 0; remaining-- {
		select {
		case result := <-results:
			if firstErr == nil && result.err != nil {
				firstErr = fmt.Errorf("AACT daemon service %q: %w", result.name, result.err)
			}
		case <-shutdown.C:
			return errors.Join(firstErr, errors.New("AACT daemon services did not stop promptly"))
		}
	}
	return firstErr
}
