package server

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func New(cfg Config) *Server {
	if cfg.MaxConnections <= 0 || cfg.MaxConnections > maxConnections {
		cfg.MaxConnections = 512
	}
	if cfg.MaxMessageSize <= 0 || cfg.MaxMessageSize > 4096 {
		cfg.MaxMessageSize = 4096
	}
	if cfg.OutboundQueue <= 0 || cfg.OutboundQueue > maxOutboundQueue {
		cfg.OutboundQueue = defaultOutboundQueue
	}
	if cfg.OutboundBytes <= 0 || cfg.OutboundBytes > 16<<20 {
		cfg.OutboundBytes = 2 << 20
	}
	if cfg.HistoryLimit < 0 {
		cfg.HistoryLimit = 0
	}
	if cfg.HistoryLimit > maxHistoryMessages {
		cfg.HistoryLimit = maxHistoryMessages
	}
	if cfg.MaxPendingPerAddress <= 0 {
		cfg.MaxPendingPerAddress = max(4, cfg.MaxConnections/4)
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 2 * time.Minute
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.RegistrationTimeout <= 0 {
		cfg.RegistrationTimeout = 10 * time.Second
	}
	if cfg.TLSConfig != nil {
		cfg.TLSConfig = cfg.TLSConfig.Clone()
		cfg.TLSConfig.MinVersion = tls.VersionTLS13
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Server{
		chat: newChatState(), slowPosts: map[string]time.Time{}, signals: map[string]activitySignal{},
		cfg: cfg, logger: logger, clients: make(map[string]*session), pending: make(map[string]int), registrations: make(map[string][]time.Time),
		nicks: make(map[string]*session), channels: make(map[string]map[string]*session), watchers: make(map[string]map[string]*session),
		history: newHistory(cfg.HistoryLimit), topics: make(map[string]topic), directory: make(map[string]protocol.AgentCard), closed: make(chan struct{}),
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
		conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("unix socket is already in use: %s", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return nil, fmt.Errorf("check existing unix socket: %w", dialErr)
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
	if s.closing.Load() {
		s.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	if s.listener != nil {
		s.mu.Unlock()
		return errors.New("server is already serving")
	}
	if err := s.validateListenerLocked(listener); err != nil {
		s.mu.Unlock()
		_ = listener.Close()
		return err
	}
	if s.cfg.TLSConfig != nil {
		listener = tls.NewListener(listener, s.cfg.TLSConfig)
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
			// Descriptor exhaustion clears when clients disconnect; keep serving.
			if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ECONNABORTED) {
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
	if unixPath != "" {
		defer os.Remove(unixPath)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(listener) }()
	select {
	case err := <-serveDone:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := s.Shutdown(shutdownCtx); shutdownErr != nil {
			return errors.Join(err, shutdownErr)
		}
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
	// A post holds messageMu across its history write. If that write stalls,
	// still stop accepting and disconnect clients within the caller's deadline;
	// the history file is left to the writer that owns it.
	ordered := s.lockMessages(ctx)
	s.mu.Lock()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for _, client := range s.clients {
		client.close()
	}
	if ordered && s.histFile != nil {
		_ = s.histFile.Close()
		s.histFile = nil
	}
	s.mu.Unlock()
	if ordered {
		s.messageMu.Unlock()
	}
	go func() {
		s.wg.Wait()
		close(s.closed)
	}()
	select {
	case <-s.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) lockMessages(ctx context.Context) bool {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !s.messageMu.TryLock() {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
	return true
}

func (s *Server) accept(conn net.Conn) {
	pendingKey := pendingAddress(conn.RemoteAddr())
	s.mu.Lock()
	if s.closing.Load() || len(s.clients) >= s.cfg.MaxConnections {
		s.mu.Unlock()
		refuse(conn)
		return
	}
	if pendingKey != "" && s.pending[pendingKey] >= s.cfg.MaxPendingPerAddress {
		s.mu.Unlock()
		s.logger.Debug("client_refused", "remote", conn.RemoteAddr().String(), "reason", "too many unregistered connections from address")
		refuse(conn)
		return
	}
	if pendingKey != "" {
		s.pending[pendingKey]++
	}
	id := newID()
	client := &session{
		server: s, conn: conn, out: make(chan string, s.cfg.OutboundQueue), done: make(chan struct{}), overload: make(chan struct{}, 1), finish: make(chan struct{}), pendingKey: pendingKey, remoteKey: pendingKey,
		client: Client{ID: id, ConnectedAt: time.Now().UTC()}, channels: make(map[string]struct{}), watching: make(map[string]struct{}),
	}
	client.lastPong.Store(time.Now().UnixNano())
	s.clients[id] = client
	s.wg.Add(1)
	s.logger.Info("client_connected", "id", id, "remote", conn.RemoteAddr().String())
	s.mu.Unlock()
	go client.writeLoop()
	go client.readLoop()
}

// refuse turns a connection away. TLS peers have not handshaken, so they are
// closed without a plaintext line.
func refuse(conn net.Conn) {
	if _, secure := conn.(*tls.Conn); secure {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
	_, _ = conn.Write([]byte(":server ERROR :server is full or shutting down\r\n"))
	_ = conn.Close()
}

// pendingAddress names the remote peer whose unregistered connections are
// capped: TCP peers that are not loopback, with IPv6 grouped by /64 so one
// allocation cannot multiply its quota. Unix sockets and loopback are exempt.
func pendingAddress(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp.IP == nil || tcp.IP.IsLoopback() {
		return ""
	}
	if v4 := tcp.IP.To4(); v4 != nil {
		return v4.String()
	}
	return tcp.IP.Mask(net.CIDRMask(64, 128)).String()
}

// releasePendingLocked stops counting a connection against its address once it
// has registered or gone away.
func (s *Server) releasePendingLocked(client *session) {
	if client.pendingKey == "" {
		return
	}
	if s.pending[client.pendingKey]--; s.pending[client.pendingKey] <= 0 {
		delete(s.pending, client.pendingKey)
	}
	client.pendingKey = ""
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
	s.releasePendingLocked(client)
	if client.quitReason != "" {
		reason = client.quitReason
	}
	delete(s.clients, client.client.ID)
	if s.nicks[nickKey(client.client.Nick)] == client {
		delete(s.nicks, nickKey(client.client.Nick))
	}
	if client.registered {
		if !client.hidden() {
			s.monitorChangedLocked(client, false)
			quitLine := fmt.Sprintf(":%s!%s@localhost QUIT :%s\r\n", client.client.Nick, client.client.Username, reason)
			for channel := range client.channels {
				s.broadcastChannelLocked(channel, quitLine)
				dropMember(s.channels, channel, client.client.ID)
			}
		}
	}
	for channel := range client.watching {
		dropMember(s.watchers, channel, client.client.ID)
	}
	s.logger.Info("client_disconnected", "id", client.client.ID, "nick", client.client.Nick, "reason", reason)
	s.mu.Unlock()
	s.wg.Done()
}

// dropMember removes a session from one channel or watch group, and the group
// itself once it is empty.
func dropMember(groups map[string]map[string]*session, key, id string) {
	delete(groups[key], id)
	if len(groups[key]) == 0 {
		delete(groups, key)
	}
}
