//go:build darwin || linux

package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/quic-go/quic-go"
)

func TestRawQUICOwnerHandshakeAdmissionAndJoinedShutdown(t *testing.T) {
	for _, datagrams := range []bool{false, true} {
		t.Run(map[bool]string{false: "streams", true: "datagrams"}[datagrams], func(t *testing.T) {
			cert := testtls.New(t)
			pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			slots := make(chan struct{}, 1)
			owner, err := newRawQUICOwner(ctx, testDispatcher(t), slots, make(chan struct{}, 2), pair, "127.0.0.1:0", datagrams, adapter.Keepalive{})
			if err != nil {
				t.Fatal(err)
			}
			address := owner.addr().String()
			t.Cleanup(func() {
				cancel()
				shutdown, finish := context.WithTimeout(context.Background(), 2*time.Second)
				defer finish()
				if err := owner.close(shutdown); err != nil {
					t.Error(err)
				}
			})
			served := make(chan error, 1)
			go func() { served <- owner.serve(ctx) }()
			clientTLS := cert.Client.Clone()
			clientTLS.NextProtos = []string{adapter.QUICALPN}
			dialCtx, finish := context.WithTimeout(ctx, 3*time.Second)
			defer finish()
			conn, err := quic.DialAddr(dialCtx, address, clientTLS, &quic.Config{EnableDatagrams: datagrams})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseWithError(0, "")
			state := conn.ConnectionState()
			if !state.TLS.HandshakeComplete || state.TLS.Version != tls.VersionTLS13 || state.TLS.NegotiatedProtocol != adapter.QUICALPN || state.SupportsDatagrams.Remote != datagrams {
				t.Fatalf("handshake=%+v", state)
			}
			// A second real handshake must fail at the shared admission boundary.
			second, err := quic.DialAddr(dialCtx, address, clientTLS, &quic.Config{EnableDatagrams: datagrams})
			if second != nil {
				_ = second.CloseWithError(0, "")
				t.Fatal("full admission accepted second handshake")
			}
			var refusal *quic.TransportError
			if !errors.As(err, &refusal) || refusal.ErrorCode != quic.ConnectionRefused {
				t.Fatalf("admission error=%v", err)
			}
			stream, err := conn.OpenStreamSync(dialCtx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Write([]byte{0}); err != nil {
				t.Fatal(err)
			}
			cancel()
			shutdown, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			if err := owner.close(shutdown); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-served:
				if err != nil {
					t.Fatal(err)
				}
			case <-shutdown.Done():
				t.Fatal("QUIC owner did not join")
			}
			if len(slots) != 0 {
				t.Fatal("QUIC retained admission after shutdown")
			}
			packet, err := net.ListenPacket("udp", address)
			if err != nil {
				t.Fatalf("QUIC retained UDP socket: %v", err)
			}
			_ = packet.Close()
		})
	}
}

