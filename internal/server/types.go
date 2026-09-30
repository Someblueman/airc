package server

import (
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	MaxConnections int
	MaxMessageSize int
	OutboundQueue  int
	HistoryLimit   int
	ReadTimeout    time.Duration
	PingInterval   time.Duration
	Logger         *slog.Logger
}

type Client struct {
	ID          string    `json:"id"`
	Nick        string    `json:"nick"`
	Username    string    `json:"username"`
	RealName    string    `json:"real_name"`
	ConnectedAt time.Time `json:"connected_at"`
}

type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Body      string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type Server struct {
	cfg      Config
	logger   *slog.Logger
	mu       sync.Mutex
	clients  map[string]*session
	nicks    map[string]*session
	channels map[string]map[string]*session
	history  historyRing
	listener net.Listener
	closed   chan struct{}
	wg       sync.WaitGroup
	closing  atomic.Bool
}

type session struct {
	server     *Server
	conn       net.Conn
	out        chan string
	done       chan struct{}
	closeOnce  sync.Once
	client     Client
	registered bool
	channels   map[string]struct{}
	lastPong   atomic.Int64
}

func (s *session) close() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

func (s *session) enqueue(line string) bool {
	select {
	case <-s.done:
		return false
	case s.out <- line:
		return true
	default:
		s.close()
		s.server.logger.Warn("client_disconnected", "id", s.client.ID, "reason", "outbound queue full")
		return false
	}
}
