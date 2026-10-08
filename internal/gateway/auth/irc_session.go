package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"slices"
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
	grants                     map[[2]string]ircGrant
}

func NewIRCServer(v *Verifier, c config.Config, b config.IRCBridge) (*IRCServer, error) {
	if v == nil || c.ValidateIRCBridge(b) != nil {
		return nil, ErrCredential
	}
	s := &IRCServer{address: b.Address, nickname: b.Nickname, account: b.Account, security: &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}}
	s.network = b.ID
	s.freshness = time.Duration(b.FreshnessSeconds) * time.Second
	s.grants = make(map[[2]string]ircGrant, len(b.Routes))
	for _, route := range b.Routes {
		var identity Identity
		for i := range v.principals {
			candidate := Identity{&v.principals[i]}
			if candidate.ID() == route.Principal && v.OwnsIdentity(candidate) {
				identity = candidate
				break
			}
		}
		if identity.ID() == "" {
			return nil, ErrCredential
		}
		for _, op := range route.Operations {
			if !slices.Contains(identity.principal.operations, op) || !slices.Contains(identity.principal.targets, route.Target) {
				return nil, ErrCredential
			}
		}
		s.grants[[2]string{route.Account, strings.ToLower(route.Room)}] = ircGrant{identity: identity, target: route.Target, operations: slices.Clone(route.Operations)}
	}
	var err error
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

// Connect uses one three-second budget for dial, TLS, CAP, SASL and welcome.
// Certificate/name verification cannot be bypassed through caller TLS options.
func (s *IRCServer) Connect(parent context.Context) (*IRCSession, error) {
	if s == nil || len(s.password) == 0 {
		return nil, ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
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
