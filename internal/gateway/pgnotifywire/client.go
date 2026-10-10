package pgnotifywire

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"net"
	"slices"
	"sync"
	"time"
)

const (
	startupTimeout   = 10 * time.Second
	maxFrameBytes    = 16 << 10
	maxStartupFrames = 64
)

type Session struct {
	conn          net.Conn
	channel       string
	readMu        sync.Mutex
	mu            sync.Mutex
	closed        bool
	closeErr      error
	parameters    map[string]string
	pending       [][]byte
	startupFrames int
}

func Connect(parent context.Context, address string, security *tls.Config, database, username, channel string) (*Session, error) {
	if parent == nil || security == nil || ValidateLocation(address, security.ServerName, database, username, channel) != nil || security.InsecureSkipVerify || len(security.Certificates) != 1 {
		return nil, ErrProtocol
	}
	configured := security.Certificates[0]
	now := time.Now()
	leaf, err := certificateLeaf(configured)
	if err != nil {
		return nil, ErrProtocol
	}
	if leaf.IsCA || leaf.Subject.CommonName != username || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return nil, ErrProtocol
	}
	if _, ok := leaf.PublicKey.(ed25519.PublicKey); !ok {
		return nil, ErrProtocol
	}
	privateKey, ok := configured.PrivateKey.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrProtocol
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	leafPublic, leafOK := leaf.PublicKey.(ed25519.PublicKey)
	if !ok || !leafOK || !bytes.Equal(publicKey, leafPublic) {
		return nil, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(parent, startupTimeout)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: startupTimeout, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, ErrProtocol
	}
	fail := func() (*Session, error) { _ = conn.Close(); return nil, ErrProtocol }
	interruptDone := make(chan struct{})
	stopInterrupt := context.AfterFunc(ctx, func() { _ = conn.Close(); close(interruptDone) })
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			if !stopInterrupt() {
				<-interruptDone
			}
		})
	}
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(startupTimeout)); err != nil {
		return fail()
	}
	if err := writeFull(conn, []byte{0, 0, 0, 8, 4, 210, 22, 47}); err != nil {
		return fail()
	}
	var reply [1]byte
	if _, err := readFull(conn, reply[:]); err != nil || reply[0] != 'S' {
		return fail()
	}
	clone := security.Clone()
	if clone.MinVersion < tls.VersionTLS12 {
		clone.MinVersion = tls.VersionTLS12
	}
	clone.ServerName = security.ServerName
	clone.InsecureSkipVerify = false
	clone.NextProtos = nil
	clone.ClientSessionCache = nil
	if security.RootCAs != nil {
		clone.RootCAs = security.RootCAs.Clone()
	}
	requested, selected := false, false
	clone.GetClientCertificate = func(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		requested = true
		if len(configured.Certificate) == 0 || request.SupportsCertificate(&configured) != nil {
			return nil, ErrProtocol
		}
		selected = true
		return &configured, nil
	}
	tlsConn := tls.Client(conn, clone)
	if err := tlsConn.HandshakeContext(ctx); err != nil || !requested || !selected {
		return fail()
	}
	if err := tlsConn.SetDeadline(time.Now().Add(startupTimeout)); err != nil {
		return fail()
	}
	session := &Session{conn: tlsConn, channel: channel, parameters: make(map[string]string)}
	startupErr := session.startup(ctx, database, username)
	stop()
	if startupErr != nil || ctx.Err() != nil {
		_ = session.Close()
		return fail()
	}
	if err := tlsConn.SetDeadline(time.Time{}); err != nil {
		return fail()
	}
	return session, nil
}

