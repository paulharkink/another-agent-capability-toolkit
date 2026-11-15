package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Server runs a service handler on a listener managed by the AACT daemon.
type Server struct {
	server  *http.Server
	addr    net.Addr
	errs    chan error
	done    chan struct{}
	mu      sync.Mutex
	err     error
	closed  bool
	serves  sync.WaitGroup
	once    sync.Once
	stopErr error
}

func StartServer(ctx context.Context, listener net.Listener, handler http.Handler) (*Server, error) {
	if listener == nil {
		return nil, errors.New("daemon listener is required")
	}
	if handler == nil {
		return nil, errors.New("daemon service handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s := &Server{server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}, addr: listener.Addr(), errs: make(chan error, 8), done: make(chan struct{})}
	if err := s.AddListener(listener); err != nil {
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.Shutdown(shutdownCtx)
		case <-s.done:
		}
	}()
	return s, nil
}

// AddListener serves another listener through the same daemon handler. This
// supports host routes that need a second reachable address without giving a
// service ownership of a separate HTTP server.
func (s *Server) AddListener(listener net.Listener) error {
	if s == nil || listener == nil {
		return errors.New("daemon listener is required")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("daemon server is closed")
	}
	s.serves.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.serves.Done()
		serveErr := s.server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
			err := fmt.Errorf("serve daemon service: %w", serveErr)
			s.mu.Lock()
			if s.err == nil {
				s.err = err
			}
			s.mu.Unlock()
			select {
			case s.errs <- err:
			default:
			}
		}
	}()
	return nil
}

func (s *Server) Errors() <-chan error { return s.errs }

func (s *Server) URL() string {
	return (&url.URL{Scheme: "http", Host: s.addr.String()}).String()
}

func (s *Server) Wait() error {
	if s == nil {
		return nil
	}
	s.serves.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		closeErr = s.server.Close()
		s.mu.Lock()
		s.stopErr = closeErr
		s.mu.Unlock()
	})
	if err := s.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.mu.Lock()
		if s.stopErr == nil {
			s.stopErr = err
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopErr
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		err := s.server.Shutdown(ctx)
		s.mu.Lock()
		s.stopErr = err
		s.mu.Unlock()
	})
	if err := s.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.mu.Lock()
		if s.stopErr == nil {
			s.stopErr = err
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopErr
}