func TestRawQUICOwnerCloseBeforeServingReleasesUDP(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newRawQUICOwner(t.Context(), testDispatcher(t), make(chan struct{}, 1), make(chan struct{}, 1), pair, "127.0.0.1:0", false, adapter.Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	address := owner.addr().String()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range 2 {
		if err := owner.close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("unserved owner retained UDP socket: %v", err)
	}
	_ = packet.Close()
}

// A listener's keepalive sets the QUIC idle timeout the client gets, and its
// PINGs hold a quiet connection open past the client's own idle timeout.
func TestRawQUICOwnerKeepalive(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS := cert.Client.Clone()
	clientTLS.NextProtos = []string{adapter.QUICALPN}
	dial := func(keep adapter.Keepalive) *quic.Conn {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		owner, err := newRawQUICOwner(ctx, testDispatcher(t), make(chan struct{}, 1), make(chan struct{}, 1), pair, "127.0.0.1:0", false, keep)
		if err != nil {
			t.Fatal(err)
		}
		served := make(chan error, 1)
		go func() { served <- owner.serve(ctx) }()
		t.Cleanup(func() {
			cancel()
			shutdown, finish := context.WithTimeout(context.Background(), 2*time.Second)
			defer finish()
			_ = owner.close(shutdown)
			<-served
		})
		dialCtx, finish := context.WithTimeout(ctx, 3*time.Second)
		defer finish()
		// The client would wait 30 seconds; the server's idle timeout wins.
		conn, err := quic.DialAddr(dialCtx, owner.addr().String(), clientTLS, &quic.Config{MaxIdleTimeout: 30 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseWithError(0, "") })
		return conn
	}

	// quic-go accepts no peer idle timeout under five seconds, which is
	// also the configuration's floor.
	quiet := dial(adapter.Keepalive{Idle: 5 * time.Second})
	began := time.Now()
	select {
	case <-quiet.Context().Done():
		if waited := time.Since(began); waited < 4*time.Second {
			t.Fatal("closed before the idle timeout", waited)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a quiet connection outlived the listener's idle timeout")
	}

	pinged := dial(adapter.Keepalive{Idle: 5 * time.Second, Ping: time.Second})
	select {
	case <-pinged.Context().Done():
		t.Fatal("the listener's PINGs did not hold a quiet connection", context.Cause(pinged.Context()))
	case <-time.After(8 * time.Second):
	}
}

// A resumed client's 0-RTT request waits for the handshake: through a path
// that delivers only the client's Initial and 0-RTT packets, as a spoofed
// source or a replayed first flight does, it is never answered; once the
// handshake completes, it is.
func TestRawQUICHoldsEarlyDataUntilHandshake(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	owner, err := newRawQUICOwner(ctx, testDispatcher(t), make(chan struct{}, 4), make(chan struct{}, 2), pair, "127.0.0.1:0", false, adapter.Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- owner.serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		shutdown, finish := context.WithTimeout(context.Background(), 2*time.Second)
		defer finish()
		_ = owner.close(shutdown)
		<-served
	})
	clientTLS := cert.Client.Clone()
	clientTLS.NextProtos = []string{adapter.QUICALPN}
	tickets := make(chan struct{}, 1)
	clientTLS.ClientSessionCache = ticketCache{tls.NewLRUClientSessionCache(4), tickets}
	dialCtx, finish := context.WithTimeout(ctx, 10*time.Second)
	defer finish()
	// A full handshake leaves a session ticket.
	first, err := quic.DialAddr(dialCtx, owner.addr().String(), clientTLS, &quic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-tickets:
	case <-time.After(5 * time.Second):
		t.Fatal("no session ticket after the full handshake")
	}
	_ = first.CloseWithError(0, "")

	// answered reports whether the server served the stream: it answers
	// the empty request by ending the stream, or the read times out.
	answered := func(address string) (*quic.Conn, bool) {
		conn, err := quic.DialAddrEarly(dialCtx, address, clientTLS, &quic.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseWithError(0, "") })
		stream, err := conn.OpenStream()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write([]byte{0, 0, 0, 2, '{', '}'}); err != nil {
			t.Fatal(err)
		}
		_ = stream.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = stream.Read(make([]byte, 1))
		var timeout net.Error
		return conn, !errors.As(err, &timeout) || !timeout.Timeout()
	}
	if conn, ok := answered(earlyOnlyPath(t, owner.addr().String())); ok || !conn.ConnectionState().Used0RTT {
		t.Fatalf("an early-only path was answered: %v (0-RTT %v)", ok, conn.ConnectionState().Used0RTT)
	}
	if conn, ok := answered(owner.addr().String()); !ok || !conn.ConnectionState().Used0RTT {
		t.Fatalf("a resumed 0-RTT request was answered: %v (0-RTT %v)", ok, conn.ConnectionState().Used0RTT)
	}
}

// ticketCache signals each session ticket it stores.
type ticketCache struct {
	tls.ClientSessionCache
	stored chan struct{}
}

func (c ticketCache) Put(key string, session *tls.ClientSessionState) {
	c.ClientSessionCache.Put(key, session)
	if session != nil {
		select {
		case c.stored <- struct{}{}:
		default:
		}
	}
}

// earlyOnlyPath relays UDP to target, passing from the client only
// datagrams made of Initial and 0-RTT packets: the server's flight
// arrives, but the client's handshake never completes.
func earlyOnlyPath(t *testing.T, target string) string {
	t.Helper()
	front, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		t.Fatal(err)
	}
	back, err := net.DialUDP("udp", nil, server)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = front.Close(); _ = back.Close() })
	client := make(chan net.Addr, 1)
	go func() {
		buf := make([]byte, 2048)
		var from net.Addr
		for {
			n, err := back.Read(buf)
			if err != nil {
				return
			}
			if from == nil {
				from = <-client
			}
			_, _ = front.WriteTo(buf[:n], from)
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		sent := false
		for {
			n, from, err := front.ReadFrom(buf)
			if err != nil {
				return
			}
			if !sent {
				client <- from
				sent = true
			}
			if onlyEarlyPackets(buf[:n]) {
				_, _ = back.Write(buf[:n])
			}
		}
	}()
	return front.LocalAddr().String()
}

// onlyEarlyPackets reports whether a QUIC v1 datagram holds only long
// header Initial and 0-RTT packets.
func onlyEarlyPackets(b []byte) bool {
	varint := func(b []byte) (int, []byte, bool) {
		if len(b) == 0 {
			return 0, nil, false
		}
		n := 1 << (b[0] >> 6)
		if len(b) < n {
			return 0, nil, false
		}
		v := int(b[0] & 0x3f)
		for _, c := range b[1:n] {
			v = v<<8 | int(c)
		}
		return v, b[n:], true
	}
	for len(b) > 0 {
		kind := b[0] >> 4 & 3
		if b[0]&0x80 == 0 || kind > 1 || len(b) < 6 {
			return false
		}
		rest := b[5:]
		for range 2 { // destination and source connection IDs
			if len(rest) < 1 || len(rest) < 1+int(rest[0]) {
				return false
			}
			rest = rest[1+int(rest[0]):]
		}
		size, rest, ok := 0, rest, true
		if kind == 0 { // an Initial carries a token
			if size, rest, ok = varint(rest); !ok || len(rest) < size {
				return false
			}
			rest = rest[size:]
		}
		if size, rest, ok = varint(rest); !ok || len(rest) < size {
			return false
		}
		b = rest[size:]
	}
	return true
}
