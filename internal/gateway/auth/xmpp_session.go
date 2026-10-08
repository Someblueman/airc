package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

type XMPPSession struct {
	conn   *tls.Conn
	server *XMPPServer
	reader *xmppwire.Stream
	stop   func() bool
	closed chan struct{}
	once   sync.Once
}

func (s *XMPPSession) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		_ = s.conn.Close()
		if !s.stop() {
			<-s.closed
		}
	})
}

// Connect owns one three-second deadline for TCP, verified direct TLS 1.3,
// SASL PLAIN, stream restart and exact resource binding. It yields no event
// identity or AIRC authority before independently verified room membership.
func (s *XMPPServer) Connect(parent context.Context) (*XMPPSession, error) {
	if s == nil || len(s.password) == 0 || parent == nil {
		return nil, ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
	if err != nil {
		return nil, ErrAuthentication
	}
	conn := tls.Client(raw, s.security.Clone())
	x := &XMPPSession{conn: conn, server: s, reader: xmppwire.NewStream(conn), closed: make(chan struct{})}
	x.stop = context.AfterFunc(parent, func() { _ = conn.Close(); close(x.closed) })
	success := false
	defer func() {
		if !success {
			x.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil || conn.HandshakeContext(ctx) != nil {
		return nil, ErrAuthentication
	}
	state := conn.ConnectionState()
	if !state.HandshakeComplete || state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != "" && state.NegotiatedProtocol != "xmpp-client" {
		return nil, ErrAuthentication
	}
	_, domain, _ := strings.Cut(s.account, "@")
	h, _ := xmppwire.Header(s.account)
	if x.write(h) != nil {
		return nil, ErrAuthentication
	}
	if !x.open(domain) {
		return nil, ErrAuthentication
	}
	features, err := x.reader.Read()
	if err != nil {
		return nil, ErrAuthentication
	}
	w, err := xmppwire.Plain(features, s.account, s.password)
	if err != nil || x.write(w) != nil {
		return nil, ErrAuthentication
	}
	e, err := x.reader.Read()
	if err != nil || !xmppwire.Success(e) {
		return nil, ErrAuthentication
	}
	h, _ = xmppwire.Header(s.account)
	if x.reader.Restart() != nil || x.write(h) != nil || !x.open(domain) {
		return nil, ErrAuthentication
	}
	features, err = x.reader.Read()
	if err != nil {
		return nil, ErrAuthentication
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, ErrAuthentication
	}
	id := hex.EncodeToString(nonce[:])
	w, err = xmppwire.Bind(features, s.account, s.resource, id)
	if err != nil || x.write(w) != nil {
		return nil, ErrAuthentication
	}
	e, err = x.reader.Read()
	if err != nil || !xmppwire.Bound(e, s.account, s.resource, id) {
		return nil, ErrAuthentication
	}
	if ctx.Err() != nil || parent.Err() != nil || conn.SetDeadline(time.Time{}) != nil {
		return nil, ErrAuthentication
	}
	success = true
	return x, nil
}

func (x *XMPPSession) open(domain string) bool {
	e, err := x.reader.Open()
	if err != nil {
		return false
	}
	attrs := map[string]string{}
	for _, a := range e.Attributes {
		if a.Name.Space == "" {
			attrs[a.Name.Local] = a.Value
		}
	}
	return attrs["from"] == domain && attrs["version"] == "1.0" && len(attrs["id"]) > 0 && len(attrs["id"]) <= 256
}

func (x *XMPPSession) write(w []byte) error {
	defer clear(w)
	_, err := io.Copy(x.conn, bytes.NewReader(w))
	return err
}
