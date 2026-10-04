package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// http3Owner owns the UDP transport, the handshake admission slots, and every
// QUIC connection passed to ServeQUICConn. The latter is necessary because the
// HTTP/3 server cannot close caller-owned ServeQUICConn connections for us.
type http3Owner struct {
	server       *http3.Server
	webtransport *adapter.WebTransport
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

func newHTTP3Owner(parent context.Context, d *dispatch.Dispatcher, slots chan struct{}, origins []string, pair tls.Certificate, address string) (*http3Owner, error) {
	server, err := adapter.HTTP3Server(d, cap(slots), origins)
	if err != nil {
		return nil, err
	}
	return newQUICOwner(parent, server, nil, slots, pair, address)
}

func newWebTransportOwner(parent context.Context, d *dispatch.Dispatcher, slots, requests chan struct{}, origins []string, pair tls.Certificate, address string) (*http3Owner, error) {
	transport, err := adapter.WebTransportServer(d, requests, origins)
	if err != nil {
		return nil, err
	}
	owner, err := newQUICOwner(parent, transport.H3, transport, slots, pair, address)
	if err != nil {
		_ = transport.Close()
	}
	return owner, err
}

func newQUICOwner(parent context.Context, server *http3.Server, webTransport *adapter.WebTransport, slots chan struct{}, pair tls.Certificate, address string) (*http3Owner, error) {
	server.TLSConfig = server.TLSConfig.Clone()
	server.TLSConfig.Certificates = []tls.Certificate{pair}
	server.TLSConfig.NextProtos = []string{"h3"}

	owner := &http3Owner{server: server, webtransport: webTransport, slots: slots, conns: make(map[*quic.Conn]struct{}), serveDone: make(chan struct{}), closeDone: make(chan struct{})}
	packetConn, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, err
	}
	transport := &quic.Transport{Conn: packetConn}
	transport.ConnContext = func(base context.Context, _ *quic.ClientInfo) (context.Context, error) {
		owner.admissionMu.Lock()
		defer owner.admissionMu.Unlock()
		if owner.closing {
			return nil, errors.New("HTTP/3 server is shutting down")
		}
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		select {
		case slots <- struct{}{}:
		default:
			return nil, errors.New("HTTP/3 connection capacity exceeded")
		}
		ctx, cancel := context.WithCancel(base)
		stopParent := context.AfterFunc(parent, cancel)
		owner.admissionWG.Add(1)
		context.AfterFunc(ctx, func() {
			stopParent()
			<-slots
			owner.admissionWG.Done()
		})
		return ctx, nil
	}
	listener, err := transport.Listen(server.TLSConfig, server.QUICConfig)
	if err != nil {
		_ = transport.Close()
		_ = packetConn.Close()
		return nil, err
	}
	owner.transport, owner.listener, owner.packet = transport, listener, packetConn
	return owner, nil
}

func (o *http3Owner) addr() net.Addr { return o.listener.Addr() }

func (o *http3Owner) serve(ctx context.Context) error {
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
			if o.webtransport != nil {
				_ = o.webtransport.ServeQUICConn(conn)
			} else {
				_ = o.server.ServeQUICConn(conn)
			}
		})
	}
}

func (o *http3Owner) close(ctx context.Context) error {
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
		if o.webtransport != nil {
			o.forceCloseConnections()
			o.closeErr = o.webtransport.Close()
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

func (o *http3Owner) forceCloseConnections() {
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
