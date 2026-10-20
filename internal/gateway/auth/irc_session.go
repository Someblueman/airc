package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

// IRCServer owns a provisioned TLS server profile and private SASL credentials.
// It is a connection prerequisite; registration readiness grants no AIRC identity.
// Close must follow all joined Connect/session users; it clears credential memory.
type IRCServer struct {
	address, nickname, account string
	security                   *tls.Config
	password                   []byte
	network                    string
	freshness                  time.Duration
	routes                     map[string]bridgeRoute // lowercase #channel
	authors                    *BridgeAuthors
	keepalive                  time.Duration
	keepaliveTimeout           time.Duration
}

// An established session sends PING after this much silence and closes only if
// the server then stays silent for the timeout.
const (
	ircKeepaliveInterval = 60 * time.Second
	ircKeepaliveTimeout  = 30 * time.Second
)

func NewIRCServer(c config.Config, b config.IRCBridge) (*IRCServer, error) {
	if c.ValidateIRCBridge(b) != nil {
		return nil, ErrCredential
	}
	s := &IRCServer{address: b.Address, nickname: b.Nickname, account: b.Account, security: &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}}
	ready := false
	defer func() {
		if !ready {
			s.Close()
		}
	}()
	s.network = b.ID
	s.freshness = time.Duration(b.FreshnessSeconds) * time.Second
	s.keepalive, s.keepaliveTimeout = ircKeepaliveInterval, ircKeepaliveTimeout
	s.routes = make(map[string]bridgeRoute, len(b.Routes))
	for _, r := range b.Routes {
		route, ok := newBridgeRoute(r)
		if !ok {
			return nil, ErrCredential
		}
		s.routes[strings.ToLower(r.Room)] = route
	}
	var err error
	s.authors, err = NewBridgeAuthors(b.ID, config.BridgeGuestPrefix(b.GuestPrefix, config.DefaultIRCGuestPrefix), "", b.GuestsAllowed(), b.Accounts)
	if err != nil {
		return nil, ErrCredential
	}
	if b.TLSCAFile != "" {
		s.security.RootCAs, err = ReadTrustRoots(b.TLSCAFile)
		if err != nil {
			return nil, ErrCredential
		}
	}
	s.password, err = readIRCPassword(b.PasswordFile)
	if err != nil {
		return nil, ErrCredential
	}
	registration, err := ircwire.NewRegistration(s.nickname, s.account, s.password)
	if err != nil {
		s.Close()
		return nil, ErrCredential
	}
	registration.Close()
	ready = true
	return s, nil
}
func (s *IRCServer) Close() {
	if s != nil {
		clear(s.password)
		s.password = nil
	}
}

func readIRCPassword(path string) ([]byte, error) {
	f, err := openPrivate(path)
	if err != nil {
		return nil, ErrCredential
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(data) == 0 || len(data) > 1024 {
		clear(data)
		return nil, ErrCredential
	}
	return data, nil
}

// IRCSession owns its verified client TLS connection and registration state.
// It must be closed after the caller finishes serial use; cancellation closes
// the socket immediately and Close joins the cancellation callback.
type IRCSession struct {
	conn         *tls.Conn
	server       *IRCServer
	ctx          context.Context
	reader       *ircwire.Reader
	registration *ircwire.Registration
	stop         func() bool
	closed       chan struct{}
	once         sync.Once
}

func (s *IRCSession) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		_ = s.conn.Close()
		if !s.stop() {
			<-s.closed
		}
		s.registration.Close()
	})
}

// IRCSetupBudget bounds Connect, and then JoinRooms; a variable only so tests
// can shorten it.
var IRCSetupBudget = 3 * time.Second

// Connect uses one IRCSetupBudget for dial, TLS, CAP, SASL and welcome.
// Certificate/name verification cannot be bypassed through caller TLS options.
func (s *IRCServer) Connect(parent context.Context) (*IRCSession, error) {
	if s == nil || len(s.password) == 0 {
		return nil, ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, IRCSetupBudget)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
	if err != nil {
		return nil, ErrAuthentication
	}
	registration, err := ircwire.NewRegistration(s.nickname, s.account, s.password)
	if err != nil {
		_ = raw.Close()
		return nil, ErrAuthentication
	}
	conn := tls.Client(raw, s.security.Clone())
	session := &IRCSession{conn: conn, server: s, ctx: parent, reader: ircwire.NewReader(conn), registration: registration, closed: make(chan struct{})}
	session.stop = context.AfterFunc(parent, func() { _ = conn.Close(); close(session.closed) })
	success := false
	defer func() {
		if !success {
			session.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil || conn.HandshakeContext(ctx) != nil {
		return nil, ErrAuthentication
	}
	state := conn.ConnectionState()
	if !state.HandshakeComplete || state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 {
		return nil, ErrAuthentication
	}
	wires, err := registration.Start()
	if err != nil || session.write(wires) != nil {
		return nil, ErrAuthentication
	}
	for !registration.Ready() {
		command, err := session.reader.Read()
		if err != nil {
			return nil, ErrAuthentication
		}
		wires, err = registration.Step(command)
		if err != nil || session.write(wires) != nil {
			return nil, ErrAuthentication
		}
	}
	if ctx.Err() != nil || parent.Err() != nil {
		return nil, ErrAuthentication
	}
	success = true
	return session, nil
}
func (s *IRCSession) writeWithin(wires [][]byte) error {
	if len(wires) == 0 {
		return nil
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		for _, wire := range wires {
			clear(wire)
		}
		return err
	}
	return s.write(wires)
}

func (s *IRCSession) write(wires [][]byte) error {
	defer func() {
		for _, wire := range wires {
			clear(wire)
		}
	}()
	for _, wire := range wires {
		if _, err := io.Copy(s.conn, bytes.NewReader(wire)); err != nil {
			return err
		}
	}
	return nil
}

// WriteReadResponse sends a read result to the requester alone, as plain
// NOTICE lines "AIRC-RESULT/1 <id> <i>/<n> <json part>"; joining the parts
// in order gives the result JSON. Nothing is written to the channel.
func (s *IRCSession) WriteReadResponse(proof VerifiedIRCEvent, payload []byte, now time.Time) error {
	if s == nil || s.server == nil || s.ctx == nil || s.ctx.Err() != nil || !proof.Valid() || proof.server != s.server || now.Before(proof.occurred) {
		return ErrAuthentication
	}
	if len(payload) > MaxIRCResponseBytes {
		return ErrResponseCapacity
	}
	lines, err := ircwire.PlainResponseLines(proof.nickname, proof.RequestID()[:16], string(payload))
	if err != nil || len(lines) == 0 {
		return ErrAuthentication
	}
	deadline := time.Now().Add(5 * time.Second)
	if s.conn.SetWriteDeadline(deadline) != nil {
		s.Close()
		return ErrAuthentication
	}
	defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	if s.ctx.Err() != nil || s.write(lines) != nil {
		s.Close()
		return ErrAuthentication
	}
	return nil
}

// MaxIRCResponseBytes bounds a read result sent as NOTICE lines; a larger one
// is answered with capacity_exceeded (ask for a smaller limit).
const MaxIRCResponseBytes = 16 << 10
