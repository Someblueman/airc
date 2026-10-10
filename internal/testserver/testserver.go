// Package testserver starts throwaway daemons for tests outside internal/server.
package testserver

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

// Start serves cfg on a loopback port until the test ends and returns its
// address. A nil Logger discards logs. setup, when given, runs before the
// server accepts connections (accounts, admin tokens, state restores).
func Start(t testing.TB, cfg server.Config, setup func(*server.Server) error) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logger == nil {
		cfg.Logger = Logger()
	}
	srv := server.New(cfg)
	if setup != nil {
		if err := setup(srv); err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return listener.Addr().String()
}

// Logger discards everything; tests only care about the wire.
func Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