func certificateLeaf(cert tls.Certificate) (*x509.Certificate, error) {
	if len(cert.Certificate) == 0 {
		return nil, ErrProtocol
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || cert.Leaf != nil && !bytes.Equal(cert.Leaf.Raw, leaf.Raw) {
		return nil, ErrProtocol
	}
	return leaf, nil
}

func (s *Session) startup(ctx context.Context, database, username string) error {
	startup := make([]byte, 8, 128)
	binary.BigEndian.PutUint32(startup[4:8], 196608)
	for _, pair := range [][2]string{{"user", username}, {"database", database}, {"application_name", "airc-gateway"}, {"client_encoding", "UTF8"}} {
		startup = append(startup, pair[0]...)
		startup = append(startup, 0)
		startup = append(startup, pair[1]...)
		startup = append(startup, 0)
	}
	startup = append(startup, 0)
	binary.BigEndian.PutUint32(startup[:4], uint32(len(startup)))
	if len(startup) > maxFrameBytes || writeFull(s.conn, startup) != nil {
		return ErrProtocol
	}
	if err := s.startupMessages(ctx); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrProtocol
	}
	query := []byte(`LISTEN "` + s.channel + `"`)
	frame := make([]byte, 5+len(query)+1)
	frame[0] = 'Q'
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(frame)-1))
	copy(frame[5:], query)
	if err := writeFull(s.conn, frame); err != nil {
		return ErrProtocol
	}
	return s.listenReady(ctx)
}

func (s *Session) listenReady(ctx context.Context) error {
	commandComplete, ready := false, false
	notices := 0
	for !ready {
		typ, body, err := s.readStartupFrame(ctx)
		if err != nil {
			return ErrProtocol
		}
		switch typ {
		case 'A':
			payload, err := s.notification(body)
			if err != nil || len(s.pending) >= 16 {
				clear(payload)
				clear(body)
				return ErrProtocol
			}
			s.pending = append(s.pending, payload)
		case 'N':
			notices++
			if notices > 32 || s.notice(body) != nil {
				clear(body)
				return ErrProtocol
			}
		case 'S':
			if s.parameterStatus(body) != nil {
				clear(body)
				return ErrProtocol
			}
		case 'C':
			command, rest, ok := takeCString(body, 32)
			if commandComplete || !ok || command != "LISTEN" || len(rest) != 0 {
				clear(body)
				return ErrProtocol
			}
			commandComplete = true
		case 'Z':
			if !commandComplete || len(body) != 1 || body[0] != 'I' {
				clear(body)
				return ErrProtocol
			}
			ready = true
		default:
			clear(body)
			return ErrProtocol
		}
		clear(body)
	}
	if !ready || ctx.Err() != nil {
		return ErrProtocol
	}
	return nil
}

func (s *Session) startupMessages(ctx context.Context) error {
	authOK, backendKey, ready, encoding := false, false, false, false
	notices := 0
	for !ready {
		typ, body, err := s.readStartupFrame(ctx)
		if err != nil {
			return ErrProtocol
		}
		switch typ {
		case 'R':
			if authOK || len(body) != 4 || binary.BigEndian.Uint32(body) != 0 {
				return ErrProtocol
			}
			authOK = true
		case 'S':
			if !authOK || backendKey || s.parameterStatus(body) != nil {
				return ErrProtocol
			}
			if s.parameters["client_encoding"] == "UTF8" {
				encoding = true
			}
		case 'K':
			if !authOK || backendKey || len(body) != 8 {
				return ErrProtocol
			}
			backendKey = true
			clear(body)
		case 'N':
			notices++
			if notices > 32 || s.notice(body) != nil {
				return ErrProtocol
			}
		case 'Z':
			if !authOK || !backendKey || !encoding || len(body) != 1 || body[0] != 'I' {
				return ErrProtocol
			}
			ready = true
		default:
			return ErrProtocol
		}
		clear(body)
	}
	if !ready {
		return ErrProtocol
	}
	return nil
}

func (s *Session) readStartupFrame(ctx context.Context) (byte, []byte, error) {
	if s.startupFrames >= maxStartupFrames {
		return 0, nil, ErrProtocol
	}
	deadline, _ := ctx.Deadline()
	typ, body, err := readFrame(s.conn, deadline)
	if err == nil {
		s.startupFrames++
	}
	return typ, body, err
}

func writeFull(conn net.Conn, p []byte) error {
	for len(p) > 0 {
		n, err := conn.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("short postgres write")
		}
		p = p[n:]
	}
	return nil
}

func readFull(conn net.Conn, p []byte) (int, error) {
	read := 0
	for len(p) > 0 {
		n, err := conn.Read(p)
		read += n
		p = p[n:]
		if err != nil {
			return read, err
		}
		if n == 0 {
			return read, errors.New("short postgres read")
		}
	}
	return read, nil
}
