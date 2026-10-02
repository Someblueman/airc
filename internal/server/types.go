package server

import (
	"crypto/tls"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

const (
	maxConnections       = 1024
	maxOutboundQueue     = 4096
	defaultOutboundQueue = 2048
	maxHistoryMessages   = 10000
	maxChannelsPerClient = 64
	maxTotalChannels     = 1024
)

type Config struct {
	TLSConfig           *tls.Config
	RegistrationTimeout time.Duration
	MaxConnections      int
	MaxMessageSize      int
	OutboundQueue       int
	HistoryLimit        int
	ReadTimeout         time.Duration
	PingInterval        time.Duration
	Logger              *slog.Logger
}

type Client struct {
	ID          string    `json:"id"`
	Nick        string    `json:"nick"`
	Username    string    `json:"username"`
	RealName    string    `json:"real_name"`
	ConnectedAt time.Time `json:"connected_at"`
}

type Message struct {
	protocol.ChatMetadata
	ID        string    `json:"id"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Reaction  string    `json:"reaction,omitempty"`
	Seq       uint64    `json:"seq"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Body      string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type Server struct {
	cfg              Config
	logger           *slog.Logger
	mu               sync.Mutex
	messageMu        sync.Mutex // serializes posts while mu is released for history I/O
	clients          map[string]*session
	nicks            map[string]*session
	channels         map[string]map[string]*session
	watchers         map[string]map[string]*session
	history          historyRing
	topics           map[string]topic // channel headers; independent of who is connected
	topicsAt         string           // file the topics are saved to, if any
	directory        map[string]protocol.AgentCard
	profilesAt       string
	accessEnabled    bool
	accessHash       [32]byte
	adminEnabled     bool
	adminHash        [32]byte
	moderation       map[string]protocol.ModerationRule
	moderationAt     string
	accounts         map[string]account
	accountsAt       string
	chat             chatState
	chatAt           string
	slowPosts        map[string]time.Time
	signals          map[string]protocol.ChatEntry
	signalTimes      map[string]time.Time
	seq              uint64
	histFile         *os.File
	persistenceError string
	listener         net.Listener
	closed           chan struct{}
	wg               sync.WaitGroup
	closing          atomic.Bool
}

type session struct {
	server         *Server
	conn           net.Conn
	out            chan string
	done           chan struct{}
	closeOnce      sync.Once
	client         Client
	access         bool
	registered     bool
	capNegotiating bool
	saslEnabled    bool
	saslStarted    bool
	saslBuffer     string
	silentNotice   bool
	away           string
	monitoring     map[string]string
	observer       bool
	ephemeral      bool
	admin          bool
	accountID      string
	authNick       string
	quitReason     string
	channels       map[string]struct{}
	watching       map[string]struct{}
	lastPong       atomic.Int64
}

// hidden sessions never appear in WHO, NAMES or AGENTS.
func (s *session) hidden() bool { return s.observer || s.ephemeral }

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
