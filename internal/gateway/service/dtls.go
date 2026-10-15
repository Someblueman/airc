package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/pion/dtls/v3"
)

// dtlsOwner owns bounded UDP associations, including uncompleted handshakes.
// Transport identity never replaces the detached operation's AIRC authority.
type dtlsOwner struct {
	packets      *dtlsPacketListener
	d            *dispatch.Dispatcher
	requests     chan struct{}
	options      []dtls.ServerOption
	protocol     string
	alpn         string
	mu           sync.Mutex
	conns        map[*dtls.Conn]struct{}
	work         sync.WaitGroup
	serveStarted bool
	serveDone    chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

func newDTLSOwner(d *dispatch.Dispatcher, slots, requests chan struct{}, pair tls.Certificate, address, protocol string) (*dtlsOwner, error) {
	if d == nil || cap(slots) < 1 || cap(requests) < 1 || cap(requests) > config.MaxRequests || len(pair.Certificate) == 0 {
		return nil, store.ErrInvalid
	}
	alpn := adapter.DTLSALPN
	switch protocol {
	case "dtls":
	case "coaps":
		alpn = adapter.CoAPALPN
	default:
		return nil, store.ErrInvalid
	}
	// This certificate-only profile uses ephemeral ECDH and authenticated GCM.
	// Refuse incompatible keys before binding or publishing readiness.
	switch pair.PrivateKey.(type) {
	case *ecdsa.PrivateKey, *rsa.PrivateKey:
	default:
		return nil, errors.New("DTLS certificate requires an RSA or ECDSA key")
	}
	packets, err := newDTLSPacketListener(address, slots)
	if err != nil {
		return nil, err
	}
	return &dtlsOwner{
		packets: packets, d: d, requests: requests, protocol: protocol, alpn: alpn,
		options: []dtls.ServerOption{
			dtls.WithCertificates(pair), dtls.WithSupportedProtocols(alpn),
			dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, dtls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256),
			dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
			dtls.WithMTU(1200), dtls.WithReplayProtectionWindow(64),
		},
		conns: make(map[*dtls.Conn]struct{}), serveDone: make(chan struct{}),
	}, nil
}

func (o *dtlsOwner) addr() net.Addr { return o.packets.Addr() }

func (o *dtlsOwner) serve(ctx context.Context) error {
	o.mu.Lock()
	o.serveStarted = true
	o.mu.Unlock()
	defer close(o.serveDone)
	for {
		packet, remote, err := o.packets.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		peer := packet.(*dtlsPeer)
		conn, err := dtls.ServerWithOptions(peer, remote, o.options...)
		if err != nil {
			_ = peer.Close()
			peer.release()
			return err
		}
		o.mu.Lock()
		o.conns[conn] = struct{}{}
		o.mu.Unlock()
		o.work.Go(func() {
			defer func() {
				// Close joins Pion's handshaker before releasing its admission slot.
				_ = conn.Close()
				_ = peer.Close()
				peer.release()
				o.mu.Lock()
				delete(o.conns, conn)
				o.mu.Unlock()
			}()
			handshakeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.HandshakeContext(handshakeCtx)
			cancel()
			if err != nil {
				return
			}
			state, ok := conn.ConnectionState()
			if !ok || state.NegotiatedProtocol != o.alpn {
				return
			}
			peer.activity()
			if o.protocol == "coaps" {
				_ = adapter.ServeCoAP(ctx, conn, o.d, o.requests, peer.activity)
				return
			}
			_ = adapter.ServeDTLSDatagrams(ctx, conn, o.d, o.requests, peer.activity)
		})
	}
}

func (o *dtlsOwner) close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		// Stop admission and socket I/O first. This also wakes incomplete peers.
		o.closeErr = o.packets.Close()
		o.mu.Lock()
		started := o.serveStarted
		o.mu.Unlock()
		if started {
			<-o.serveDone
		}
		o.mu.Lock()
		conns := make([]*dtls.Conn, 0, len(o.conns))
		for conn := range o.conns {
			conns = append(conns, conn)
		}
		o.mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		o.work.Wait()
	})
	return o.closeErr
}
