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
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

// IRCServer owns a provisioned TLS server profile and private SASL credentials.
// It is a connection prerequisite; registration readiness grants no AIRC identity.
// Close must follow all joined Connect/session users; it clears credential memory.
type IRCServer struct {
	address, nickname, account string
	realm                      string
	security                   *tls.Config
	password                   []byte
	network                    string
	verifier                   *Verifier
	freshness                  time.Duration
	grants                     map[[2]string]ircGrant
	responseSealer             *ResponseSealer
	keepalive                  time.Duration
	keepaliveTimeout           time.Duration
}

// An established session sends PING after this much silence and closes only if
// the server then stays silent for the timeout.
const (
	ircKeepaliveInterval = 60 * time.Second
	ircKeepaliveTimeout  = 30 * time.Second
)

func NewIRCServer(v *Verifier, c config.Config, b config.IRCBridge) (*IRCServer, error) {
	if v == nil || c.ValidateIRCBridge(b) != nil {
		return nil, ErrCredential
	}
	s := &IRCServer{address: b.Address, nickname: b.Nickname, account: b.Account, realm: c.Realm, verifier: v, security: &tls.Config{ServerName: b.TLSServerName, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}}
	ready := false
	defer func() {
		if !ready {
			s.Close()
		}
	}()
	s.network = b.ID
	s.freshness = time.Duration(b.FreshnessSeconds) * time.Second
	s.keepalive, s.keepaliveTimeout = ircKeepaliveInterval, ircKeepaliveTimeout
	s.grants = make(map[[2]string]ircGrant, len(b.Routes))
	hasRead := false
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
		target := route.Target
		if target.Normalize() != nil {
			return nil, ErrCredential
		}
		for _, op := range route.Operations {
			if !slices.Contains(identity.principal.operations, op) || !slices.Contains(identity.principal.targets, target) {
				return nil, ErrCredential
			}
			hasRead = hasRead || isIRCReadOperation(op)
		}
		grant := ircGrant{identity: identity, target: target, operations: slices.Clone(route.Operations)}
		for _, op := range route.Operations {
			if !isIRCReadOperation(op) {
				continue
			}
			key, err := ReadResponseRecipientKey(route.ResponseRecipientPublicKeyFile)
			if err != nil {
				return nil, ErrCredential
			}
			grant.responseRecipientKeyID = route.ResponseRecipientKeyID
			grant.responseRecipientPublicKey = key
			break
		}
		s.grants[[2]string{route.Account, strings.ToLower(route.Room)}] = grant
	}
	if hasRead {
		if c.ResponseSigningKey == nil {
			return nil, ErrCredential
		}
		signingKey, err := ReadResponseSigningKey(c.ResponseSigningKey.PrivateKeyFile)
		if err != nil {
			return nil, ErrCredential
		}
		s.responseSealer, err = NewResponseSealer(c.ResponseSigningKey.ID, signingKey)
		clear(signingKey)
		if err != nil {
			return nil, ErrCredential
		}
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
	ready = true
	return s, nil
}
func (s *IRCServer) Close() {
	if s != nil {
		clear(s.password)
		s.password = nil
		if s.responseSealer != nil {
			clear(s.responseSealer.signingKey)
			s.responseSealer.signingKey = nil
			s.responseSealer = nil
		}
	}
}

func isIRCReadOperation(op core.Operation) bool {
	return op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

func IRCReadResponseRoute(network, account, room string, target core.Target) string {
	targetCopy := target
	if !config.IRCAccount(account) || !config.IRCRoom(room) || targetCopy.Normalize() != nil {
		return ""
	}
	return ircDigest("airc-irc-read-response-v1", network, account, strings.ToLower(room), targetCopy.Kind, targetCopy.Name)
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

// WriteReadResponse seals the complete native result before writing any IRC
// lines. A protected token may be delivered in fragments, but no plaintext is
// ever sent to the room.
func (s *IRCSession) WriteReadResponse(proof VerifiedIRCEvent, payload []byte, now time.Time) error {
	if s == nil || s.server == nil || s.ctx == nil || s.ctx.Err() != nil || !proof.IsRead() || proof.server != s.server ||
		s.server.verifier == nil || !s.server.verifier.OwnsIdentity(proof.identity) || now.Before(proof.occurred) || !now.Before(proof.until) || !time.Now().Before(proof.until) {
		return ErrAuthentication
	}
	request, err := proof.ReadRequest()
	if err != nil {
		return ErrAuthentication
	}
	grant, ok := s.server.grants[[2]string{proof.account, proof.room}]
	if !ok || grant.identity.principal != proof.identity.principal || grant.responseRecipientPublicKey == nil || grant.responseRecipientKeyID == "" ||
		s.server.responseSealer == nil || !slices.Contains(grant.operations, request.Operation) {
		return ErrAuthentication
	}
	route := IRCReadResponseRoute(proof.network, proof.account, proof.room, proof.target)
	if route == "" {
		return ErrAuthentication
	}
	scope := ResponseScope{Realm: s.server.realm, Principal: proof.identity.ID(), Binding: proof.identity.Binding(), Route: route}
	token, err := s.server.responseSealer.Seal(scope, grant.responseRecipientKeyID, grant.responseRecipientPublicKey, payload, now)
	if err != nil {
		return err
	}
	lines, err := ircwire.ResponseLines(proof.nickname, proof.RequestID(), token)
	if err != nil || len(lines) == 0 || len(lines) > ircwire.MaxResponseChunks {
		return ErrAuthentication
	}
	defer func() {
		for _, line := range lines {
			clear(line)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	if proof.until.Before(deadline) {
		deadline = proof.until
	}
	if !time.Now().Before(deadline) || s.conn.SetWriteDeadline(deadline) != nil {
		s.Close()
		return ErrAuthentication
	}
	defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	for index, line := range lines {
		if s.ctx.Err() != nil || s.write([][]byte{line}) != nil {
			s.Close()
			return ErrAuthentication
		}
		if index == len(lines)-1 {
			break
		}
		wait := 100 * time.Millisecond
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		if wait <= 0 {
			s.Close()
			return ErrAuthentication
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			timer.Stop()
			s.Close()
			return ErrAuthentication
		}
	}
	return nil
}
