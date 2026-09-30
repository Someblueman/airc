package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func New(cfg Config) *Server {
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 128
	}
	if cfg.MaxMessageSize <= 0 || cfg.MaxMessageSize > 4096 {
		cfg.MaxMessageSize = 4096
	}
	if cfg.OutboundQueue <= 0 {
		cfg.OutboundQueue = 128
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 2 * time.Minute
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Server{
		cfg: cfg, logger: logger, clients: make(map[string]*session),
		nicks: make(map[string]*session), channels: make(map[string]map[string]*session),
		history: newHistory(cfg.HistoryLimit), closed: make(chan struct{}),
	}
}

func ListenTCP(address string) (net.Listener, error) {
	if address == "" {
		address = "127.0.0.1:6667"
	}
	return net.Listen("tcp", address)
}

func ListenUnix(path string) (net.Listener, error) {
	if path == "" {
		return nil, errors.New("unix socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect socket path: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on unix socket: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("set unix socket permissions: %w", err)
	}
	return listener, nil
}

// Serve accepts connections until the listener closes. Call Shutdown to close it.
func (s *Server) Serve(listener net.Listener) error {
	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		return errors.New("server is already serving")
	}
	s.listener = listener
	s.mu.Unlock()
	s.logger.Info("server_started", "address", listener.Addr().String())

	for {
		conn, err := listener.Accept()
		if err != nil {
			if s.closing.Load() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if temporary, ok := err.(net.Error); ok && temporary.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return fmt.Errorf("accept client: %w", err)
		}
		s.accept(conn)
	}
}

// Run creates the configured listener and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context, address, unixPath string) error {
	var listener net.Listener
	var err error
	if unixPath != "" {
		listener, err = ListenUnix(unixPath)
	} else {
		listener, err = ListenTCP(address)
	}
	if err != nil {
		return err
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(listener) }()
	select {
	case err := <-serveDone:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-serveDone
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.closing.Swap(true) {
		select {
		case <-s.closed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for _, client := range s.clients {
		client.close()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		close(s.closed)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) accept(conn net.Conn) {
	s.mu.Lock()
	if s.closing.Load() || len(s.clients) >= s.cfg.MaxConnections {
		s.mu.Unlock()
		_, _ = conn.Write([]byte(":server ERROR :server is full or shutting down\r\n"))
		_ = conn.Close()
		return
	}
	id := newID()
	client := &session{
		server: s, conn: conn, out: make(chan string, s.cfg.OutboundQueue), done: make(chan struct{}),
		client: Client{ID: id, ConnectedAt: time.Now().UTC()}, channels: make(map[string]struct{}),
	}
	client.lastPong.Store(time.Now().UnixNano())
	s.clients[id] = client
	s.wg.Add(1)
	s.logger.Info("client_connected", "id", id, "remote", conn.RemoteAddr().String())
	s.mu.Unlock()
	go client.writeLoop()
	go client.readLoop()
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func (s *Server) remove(client *session, reason string) {
	s.mu.Lock()
	if _, exists := s.clients[client.client.ID]; !exists {
		s.mu.Unlock()
		return
	}
	client.close()
	delete(s.clients, client.client.ID)
	if client.registered {
		delete(s.nicks, nickKey(client.client.Nick))
		quitLine := fmt.Sprintf(":%s!%s@localhost QUIT :%s\r\n", client.client.Nick, client.client.Username, reason)
		for channel := range client.channels {
			for id, member := range s.channels[channel] {
				if id != client.client.ID {
					member.enqueue(quitLine)
				}
			}
			delete(s.channels[channel], client.client.ID)
			if len(s.channels[channel]) == 0 {
				delete(s.channels, channel)
			}
		}
	}
	s.logger.Info("client_disconnected", "id", client.client.ID, "nick", client.client.Nick, "reason", reason)
	s.mu.Unlock()
	s.wg.Done()
}

func (s *Server) address() string { return strings.TrimSpace(s.listener.Addr().String()) }
