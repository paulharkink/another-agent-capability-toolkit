package vault

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

// Daemon runs the vault API on a caller-owned listener and shuts it down when
// the launch context ends. Its bearer tokens remain memory-only.
type Daemon struct {
	server *http.Server
	addr   net.Addr
	done   chan struct{}
	err    error
	once   sync.Once
}

func StartDaemon(ctx context.Context, listener net.Listener, handler http.Handler) (*Daemon, error) {
	if listener == nil {
		return nil, errors.New("vault daemon listener is required")
	}
	if handler == nil {
		return nil, errors.New("vault daemon handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d := &Daemon{server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}, addr: listener.Addr(), done: make(chan struct{})}
	go func() {
		serveErr := d.server.Serve(listener)
		if !errors.Is(serveErr, http.ErrServerClosed) {
			d.err = fmt.Errorf("serve vault API: %w", serveErr)
		}
		close(d.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = d.server.Shutdown(shutdownCtx)
		case <-d.done:
		}
	}()
	return d, nil
}

func (d *Daemon) URL() string {
	return (&url.URL{Scheme: "http", Host: d.addr.String()}).String()
}

func (d *Daemon) Wait() error {
	if d == nil {
		return nil
	}
	<-d.done
	return d.err
}

func (d *Daemon) Close() error {
	if d == nil {
		return nil
	}
	var closeErr error
	d.once.Do(func() { closeErr = d.server.Close() })
	if err := d.Wait(); closeErr == nil && !errors.Is(err, http.ErrServerClosed) {
		closeErr = err
	}
	return closeErr
}
