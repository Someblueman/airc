package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// quicOwner owns the UDP socket, pre-handshake admission and accepted
// connections for raw QUIC, HTTP/3 and WebTransport listeners.
type quicOwner struct {
	server       *http3.Server
	webtransport *adapter.WebTransport
	raw          *adapter.QUIC
	transport    *quic.Transport
	listener     *quic.Listener
	packet       net.PacketConn
	slots        chan struct{}

	mu           sync.Mutex
	conns        map[*quic.Conn]struct{}
	work         sync.WaitGroup
	admissionMu  sync.Mutex
	admissionWG  sync.WaitGroup
	closing      bool
	serveDone    chan struct{}
	serveStarted bool
	closeOnce    sync.Once
	closeDone    chan struct{}
	closeErr     error
}

func newHTTP3Owner(parent context.Context, d *dispatch.Dispatcher, slots chan struct{}, origins []string, pair tls.Certificate, address string) (*quicOwner, error) {
	server, err := adapter.HTTP3Server(d, cap(slots), origins)
	if err != nil {
		return nil, err
	}
	return newHTTP3QUICOwner(parent, server, nil, slots, pair, address)
}

func newWebTransportOwner(parent context.Context, d *dispatch.Dispatcher, slots, requests chan struct{}, origins []string, pair tls.Certificate, address string) (*quicOwner, error) {
	transport, err := adapter.WebTransportServer(d, requests, origins)
	if err != nil {
		return nil, err
	}
	owner, err := newHTTP3QUICOwner(parent, transport.H3, transport, slots, pair, address)
	if err != nil {
		_ = transport.Close()
	}
	return owner, err
}

func newHTTP3QUICOwner(parent context.Context, server *http3.Server, webTransport *adapter.WebTransport, slots chan struct{}, pair tls.Certificate, address string) (*quicOwner, error) {
	server.TLSConfig = server.TLSConfig.Clone()
	server.TLSConfig.Certificates = []tls.Certificate{pair}
	server.TLSConfig.NextProtos = []string{"h3"}
	owner, err := newQUICOwner(parent, server.TLSConfig, server.QUICConfig, slots, address)
	if err != nil {
		return nil, err
	}
	owner.server, owner.webtransport = server, webTransport
	return owner, nil
}

func newRawQUICOwner(parent context.Context, d *dispatch.Dispatcher, slots, requests chan struct{}, pair tls.Certificate, address string, datagrams bool) (*quicOwner, error) {
	a, err := adapter.QUICServer(d, requests, datagrams)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, NextProtos: []string{adapter.QUICALPN}}
	quicConfig := &quic.Config{Allow0RTT: false, EnableDatagrams: datagrams, HandshakeIdleTimeout: 5 * time.Second, MaxIdleTimeout: 30 * time.Second, MaxIncomingStreams: 4, MaxIncomingUniStreams: 4, InitialStreamReceiveWindow: 64 << 10, MaxStreamReceiveWindow: 64 << 10, InitialConnectionReceiveWindow: 64 << 10, MaxConnectionReceiveWindow: 64 << 10}
	owner, err := newQUICOwner(parent, tlsConfig, quicConfig, slots, address)
	if err != nil {
		return nil, err
	}
	owner.raw = a
	return owner, nil
}

func newQUICOwner(parent context.Context, tlsConfig *tls.Config, quicConfig *quic.Config, slots chan struct{}, address string) (*quicOwner, error) {
	owner := &quicOwner{slots: slots, conns: make(map[*quic.Conn]struct{}), serveDone: make(chan struct{}), closeDone: make(chan struct{})}
	packetConn, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, err
	}
	transport := &quic.Transport{Conn: packetConn}
	transport.ConnContext = func(base context.Context, _ *quic.ClientInfo) (context.Context, error) {
		owner.admissionMu.Lock()
		defer owner.admissionMu.Unlock()
		if owner.closing {
			return nil, errors.New("QUIC listener is shutting down")
		}
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		select {
		case slots <- struct{}{}:
		default:
			return nil, errors.New("QUIC connection capacity exceeded")
		}
		ctx, cancel := context.WithCancel(base)
		parentDone := make(chan struct{})
		stopParent := context.AfterFunc(parent, func() {
			cancel()
			close(parentDone)
		})
		owner.admissionWG.Add(1)
		context.AfterFunc(ctx, func() {
			if !stopParent() {
				<-parentDone
			}
			<-slots
			owner.admissionWG.Done()
		})
		return ctx, nil
	}
	listener, err := transport.Listen(tlsConfig, quicConfig)
	if err != nil {
		_ = transport.Close()
		_ = packetConn.Close()
		return nil, err
	}
	owner.transport, owner.listener, owner.packet = transport, listener, packetConn
	return owner, nil
}

func (o *quicOwner) addr() net.Addr { return o.listener.Addr() }

func (o *quicOwner) serve(ctx context.Context) error {
	o.mu.Lock()
	o.serveStarted = true
	o.mu.Unlock()
	defer close(o.serveDone)
	for {
		conn, err := o.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, quic.ErrServerClosed) {
				return nil
			}
			return err
		}
		o.mu.Lock()
		o.conns[conn] = struct{}{}
		o.mu.Unlock()
		o.work.Go(func() {
			defer func() {
				o.mu.Lock()
				delete(o.conns, conn)
				o.mu.Unlock()
				_ = conn.CloseWithError(0, "")
			}()
			if o.raw != nil {
				_ = o.raw.ServeConn(ctx, conn)
			} else if o.webtransport != nil {
				_ = o.webtransport.ServeQUICConn(conn)
			} else {
				_ = o.server.ServeQUICConn(conn)
			}
		})
	}
}

func (o *quicOwner) close(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.admissionMu.Lock()
		o.closing = true
		o.admissionMu.Unlock()
		_ = o.listener.Close()
		o.mu.Lock()
		started := o.serveStarted
		o.mu.Unlock()
		if started {
			<-o.serveDone
		}
		if o.webtransport != nil || o.raw != nil {
			o.forceCloseConnections()
			if o.webtransport != nil {
				o.closeErr = o.webtransport.Close()
			}
			o.work.Wait()
			o.closeErr = errors.Join(o.closeErr, o.transport.Close(), o.packet.Close())
			o.admissionWG.Wait()
			close(o.closeDone)
			return
		}
		shutdownDone := make(chan error, 1)
		go func() { shutdownDone <- o.server.Shutdown(ctx) }()
		var err error
		select {
		case err = <-shutdownDone:
		case <-ctx.Done():
			o.forceCloseConnections()
			err = <-shutdownDone
		}
		o.work.Wait()
		_ = o.server.Close()
		if closeErr := o.transport.Close(); err == nil {
			err = closeErr
		}
		if closeErr := o.packet.Close(); err == nil {
			err = closeErr
		}
		o.admissionWG.Wait()
		o.closeErr = err
		close(o.closeDone)
	})
	<-o.closeDone
	return o.closeErr
}

func (o *quicOwner) forceCloseConnections() {
	o.mu.Lock()
	conns := make([]*quic.Conn, 0, len(o.conns))
	for conn := range o.conns {
		conns = append(conns, conn)
	}
	o.mu.Unlock()
	for _, conn := range conns {
		_ = conn.CloseWithError(0x100, "server shutdown")
	}
}
